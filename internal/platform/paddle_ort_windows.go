//go:build windows && amd64

package platform

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// onnxruntime.dll (Microsoft 가 서명한 공식 파일, 1.22.1) — 출처·해시는 paddle.go
//
//go:embed paddle/onnxruntime.dll
var ortDLL []byte

const (
	ortDLLVersion = "1.22.1"
	ortDLLSHA256  = "7788f3f38e9a339003f7d7e1bf47f928287cf409bb5273273149e1282fbf503f"
)

// ortCall : C 함수 호출 (cgo 없이)
func ortCall(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}

// ortCandidates : (1) 프로그램에 든 onnxruntime.dll → (2) Windows 에 들어 있는 onnxruntime.dll (System32)
//
// (1)은 Visual C++ 런타임(MSVCP140.dll 등)이 있어야 열린다. 대부분의 PC 에 있지만 없으면 (2)로 넘어간다.
// (2)는 Windows 10(1809 이후)·11 의 일부라, 보안 정책(스마트 앱 컨트롤·AppLocker)이 사용자 폴더의 DLL 을 막아도 열린다.
// 버전이 PC 마다 다르지만 우리 모델은 옛 연산(opset 7·11)만 쓴다. 그래도 못 읽으면 Windows 글자 인식만 쓴다.
func ortCandidates() []ortCandidate {
	return []ortCandidate{
		{name: "내장 onnxruntime " + ortDLLVersion, open: openEmbeddedORT},
		{name: "Windows 기본 onnxruntime (System32)", open: openSystemORT},
	}
}

// ortDLLPath 는 내장 DLL 을 사용자 폴더(%LOCALAPPDATA%\DPR\ocr-<버전>)에 풀어 둔 경로를 돌려준다.
// 이미 있으면 내용이 같은지 확인하고, 다르면 다시 쓴다 (바꿔치기·깨진 파일 방지).
func ortDLLPath() (string, error) {
	if h := sha256.Sum256(ortDLL); hex.EncodeToString(h[:]) != ortDLLSHA256 {
		return "", errors.New("프로그램에 든 onnxruntime.dll 이 손상되었습니다")
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "DPR", "ocr-"+ortDLLVersion)
	p := filepath.Join(dir, "onnxruntime.dll")
	if b, err := os.ReadFile(p); err == nil && bytes.Equal(b, ortDLL) {
		return p, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "onnxruntime-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(ortDLL)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return "", errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		// 다른 프로세스가 같은 파일을 쓰고 있을 수 있다 — 내용이 같으면 그대로 쓴다
		if b, rerr := os.ReadFile(p); rerr == nil && bytes.Equal(b, ortDLL) {
			return p, nil
		}
		return "", err
	}
	return p, nil
}

func loadORT(path string, flags uintptr) (uintptr, error) {
	h, err := windows.LoadLibraryEx(path, 0, flags)
	if err != nil {
		if errors.Is(err, windows.ERROR_MOD_NOT_FOUND) {
			return 0, fmt.Errorf("%s 을 열지 못했습니다 — Visual C++ 런타임(MSVCP140.dll·VCRUNTIME140.dll)이 없는 PC 로 보입니다: %w", filepath.Base(path), err)
		}
		return 0, fmt.Errorf("%s 을 열지 못했습니다 (보안 프로그램·정책이 막았을 수 있습니다): %w", path, err)
	}
	sym, err := windows.GetProcAddress(h, "OrtGetApiBase")
	if err != nil {
		return 0, fmt.Errorf("%s 에 OrtGetApiBase 가 없습니다: %w", path, err)
	}
	return sym, nil
}

func openEmbeddedORT() (uintptr, error) {
	p, err := ortDLLPath()
	if err != nil {
		return 0, fmt.Errorf("onnxruntime.dll 을 풀지 못했습니다: %w", err)
	}
	// 전체 경로로 열고, 딸린 DLL(Visual C++ 런타임)은 그 폴더와 System32 에서만 찾는다 (PATH·현재 폴더는 보지 않는다)
	return loadORT(p, windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR|windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
}

func openSystemORT() (uintptr, error) {
	sys32, _ := windowsDirs()
	p := filepath.Join(sys32, "onnxruntime.dll")
	if !isFile(p) {
		return 0, fmt.Errorf("%s 이 없습니다", p)
	}
	return loadORT(p, windows.LOAD_LIBRARY_SEARCH_SYSTEM32)
}

func paddleWorkerCmd(cmd *exec.Cmd) *exec.Cmd { return hidden(cmd) }
