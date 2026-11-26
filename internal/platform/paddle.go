package platform

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// PaddleOCR(PP-OCRv5, 한국어) — Windows 기본 글자 인식 옆에서 도는 두 번째 엔진. 무료·오프라인 (D25).
//
// 두 엔진이 같은 칸을 같게 읽으면 믿고, 다르게 읽으면 사용자가 고른다 (internal/service/lotocr_compare.go).
// 결과는 Windows 엔진과 같은 OCRResult(낱말과 위치)라 표 읽기를 그대로 쓴다.
//
//   - 모델 2개(글줄 검출 4.8MB · 한국어 글자 읽기 13.4MB)와 글자 사전은 프로그램 안에 들어 있다 — 인터넷을 쓰지 않는다.
//   - 계산은 ONNX Runtime 이 한다 (cgo 없이 부름 — paddle_ort.go).
//   - 화면을 띄우는 프로그램(DPR.exe)에서는 자기 자신을 '일꾼'으로 한 번 더 실행해 그 안에서 읽는다 (RunPaddleWorker).
//     onnxruntime 안에서 죽거나 멈춰도 프로그램은 살아 있고, 시간 제한으로 끊을 수 있다.
//   - 어떤 이유로든 안 되면 ErrPaddleUnavailable — 부르는 쪽은 Windows 글자 인식만으로 계속한다.

// 프로그램에 든 파일 (internal/platform/paddle/) — 공식 배포처에서 받은 그대로이고, 시험(TestPaddleAssetsPinned)이 해시를 확인한다.
//
//	PP-OCRv5_mobile_det.onnx (4,826,518 바이트, Apache-2.0)
//	  https://huggingface.co/PaddlePaddle/PP-OCRv5_mobile_det_onnx/resolve/e6f4fa85f00e168c862bc462aebca69eef9b3d3d/inference.onnx
//	  SHA-256 a431985659dc921974177a95adcfbb90fd9e51989a5e04d70d0b75f597b6e61d
//	korean_PP-OCRv5_mobile_rec.onnx (13,418,787 바이트, Apache-2.0)
//	  https://huggingface.co/PaddlePaddle/korean_PP-OCRv5_mobile_rec_onnx/resolve/5c6f574b8e2230adf4287b33e736d71b9fabd28e/inference.onnx
//	  SHA-256 92f0b7785e64fc9090106a241cf4c1eb97472824558272751b88a2a4476d3a08
//	korean_dict.txt (글자 11,945개, 한 줄에 하나) — 같은 저장소·같은 판의 inference.yml
//	  (SHA-256 f757fa1c40e99edcf27e9cce879b93eb2a51fa46f5ef39095689b8c37dd75998) 의 PostProcess.character_dict 를 차례대로 옮긴 것
//	  SHA-256 a88071c68c01707489baa79ebe0405b7beb5cca229f4fc94cc3ef992328802d7
//	onnxruntime.dll (Windows x64, 12,416,032 바이트, MIT, Microsoft 전자서명) — NuGet Microsoft.ML.OnnxRuntime 1.22.1 의
//	  runtimes/win-x64/native/onnxruntime.dll
//	  https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime/1.22.1/microsoft.ml.onnxruntime.1.22.1.nupkg
//	  (nupkg SHA-256 7b091e012dcfef2f9cc965f2252041101b1840c748ffbc1c18ba047bd6508b1c)
//	  SHA-256 7788f3f38e9a339003f7d7e1bf47f928287cf409bb5273273149e1282fbf503f
//
// 검출 모델은 서버용(88MB)이 아니라 모바일용(4.8MB)이다. 가상 표 그림 4장으로 잰 결과(개발 PC, 2026-11-26):
// 모바일용은 전 칸(135칸)을 읽었고, 서버용은 검출이 9~10배 느리면서(3200×2400 사진 8.9초 대 1.0초) 더 맞히지 못했다.

//go:embed paddle/PP-OCRv5_mobile_det.onnx
var paddleDetModel []byte

//go:embed paddle/korean_PP-OCRv5_mobile_rec.onnx
var paddleRecModel []byte

