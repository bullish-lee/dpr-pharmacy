package platform

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"
)

// ONNX Runtime 의 C API 를 cgo 없이 부른다 (D25).
//
// onnxruntime 라이브러리(Windows: onnxruntime.dll)를 실행 중에 열고, OrtGetApiBase() 가 돌려주는 함수 표(OrtApi)의
// 함수 주소를 직접 호출한다. Windows 는 syscall.SyscallN, macOS·Linux(개발·시험용)는 purego.SyscallN — ortCall.
// 그래서 CGO_ENABLED=0 으로도 빌드된다.
//
// 함수 표의 순서는 ONNX Runtime 이 버전을 올려도 바꾸지 않고 뒤에만 덧붙인다 (ABI 약속).
// 아래 번호는 onnxruntime_c_api.h (1.22.1) 의 struct OrtApi 에서 뽑았고 1.32.0 과도 같다.
// 모두 API 버전 1 부터 있는 함수라 GetApi(1) 로 받는다 — Windows 에 들어 있는 옛 onnxruntime.dll 도 쓸 수 있게.
const (
	ortGetErrorMessage                = 2
	ortCreateEnv                      = 3
	ortCreateSessionFromArray         = 8
	ortRun                            = 9
	ortCreateSessionOptions           = 10
	ortSetIntraOpNumThreads           = 24
	ortCreateTensorWithDataAsOrtValue = 49
	ortGetTensorMutableData           = 51
	ortGetDimensionsCount             = 61
	ortGetDimensions                  = 62
	ortGetTensorTypeAndShape          = 65
	ortCreateCpuMemoryInfo            = 69
	ortReleaseEnv                     = 92
	ortReleaseStatus                  = 93
	ortReleaseMemoryInfo              = 94
	ortReleaseSession                 = 95
	ortReleaseValue                   = 96
	ortReleaseTensorTypeAndShapeInfo  = 99
	ortReleaseSessionOptions          = 100

	ortAPIVersion    = 1
	ortLogError      = 3 // ORT_LOGGING_LEVEL_ERROR
	ortArenaAlloc    = 1 // OrtArenaAllocator
	ortMemDefault    = 0 // OrtMemTypeDefault
	ortElemTypeFloat = 1 // ONNX_TENSOR_ELEMENT_DATA_TYPE_FLOAT
)

// ortCandidate : 열어 볼 onnxruntime 라이브러리 하나 (앞에서부터 차례로 시도)
type ortCandidate struct {
	name string
	open func() (getAPIBase uintptr, err error) // OrtGetApiBase 함수 주소
}

// ortRuntime : 열린 onnxruntime
type ortRuntime struct {
	api     uintptr // const OrtApi*
	env     uintptr
	mem     uintptr
	name    string // 어느 라이브러리인지 (점검 화면에 보여 준다)
	version string
}

