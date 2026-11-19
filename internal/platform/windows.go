//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
)

const taskFolder = `\DPR\`

func hidden(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd
}

// InstallCrawlTasks 는 Windows 작업 스케줄러에 'exe crawl' 을 매일 지정 시각에 등록한다 (창이 닫혀 있어도 수집).
func InstallCrawlTasks(exe string, times []string) (string, error) {
	_ = RemoveCrawlTasks()
	var made []string
	for _, t := range times {
		name := taskFolder + "crawl_" + strings.ReplaceAll(t, ":", "") // ASCII 이름: schtasks 출력이 CP949여도 해제 가능
		cmd := hidden(exec.Command("schtasks", "/Create", "/F", "/TN", name, "/TR", fmt.Sprintf(`"%s" crawl`, exe), "/SC", "DAILY", "/ST", t))
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("작업 등록 실패(%s): %v %s", t, err, string(out))
		}
		made = append(made, t)
	}
	return "Windows 작업 스케줄러에 등록했습니다: " + strings.Join(made, ", "), nil
}

func RemoveCrawlTasks() error {
	out, err := hidden(exec.Command("schtasks", "/Query", "/FO", "CSV", "/NH")).Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.SplitN(line, ",", 2)
		if len(f) == 0 {
			continue
		}
		name := strings.Trim(f[0], "\" \r")
		if strings.HasPrefix(name, taskFolder) {
			_ = hidden(exec.Command("schtasks", "/Delete", "/F", "/TN", name)).Run()
		}
	}
	return nil
}

func Toast(title, body string) error {
	n := toast.Notification{AppID: "급여약품 관리", Title: title, Body: body}
	return n.Push()
}

func OpenFolder(path string) error { return hidden(exec.Command("explorer", path)).Start() }

var (
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	user32        = syscall.NewLazyDLL("user32.dll")
	attachConsole = kernel32.NewProc("AttachConsole")
	messageBoxW   = user32.NewProc("MessageBoxW")
)

// AttachConsole 은 명령 프롬프트에서 실행했을 때 출력이 보이도록 부모 콘솔에 붙는다 (GUI 서브시스템 exe).
func AttachConsole() {
	const attachParentProcess = ^uintptr(0)
	if r, _, _ := attachConsole.Call(attachParentProcess); r == 0 {
		return
	}
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}

// Fatal 은 앱 시작 전 치명적 오류를 메시지 상자로 알린다.
func Fatal(title, msg string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(msg)
	_, _, _ = messageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
}

// ProcessAlive : 그 PID 의 프로세스가 아직 살아 있는가 (OpenProcess + GetExitCodeProcess)
func ProcessAlive(pid int) bool {
	const stillActive = 259
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

var procGetDriveType = kernel32.NewProc("GetDriveTypeW")

// IsNetworkPath : 네트워크 폴더(\\server\share 또는 네트워크 드라이브로 연결한 Z: 등)인가.
// SQLite 의 WAL 기록은 네트워크 파일 시스템에서 안전하지 않다 — 데이터 폴더로 쓰지 않는다 (백업 폴더는 괜찮음).
func IsNetworkPath(p string) bool {
	if strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//") {
		return true
	}
	if len(p) < 2 || p[1] != ':' {
		return false
	}
	root, err := syscall.UTF16PtrFromString(p[:2] + `\`)
	if err != nil {
		return false
	}
	r, _, _ := procGetDriveType.Call(uintptr(unsafe.Pointer(root)))
	return r == 4 // DRIVE_REMOTE
}
