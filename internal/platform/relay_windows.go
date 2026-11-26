//go:build windows

package platform

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CloudflaredDownloadURL : 클라우드플레어 무료 중계 프로그램(cloudflared) 공식 배포 주소 (GitHub 릴리스)
const CloudflaredDownloadURL = "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe"

// CloudflaredExe : 받아 둘 파일 이름
const CloudflaredExe = "cloudflared.exe"

// HideWindow : 검은 명령 창이 뜨지 않게 실행한다
func HideWindow(cmd *exec.Cmd) *exec.Cmd { return hidden(cmd) }

var (
	crypt32              = windows.NewLazySystemDLL("crypt32.dll")
	procCryptMsgGetParam = crypt32.NewProc("CryptMsgGetParam")
	procCryptMsgClose    = crypt32.NewProc("CryptMsgClose")
)

const cmsgSignerCertInfoParam = 7 // CMSG_SIGNER_CERT_INFO_PARAM

// VerifySigner 는 실행 파일에 Windows 가 확인한 유효한 전자서명이 있고, 서명한 회사 이름이 정확히 want 인지 본다.
// (이름 일부만 맞추면 'Cloudflare 무엇무엇' 같은 다른 회사 인증서도 통과할 수 있어 전체 이름을 맞춘다)
// PowerShell 을 부르지 않고 Windows 서명 확인 기능(WinVerifyTrust)을 직접 쓴다 — PATH 가 망가졌거나
// PowerShell 이 막힌 PC 에서도 동작한다.
func VerifySigner(path, want string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("전자서명을 확인하지 못했습니다: %w", err)
	}
	// 1) 서명이 유효한지 (위조·변조·만료·신뢰할 수 없는 발급자면 실패)
	file := windows.WinTrustFileInfo{FilePath: p}
	file.Size = uint32(unsafe.Sizeof(file))
	data := windows.WinTrustData{
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&file),
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
		ProvFlags:                       windows.WTD_CACHE_ONLY_URL_RETRIEVAL,
	}
	data.Size = uint32(unsafe.Sizeof(data))
	verr := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	_ = windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, &data)
	if verr != nil {
		return fmt.Errorf("전자서명이 올바르지 않습니다 (%v)", verr)
	}
	// 2) 서명한 회사 이름
	subject, err := signerName(p)
	if err != nil {
		return fmt.Errorf("전자서명을 확인하지 못했습니다: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(subject), want) {
		return fmt.Errorf("서명한 회사가 다릅니다: %s", subject)
	}
	return nil
}

// SignerName : 파일에 들어 있는 전자서명에서 서명한 회사 이름을 읽는다 (서명이 유효한지는 보지 않음 — VerifySigner 가 본다)
func SignerName(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	return signerName(p)
}

func signerName(p *uint16) (string, error) {
	var enc, ctype, ftype uint32
	var store, msg windows.Handle
	if err := windows.CryptQueryObject(windows.CERT_QUERY_OBJECT_FILE, unsafe.Pointer(p),
		windows.CERT_QUERY_CONTENT_FLAG_PKCS7_SIGNED_EMBED, windows.CERT_QUERY_FORMAT_FLAG_BINARY, 0,
		&enc, &ctype, &ftype, &store, &msg, nil); err != nil {
		return "", err
	}
	defer windows.CertCloseStore(store, 0)
	defer procCryptMsgClose.Call(uintptr(msg))
	var size uint32
	if r, _, e := procCryptMsgGetParam.Call(uintptr(msg), cmsgSignerCertInfoParam, 0, 0, uintptr(unsafe.Pointer(&size))); r == 0 || size == 0 {
		return "", fmt.Errorf("서명 정보 없음: %v", e)
	}
	buf := make([]byte, size)
	if r, _, e := procCryptMsgGetParam.Call(uintptr(msg), cmsgSignerCertInfoParam, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r == 0 {
		return "", fmt.Errorf("서명 정보 읽기 실패: %v", e)
	}
	cert, err := windows.CertFindCertificateInStore(store, windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING, 0,
		windows.CERT_FIND_SUBJECT_CERT, unsafe.Pointer(&buf[0]), nil)
	if err != nil {
		return "", err
	}
	defer windows.CertFreeCertificateContext(cert)
	n := windows.CertGetNameString(cert, windows.CERT_NAME_SIMPLE_DISPLAY_TYPE, 0, nil, nil, 0)
	if n <= 1 {
		return "", fmt.Errorf("서명한 회사 이름 없음")
	}
	name := make([]uint16, n)
	windows.CertGetNameString(cert, windows.CERT_NAME_SIMPLE_DISPLAY_TYPE, 0, nil, &name[0], n)
	return windows.UTF16ToString(name), nil
}

// KillIfNamed : pid 가 아직 살아 있고 그 프로세스 이름이 name 이면 끝낸다 (지난번 비정상 종료 때 남은 중계 프로그램 정리용)
func KillIfNamed(pid int, name string) {
	if pid <= 0 || !ProcessAlive(pid) {
		return
	}
	out, err := hidden(exec.Command(systemTool("tasklist.exe"), "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH")).Output()
	if err != nil || !strings.Contains(strings.ToLower(string(out)), strings.ToLower(name)) {
		return
	}
	_ = hidden(exec.Command(systemTool("taskkill.exe"), "/PID", strconv.Itoa(pid), "/F")).Run()
}
