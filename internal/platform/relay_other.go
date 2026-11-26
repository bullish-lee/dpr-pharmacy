//go:build !windows

package platform

import (
	"os/exec"
	"runtime"
)

// 개발용(macOS·리눅스): 같은 이름의 공식 배포 파일
var CloudflaredDownloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-" + runtime.GOOS + "-" + runtime.GOARCH

const CloudflaredExe = "cloudflared"

func HideWindow(cmd *exec.Cmd) *exec.Cmd { return cmd }

// VerifySigner : Windows 전자서명 확인은 Windows 에서만 (개발 환경에서는 건너뜀)
func VerifySigner(path, want string) error { return nil }

func SignerName(path string) (string, error) { return "", nil }

func KillIfNamed(pid int, name string) {}