//go:embed paddle/korean_dict.txt
var paddleDictText string

const (
	// PaddleEngine : OCRResult.Engine 값 (Windows 엔진은 인식 언어 — ko 등)
	PaddleEngine = "paddle-ko"

	paddleDetSHA256  = "a431985659dc921974177a95adcfbb90fd9e51989a5e04d70d0b75f597b6e61d"
	paddleRecSHA256  = "92f0b7785e64fc9090106a241cf4c1eb97472824558272751b88a2a4476d3a08"
	paddleDictSHA256 = "a88071c68c01707489baa79ebe0405b7beb5cca229f4fc94cc3ef992328802d7"

	paddleInput  = "x"
	paddleOutput = "fetch_name_0"

	// 긴 변이 이보다 크면 줄여서 읽는다 (1200만 화소 사진 4000px → 0.8배).
	// 가상 사진(3200×2400)으로 잰 결과: 0.8배 이하로 줄이면 붙어 있는 두 칸이 한 줄로 묶여 1칸을 놓쳤다.
	// 이 크기에서 일꾼 프로세스가 메모리를 1.6GB 까지 쓴다 (읽고 나면 끝나며 돌려준다) — 메모리가 모자란 PC 가 있으면 줄인다.
	paddleMaxSide = 3200

	paddleWorkerArg = "paddle-ocr-worker"
	paddlePathsEnv  = "DPR_OCR_PATHS"
)

// ErrPaddleUnavailable : 이 PC 에서 PaddleOCR 을 쓸 수 없음 — Windows 글자 인식만으로 계속한다
var ErrPaddleUnavailable = errors.New("이 PC 에서는 보조 글자 인식(PaddleOCR)을 쓸 수 없습니다")

type paddleEngine struct {
	rt       *ortRuntime
	det, rec *ortSession
	dict     []rune
}

var paddleState struct {
	mu   sync.Mutex // 한 번에 한 장씩 (onnxruntime 이 안에서 여러 코어를 쓴다)
	once sync.Once
	eng  *paddleEngine
	err  error
}

func paddleDict() []rune {
	lines := strings.Split(strings.TrimRight(paddleDictText, "\n"), "\n")
	d := make([]rune, len(lines))
	for i, l := range lines {
		r := []rune(l)
		if len(r) != 1 {
			d[i] = '�'
			continue
		}
		d[i] = r[0]
	}
	return d
}

// paddleLoad : onnxruntime 을 열고 모델 두 개를 올린다 (한 번만). 후보 라이브러리를 차례로 시도한다.
func paddleLoad() (*paddleEngine, error) {
	paddleState.once.Do(func() {
		threads := min(runtime.NumCPU(), 4) // PC 가 느려지지 않게 4개까지만
		var errs []string
		for _, c := range ortCandidates() {
			rt, err := openORT(c)
			if err != nil {
				errs = append(errs, c.name+": "+err.Error())
				continue
			}
			det, err := rt.newSession(paddleDetModel, paddleInput, paddleOutput, threads)
			if err != nil {
				errs = append(errs, c.name+": 검출 모델 — "+err.Error())
				rt.close()
				continue
			}
			rec, err := rt.newSession(paddleRecModel, paddleInput, paddleOutput, threads)
			if err != nil {
				errs = append(errs, c.name+": 글자 모델 — "+err.Error())
				det.close()
				rt.close()
				continue
			}
			paddleState.eng = &paddleEngine{rt: rt, det: det, rec: rec, dict: paddleDict()}
			return
		}
		if len(errs) == 0 {
			errs = []string{"이 운영체제에서는 지원하지 않습니다"}
		}
		paddleState.err = fmt.Errorf("%w — %s", ErrPaddleUnavailable, joinErrs(errs))
	})
	return paddleState.eng, paddleState.err
}

