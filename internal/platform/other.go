//go:build !windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

var errWindowsOnly = errors.New("Windows 에서만 사용할 수 있는 기능입니다 (macOS 개발 환경에서는 앱을 켜 두면 앱 내 스케줄러가 수집합니다)")

func InstallCrawlTasks(exe string, times []string) (string, error) { return "", errWindowsOnly }
func RemoveCrawlTasks() error                                      { return errWindowsOnly }

// Toast 는 macOS 에서는 osascript 알림으로 대체한다.
func Toast(title, body string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("osascript", "-e", `display notification "`+escape(body)+`" with title "`+escape(title)+`"`).Run()
	}
	return nil
}

func escape(s string) string {
	out := []rune{}
	for _, r := range s {
		if r == '"' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

func OpenFolder(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	return exec.Command("xdg-open", path).Start()
}

func AttachConsole() {}

func Fatal(title, msg string) { fmt.Fprintln(os.Stderr, title+":", msg) }

// ProcessAlive : 그 PID 의 프로세스가 아직 살아 있는가
func ProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// IsNetworkPath : 네트워크 폴더인가 (macOS 개발 환경에서는 // 로 시작하는 경로만 본다)
func IsNetworkPath(p string) bool { return len(p) >= 2 && p[0] == '/' && p[1] == '/' }
