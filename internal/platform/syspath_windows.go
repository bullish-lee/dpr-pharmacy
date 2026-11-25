//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Windows 기본 도구(PowerShell·schtasks·explorer)를 PATH 가 아니라 실제 위치로 찾는다.
//
// 약국 PC 에서 PATH 환경 변수가 망가져(다른 프로그램 설치가 덮어씀 등) 'powershell.exe' 를 이름만으로 못 찾은 일이 있었다
// (2026-11-25: exec: "powershell.exe": executable file not found in %PATH%). Windows 폴더 위치는 API 로 직접 묻는다.

// windowsDirs : (System32, Windows) 폴더 — API → 환경 변수 → 기본값 순
func windowsDirs() (system32, windir string) {
	if d, err := windows.GetSystemDirectory(); err == nil && d != "" {
		system32 = d
	}
	if d, err := windows.GetWindowsDirectory(); err == nil && d != "" {
		windir = d
	}
	if windir == "" {
		for _, k := range []string{"SystemRoot", "windir"} {
			if v := os.Getenv(k); v != "" {
				windir = v
				break
			}
		}
	}
	if windir == "" {
		windir = `C:\Windows`
	}
	if system32 == "" {
		system32 = filepath.Join(windir, "System32")
	}
	return system32, windir
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// systemTool : System32 아래 도구의 전체 경로 (없으면 이름 그대로 — PATH 에 맡김)
func systemTool(name string) string {
	sys32, _ := windowsDirs()
	if p := filepath.Join(sys32, name); isFile(p) {
		return p
	}
	return name
}

// explorerPath : Windows 폴더의 explorer.exe
func explorerPath() string {
	_, windir := windowsDirs()
	if p := filepath.Join(windir, "explorer.exe"); isFile(p) {
		return p
	}
	return "explorer.exe"
}

// ErrNoPowerShell : 이 PC 에서 Windows PowerShell 을 찾지 못함
var ErrNoPowerShell = errors.New("이 PC 에서 Windows PowerShell 을 찾지 못했습니다")

// PowerShellPath 는 Windows PowerShell 5.1 의 전체 경로를 찾는다.
// 글자 인식(Windows.Media.Ocr)은 Windows PowerShell 5.1 에서만 된다 (PowerShell 7 'pwsh' 는 WinRT 형식을 못 읽는다).
func PowerShellPath() (string, error) {
	sys32, windir := windowsDirs()
	var tried []string
	for _, p := range []string{
		filepath.Join(sys32, `WindowsPowerShell\v1.0\powershell.exe`),
		filepath.Join(windir, `System32\WindowsPowerShell\v1.0\powershell.exe`),
		filepath.Join(windir, `Sysnative\WindowsPowerShell\v1.0\powershell.exe`), // 32비트로 실행될 때
	} {
		if isFile(p) {
			return p, nil
		}
		tried = append(tried, p)
	}
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("%w (찾아본 곳: %s, PATH)", ErrNoPowerShell, strings.Join(tried, ", "))
}

// PowerShellCommand 는 창 없이 도는 Windows PowerShell 명령을 만든다 (PATH 와 상관없이).
// 다른 기능(서명 확인 등)도 PowerShell 을 부를 때 이것을 쓴다.
func PowerShellCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	p, err := PowerShellPath()
	if err != nil {
		return nil, err
	}
	return hidden(exec.CommandContext(ctx, p, args...)), nil
}