// read : 그림 한 장을 읽는다
func (e *paddleEngine) read(ctx context.Context, path string) (res OCRResult) {
	res.Engine = PaddleEngine
	res.Runtime = e.rt.name + " " + e.rt.version
	defer func() { // 그림 손질 중의 예상 못한 오류로 프로그램이 죽지 않게
		if r := recover(); r != nil {
			res.Lines, res.Error = nil, fmt.Sprintf("글자 인식 중 오류: %v", r)
		}
	}()
	src, err := loadBGR(path)
	if err != nil {
		res.Error = "그림을 읽지 못했습니다: " + err.Error()
		return res
	}
	res.Width, res.Height = src.w, src.h
	lines, angle, err := e.readImage(ctx, src)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Lines, res.Angle = lines, angle
	return res
}

func (e *paddleEngine) readImage(ctx context.Context, src *bgrImg) ([]OCRLine, float64, error) {
	// 너무 큰 사진은 줄인다 (좌표는 끝에 원래 크기로 되돌린다)
	img, k := src, 1.0
	if m := max(src.w, src.h); m > paddleMaxSide {
		k = paddleMaxSide / float64(m)
		img = resizeArea(src, max(int(math.Round(float64(src.w)*k)), 1), max(int(math.Round(float64(src.h)*k)), 1))
	}
	// 1. 글줄 검출
	in, h, w := detInput(img)
	prob, shape, err := e.det.run(in, []int64{1, 3, int64(h), int64(w)})
	if err != nil {
		return nil, 0, err
	}
	if len(shape) != 4 || shape[0] != 1 || shape[1] != 1 {
		return nil, 0, fmt.Errorf("검출 모델 출력이 예상과 다릅니다 (%v)", shape)
	}
	boxes := detBoxes(prob, int(shape[3]), int(shape[2]), img.w, img.h)
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	// 2. 글줄마다 잘라서 — 가로세로비가 비슷한 것끼리 묶어 읽는다
	type item struct {
		box     quad
		crop    *bgrImg
		rotated bool
		line    recLine
	}
	var items []*item
	for _, b := range boxes {
		if c, rot := cropLine(img, b); c != nil {
			items = append(items, &item{box: b, crop: c, rotated: rot})
		}
	}
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ca, cb := items[order[a]].crop, items[order[b]].crop
		return float64(ca.w)/float64(ca.h) < float64(cb.w)/float64(cb.h)
	})
	for beg := 0; beg < len(order); beg += recBatch {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		idx := order[beg:min(beg+recBatch, len(order))]
		crops := make([]*bgrImg, len(idx))
		for i, j := range idx {
			crops[i] = items[j].crop
		}
		data, padW, lineW := recInput(crops)
		out, shape, err := e.rec.run(data, []int64{int64(len(crops)), 3, recHeight, int64(padW)})
		if err != nil {
			return nil, 0, err
		}
		if len(shape) != 3 || int(shape[0]) != len(crops) || int(shape[2]) != len(e.dict)+2 {
			return nil, 0, fmt.Errorf("글자 모델 출력이 사전과 맞지 않습니다 (%v, 사전 %d자)", shape, len(e.dict))
		}
		steps, classes := int(shape[1]), int(shape[2])
		for i, j := range idx {
			chars := ctcDecode(out[i*steps*classes:(i+1)*steps*classes], steps, classes, e.dict)
			items[j].line = newRecLine(chars, steps, padW, lineW[i])
		}
	}
	// 3. 낱말과 위치 (원본 좌표)
	var lines []OCRLine
	var angles []float64
	for _, it := range items {
		l := it.line
		if l.text == "" || l.conf < recMinConf {
			continue
		}
		q := it.box
		ws := l.words()
		if it.rotated || len(ws) == 0 { // 세로 글줄은 나누지 않는다
			ws = []recWord{{text: l.text, f0: 0, f1: 1, conf: l.conf}}
		} else if dist(q[0], q[1]) > 1.5*dist(q[0], q[3]) {
			angles = append(angles, math.Atan2(q[1].y-q[0].y, q[1].x-q[0].x)*180/math.Pi)
		}
		line := OCRLine{Text: l.text}
		for _, wd := range ws {
			// 글줄 상자의 윗변·아랫변을 따라 f0~f1 구간
			at := func(a, b pt, f float64) pt { return pt{a.x + (b.x-a.x)*f, a.y + (b.y-a.y)*f} }
			cs := [4]pt{at(q[0], q[1], wd.f0), at(q[0], q[1], wd.f1), at(q[3], q[2], wd.f0), at(q[3], q[2], wd.f1)}
			x0, y0, x1, y1 := cs[0].x, cs[0].y, cs[0].x, cs[0].y
			for _, c := range cs[1:] {
				x0, x1 = math.Min(x0, c.x), math.Max(x1, c.x)
				y0, y1 = math.Min(y0, c.y), math.Max(y1, c.y)
			}
			line.Words = append(line.Words, OCRWord{
				Text: wd.text, X: math.Round(x0 / k), Y: math.Round(y0 / k), W: math.Round((x1 - x0) / k), H: math.Round((y1 - y0) / k),
				C: math.Round(wd.conf*1000) / 1000,
			})
		}
		lines = append(lines, line)
	}
	// 글줄 기울기(시계 방향 +, 도) — 표 읽기가 이만큼 되돌려 줄을 맞춘다 (Windows 엔진의 TextAngle 과 같은 뜻)
	angle := 0.0
	if len(angles) >= 3 {
		sort.Float64s(angles)
		if a := angles[len(angles)/2]; math.Abs(a) >= 0.2 && math.Abs(a) < 45 {
			angle = math.Round(a*100) / 100
		}
	}
	return lines, angle, nil
}