// ptrAt : C 가 돌려준 주소를 Go 포인터로 (go vet 의 unsafe.Pointer(uintptr) 경고 없이)
func ptrAt(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

// cString : C 문자열(\0 으로 끝남)을 읽는다
func cString(p uintptr) string {
	if p == 0 {
		return ""
	}
	base := ptrAt(p)
	n := 0
	for n < 4096 && *(*byte)(unsafe.Add(base, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(base), n))
}

// fn : 함수 표의 i 번째 함수 주소
func (o *ortRuntime) fn(i int) uintptr {
	return *(*uintptr)(unsafe.Add(ptrAt(o.api), i*int(unsafe.Sizeof(uintptr(0)))))
}

// status : OrtStatus* 를 Go 오류로 (0 이면 성공)
func (o *ortRuntime) status(st uintptr) error {
	if st == 0 {
		return nil
	}
	msg := cString(ortCall(o.fn(ortGetErrorMessage), st))
	ortCall(o.fn(ortReleaseStatus), st)
	return errors.New("onnxruntime: " + msg)
}

// ortPins : C 로 넘기는 Go 메모리를 호출이 끝날 때까지 고정한다.
// (스택에 있는 변수의 주소를 uintptr 로 넘기면 호출 도중 스택이 옮겨질 수 있다 — Pin 은 변수를 힙에 두게 한다)
type ortPins struct{ p runtime.Pinner }

func (a *ortPins) ptr(p unsafe.Pointer) uintptr {
	a.p.Pin(p)
	return uintptr(p)
}
func (a *ortPins) done() { a.p.Unpin() }

// openORT 는 후보 하나를 열어 API·환경을 준비한다.
func openORT(c ortCandidate) (rt *ortRuntime, err error) {
	sym, err := c.open()
	if err != nil {
		return nil, err
	}
	if sym == 0 {
		return nil, errors.New("OrtGetApiBase 를 찾지 못했습니다")
	}
	base := ortCall(sym)
	if base == 0 {
		return nil, errors.New("OrtGetApiBase 가 빈 값을 돌려줬습니다")
	}
	// struct OrtApiBase { GetApi; GetVersionString; }
	getAPI := *(*uintptr)(ptrAt(base))
	getVer := *(*uintptr)(unsafe.Add(ptrAt(base), unsafe.Sizeof(uintptr(0))))
	o := &ortRuntime{name: c.name}
	o.version = cString(ortCall(getVer))
	o.api = ortCall(getAPI, ortAPIVersion)
	if o.api == 0 {
		return nil, fmt.Errorf("onnxruntime %s 이 API 버전 %d 을 지원하지 않습니다", o.version, ortAPIVersion)
	}
	var pins ortPins
	defer pins.done()
	logID := append([]byte("dpr"), 0)
	env := new(uintptr)
	if err := o.status(ortCall(o.fn(ortCreateEnv), ortLogError, pins.ptr(unsafe.Pointer(&logID[0])), pins.ptr(unsafe.Pointer(env)))); err != nil {
		return nil, err
	}
	o.env = *env
	mem := new(uintptr)
	if err := o.status(ortCall(o.fn(ortCreateCpuMemoryInfo), ortArenaAlloc, ortMemDefault, pins.ptr(unsafe.Pointer(mem)))); err != nil {
		ortCall(o.fn(ortReleaseEnv), o.env)
		return nil, err
	}
	o.mem = *mem
	return o, nil
}

func (o *ortRuntime) close() {
	if o.mem != 0 {
		ortCall(o.fn(ortReleaseMemoryInfo), o.mem)
		o.mem = 0
	}
	if o.env != 0 {
		ortCall(o.fn(ortReleaseEnv), o.env)
		o.env = 0
	}
}

// ortSession : 모델 하나 (입력 1개 → 출력 1개, 모두 float32)
type ortSession struct {
	rt      *ortRuntime
	h       uintptr
	in, out []byte // 입력·출력 이름 (\0 으로 끝남)
}

// newSession 은 메모리에 있는 모델(내장 파일)로 세션을 만든다 — 파일 경로를 쓰지 않아 한글 경로 문제가 없다.
func (o *ortRuntime) newSession(model []byte, inName, outName string, threads int) (*ortSession, error) {
	if len(model) == 0 {
		return nil, errors.New("모델이 비었습니다")
	}
	var pins ortPins
	defer pins.done()
	opts := new(uintptr)
	if err := o.status(ortCall(o.fn(ortCreateSessionOptions), pins.ptr(unsafe.Pointer(opts)))); err != nil {
		return nil, err
	}
	defer ortCall(o.fn(ortReleaseSessionOptions), *opts)
	if threads > 0 {
		if err := o.status(ortCall(o.fn(ortSetIntraOpNumThreads), *opts, uintptr(threads))); err != nil {
			return nil, err
		}
	}
	h := new(uintptr)
	if err := o.status(ortCall(o.fn(ortCreateSessionFromArray), o.env, pins.ptr(unsafe.Pointer(&model[0])), uintptr(len(model)), *opts, pins.ptr(unsafe.Pointer(h)))); err != nil {
		return nil, err
	}
	return &ortSession{rt: o, h: *h, in: append([]byte(inName), 0), out: append([]byte(outName), 0)}, nil
}

func (s *ortSession) close() {
	if s != nil && s.h != 0 {
		ortCall(s.rt.fn(ortReleaseSession), s.h)
		s.h = 0
	}
}

// run 은 float32 텐서 하나를 넣어 float32 텐서 하나를 받는다 (결과는 Go 메모리로 복사).
func (s *ortSession) run(data []float32, shape []int64) (out []float32, outShape []int64, err error) {
	n := int64(1)
	for _, d := range shape {
		n *= d
	}
	if n <= 0 || int64(len(data)) != n {
		return nil, nil, fmt.Errorf("입력 크기가 맞지 않습니다 (%v, %d개)", shape, len(data))
	}
	o := s.rt
	var pins ortPins
	defer pins.done()

	inVal := new(uintptr)
	if err := o.status(ortCall(o.fn(ortCreateTensorWithDataAsOrtValue), o.mem,
		pins.ptr(unsafe.Pointer(&data[0])), uintptr(len(data)*4),
		pins.ptr(unsafe.Pointer(&shape[0])), uintptr(len(shape)), ortElemTypeFloat, pins.ptr(unsafe.Pointer(inVal)))); err != nil {
		return nil, nil, err
	}
	defer ortCall(o.fn(ortReleaseValue), *inVal)

	inNames := new(uintptr)
	*inNames = pins.ptr(unsafe.Pointer(&s.in[0]))
	outNames := new(uintptr)
	*outNames = pins.ptr(unsafe.Pointer(&s.out[0]))
	outVal := new(uintptr)
	if err := o.status(ortCall(o.fn(ortRun), s.h, 0,
		pins.ptr(unsafe.Pointer(inNames)), pins.ptr(unsafe.Pointer(inVal)), 1,
		pins.ptr(unsafe.Pointer(outNames)), 1, pins.ptr(unsafe.Pointer(outVal)))); err != nil {
		return nil, nil, err
	}
	defer ortCall(o.fn(ortReleaseValue), *outVal)

	info := new(uintptr)
	if err := o.status(ortCall(o.fn(ortGetTensorTypeAndShape), *outVal, pins.ptr(unsafe.Pointer(info)))); err != nil {
		return nil, nil, err
	}
	defer ortCall(o.fn(ortReleaseTensorTypeAndShapeInfo), *info)
	rank := new(uintptr)
	if err := o.status(ortCall(o.fn(ortGetDimensionsCount), *info, pins.ptr(unsafe.Pointer(rank)))); err != nil {
		return nil, nil, err
	}
	if *rank == 0 || *rank > 8 {
		return nil, nil, fmt.Errorf("출력 차원이 이상합니다 (%d)", *rank)
	}
	outShape = make([]int64, *rank)
	if err := o.status(ortCall(o.fn(ortGetDimensions), *info, pins.ptr(unsafe.Pointer(&outShape[0])), *rank)); err != nil {
		return nil, nil, err
	}
	total := int64(1)
	for _, d := range outShape {
		if d <= 0 || d > 1<<28 {
			return nil, nil, fmt.Errorf("출력 크기가 이상합니다 (%v)", outShape)
		}
		total *= d
	}
	if total > 1<<30 {
		return nil, nil, fmt.Errorf("출력이 너무 큽니다 (%v)", outShape)
	}
	ptr := new(uintptr)
	if err := o.status(ortCall(o.fn(ortGetTensorMutableData), *outVal, pins.ptr(unsafe.Pointer(ptr)))); err != nil {
		return nil, nil, err
	}
	if *ptr == 0 {
		return nil, nil, errors.New("출력이 비었습니다")
	}
	out = make([]float32, total)
	copy(out, unsafe.Slice((*float32)(ptrAt(*ptr)), total))
	runtime.KeepAlive(data)
	runtime.KeepAlive(shape)
	return out, outShape, nil
}

// joinErrs : 후보마다의 실패 이유를 한 줄로
func joinErrs(errs []string) string { return strings.Join(errs, " / ") }
