//go:build (darwin || linux) && (amd64 || arm64)

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/ebitengine/purego"
)

// macOS·Linux 는 개발·시험용이다: onnxruntime 을 넣어 두지 않고, 환경 변수가 가리키는 라이브러리를 연다.
//
//	DPR_ONNXRUNTIME_LIB=/경로/libonnxruntime.dylib go test ./internal/platform/ -run Paddle -v
//
// (NuGet Microsoft.ML.OnnxRuntime 1.22.1 의 runtimes/osx-arm64/native/libonnxruntime.dylib 등)
const ortLibEnv = "DPR_ONNXRUNTIME_LIB"

// ortCall : C 함수 호출 (cgo 없이)
func ortCall(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := purego.SyscallN(fn, args...)
	return r
}

func ortCandidates() []ortCandidate {
	return []ortCandidate{{name: "onnxruntime (" + ortLibEnv + ")", open: func() (uintptr, error) {
		p := os.Getenv(ortLibEnv)
		if p == "" {
			return 0, errors.New("이 운영체제에는 onnxruntime 이 들어 있지 않습니다 (개발용: 환경 변수 " + ortLibEnv + " 에 libonnxruntime 경로)")
		}
		h, err := purego.Dlopen(p, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return 0, fmt.Errorf("%s 을 열지 못했습니다: %w", p, err)
		}
		return purego.Dlsym(h, "OrtGetApiBase")
	}}}
}

func paddleWorkerCmd(cmd *exec.Cmd) *exec.Cmd { return cmd }
