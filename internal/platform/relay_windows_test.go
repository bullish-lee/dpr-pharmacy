//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 서명 확인: Microsoft 가 서명한 Windows 파일은 통과, 서명 없는 파일·다른 회사 이름은 거부
func TestVerifySigner(t *testing.T) {
	// 약국 PC 처럼 PATH(프로그램을 찾아보는 폴더 목록)가 비어 있어도 동작해야 한다
	t.Setenv("PATH", "")
	// cloudflared 처럼 파일 안에 서명이 들어 있는(embedded) Microsoft 파일 (PC 마다 있는 것이 달라 후보 중 있는 것)
	root := os.Getenv("SystemRoot")
	var sys string
	for _, c := range []string{
		filepath.Join(root, "System32", "MRT.exe"),
		filepath.Join(root, "System32", "MpSigStub.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
	} {
		if _, err := os.Stat(c); err == nil {
			sys = c
			break
		}
	}
	if sys != "" {
		name, err := SignerName(sys)
		if err != nil || !strings.HasPrefix(name, "Microsoft") {
			t.Fatalf("Microsoft 서명 파일 %s: 서명 회사 %q, %v", sys, name, err)
		}
		if err := VerifySigner(sys, name); err != nil {
			t.Fatalf("Microsoft 서명 파일 %s: %v", sys, err)
		}
		if err := VerifySigner(sys, CloudflareSigner); err == nil {
			t.Fatal("다른 회사 이름인데 통과함")
		}
		if err := VerifySigner(sys, "Microsoft"); err == nil {
			t.Fatal("회사 이름 일부만 맞는데 통과함")
		}
	}
	// 실제 cloudflared 파일로 확인하고 싶을 때: DPR_TEST_CLOUDFLARED=경로
	if cf := os.Getenv("DPR_TEST_CLOUDFLARED"); cf != "" {
		if err := VerifySigner(cf, CloudflareSigner); err != nil {
			t.Fatalf("cloudflared: %v", err)
		}
	}
	fake := filepath.Join(t.TempDir(), "it's fake.exe") // 작은따옴표가 든 경로도
	if err := os.WriteFile(fake, []byte("MZ not really a program"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifySigner(fake, CloudflareSigner); err == nil {
		t.Fatal("서명 없는 파일이 통과함")
	}
}