// paddleInProcess : 이 프로세스 안에서 읽는다 (일꾼 프로세스·시험)
func paddleInProcess(ctx context.Context, paths []string) ([]OCRResult, error) {
	eng, err := paddleLoad()
	if err != nil {
		return nil, err
	}
	paddleState.mu.Lock()
	defer paddleState.mu.Unlock()
	res := make([]OCRResult, 0, len(paths))
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("보조 글자 인식이 시간 안에 끝나지 않았습니다: %w", err)
		}
		res = append(res, eng.read(ctx, p))
	}
	return res, checkPaddleResults(res)
}

func checkPaddleResults(res []OCRResult) error {
	if len(res) == 0 {
		return fmt.Errorf("%w — 결과가 비었습니다", ErrPaddleUnavailable)
	}
	for _, r := range res {
		if r.Error == "" {
			return nil // 일부만 실패하면 나머지로 진행
		}
	}
	return fmt.Errorf("%w — %s", ErrPaddleUnavailable, res[0].Error)
}

// paddleUseWorker : RunPaddleWorker 를 부른 프로그램(DPR.exe)은 자기 자신을 일꾼으로 실행할 수 있다
var paddleUseWorker bool

// RunPaddleWorker 는 main() 맨 앞에서 부른다. 일꾼으로 실행된 것이면 그림을 읽어 결과(JSON)를 표준 출력에 쓰고 true.
//
//	if platform.RunPaddleWorker(os.Args) { return }
func RunPaddleWorker(args []string) bool {
	paddleUseWorker = true
	if len(args) < 2 || args[1] != paddleWorkerArg {
		return false
	}
	var paths []string
	var res []OCRResult
	err := json.Unmarshal([]byte(os.Getenv(paddlePathsEnv)), &paths)
	if err == nil {
		res, err = paddleInProcess(context.Background(), paths)
	}
	if len(res) == 0 && err != nil { // 엔진을 못 열었음 — 이유를 결과에 담아 돌려준다 (부르는 쪽이 오류로 바꾼다)
		res = []OCRResult{{Engine: PaddleEngine, Error: strings.TrimPrefix(err.Error(), ErrPaddleUnavailable.Error()+" — ")}}
	}
	out, _ := json.Marshal(res)
	os.Stdout.Write(out)
	// 결과를 다 쓴 뒤에 정리한다 (정리하다 문제가 생겨도 결과는 이미 나갔다)
	if e := paddleState.eng; e != nil {
		e.det.close()
		e.rec.close()
		e.rt.close()
	}
	return true
}

// PaddleOCR 은 이미지 파일들을 PaddleOCR(PP-OCRv5 한국어)로 읽는다. 결과 모양은 OCR(Windows 엔진)과 같다.
// 쓸 수 없으면 ErrPaddleUnavailable 로 감싼 오류 — 부르는 쪽은 Windows 글자 인식만으로 계속한다.
func PaddleOCR(ctx context.Context, paths []string) ([]OCRResult, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if len(ortCandidates()) == 0 {
		return nil, fmt.Errorf("%w — 이 운영체제에서는 지원하지 않습니다", ErrPaddleUnavailable)
	}
	if !paddleUseWorker {
		return paddleInProcess(ctx, paths)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("%w — %v", ErrPaddleUnavailable, err)
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		if a, err := filepath.Abs(p); err == nil {
			p = a
		}
		abs[i] = p
	}
	pj, _ := json.Marshal(abs)
	cmd := paddleWorkerCmd(exec.CommandContext(ctx, exe, paddleWorkerArg))
	cmd.Env = append(os.Environ(), paddlePathsEnv+"="+string(pj))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w — 시간 안에 끝나지 않았습니다: %v", ErrPaddleUnavailable, ctx.Err())
	}
	tail := strings.TrimSpace(errb.String())
	if len(tail) > 300 {
		tail = tail[len(tail)-300:]
	}
	// 결과가 온전히 나왔으면 끝날 때의 상태와 상관없이 쓴다 (다 읽고 나서 정리하다 죽은 경우)
	res, err := parseOCROutput(out.Bytes())
	if err != nil || len(res) != len(paths) {
		if runErr != nil { // onnxruntime 안에서 죽었거나 보안 프로그램이 실행을 막음
			return nil, fmt.Errorf("%w — 실행 실패: %v %s", ErrPaddleUnavailable, runErr, tail)
		}
		if err == nil { // 엔진을 못 열면 일꾼이 이유 하나만 돌려준다
			return res, checkPaddleResults(res)
		}
		return nil, fmt.Errorf("%w — 결과를 읽지 못했습니다: %v %s", ErrPaddleUnavailable, err, tail)
	}
	return res, checkPaddleResults(res)
}

// PaddleCheck : 보조 글자 인식(PaddleOCR) 점검 결과 — OCRCheck.Paddle
type PaddleCheck struct {
	OK      bool   `json:"ok"`
	Runtime string `json:"runtime"` // 쓴 onnxruntime (내장 / Windows 기본)
	Korean  bool   `json:"korean"`  // 점검 그림의 한글(도매 이름)을 읽음
	Found   int    `json:"found"`   // 점검 그림에서 찾은 제조번호 수 (3개 중)
	Text    string `json:"text"`
	Problem string `json:"problem"`
	Ms      int64  `json:"ms"`
}

// paddleSelfTest : 점검 그림을 PaddleOCR 로 읽어 본다
func paddleSelfTest(ctx context.Context) *PaddleCheck {
	c := &PaddleCheck{}
	dir, err := os.MkdirTemp("", "dpr-ocrcheck-")
	if err != nil {
		c.Problem = err.Error()
		return c
	}
	defer os.RemoveAll(dir)
	img := filepath.Join(dir, "sample.png")
	if err := os.WriteFile(img, ocrSample, 0o600); err != nil {
		c.Problem = err.Error()
		return c
	}
	start := time.Now()
	res, err := PaddleOCR(ctx, []string{img})
	c.Ms = time.Since(start).Milliseconds()
	if err != nil {
		c.Problem = err.Error() + ". Windows 글자 인식만으로 읽습니다."
		return c
	}
	var sb strings.Builder
	for _, r := range res {
		if r.Runtime != "" {
			c.Runtime = r.Runtime
		}
		for _, l := range r.Lines {
			sb.WriteString(l.Text)
			sb.WriteByte('\n')
		}
	}
	text := sb.String()
	for _, lot := range []string{"1001", "1002", "1003"} {
		if strings.Contains(text, lot) {
			c.Found++
		}
	}
	c.Korean = strings.Contains(text, "도매")
	c.OK = c.Found > 0
	if !c.OK {
		c.Problem = "보조 글자 인식은 실행되지만 점검 그림의 제조번호를 읽지 못했습니다. Windows 글자 인식만으로 읽습니다."
	}
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200])
	}
	c.Text = text
	return c
}
