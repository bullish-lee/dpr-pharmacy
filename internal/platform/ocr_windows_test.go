//go:build windows

package platform

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 약국 PC 처럼 PATH 에 PowerShell 이 없어도 찾는다 (2026-11-25: executable file not found in %PATH%)
func TestPowerShellPathWithoutPATH(t *testing.T) {
	t.Setenv("PATH", "")
	p, err := PowerShellPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(p[len(p)-len(`WindowsPowerShell\v1.0\powershell.exe`):], `WindowsPowerShell\v1.0\powershell.exe`) {
		t.Fatalf("Windows PowerShell 5.1 이 아님: %s", p)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	if s := systemTool("schtasks.exe"); !strings.Contains(strings.ToLower(s), `\system32\`) {
		t.Fatalf("schtasks: %s", s)
	}
	if e := explorerPath(); !strings.HasSuffix(strings.ToLower(e), `\explorer.exe`) || !strings.Contains(e, `\`) {
		t.Fatalf("explorer: %s", e)
	}
}

// 점검 그림(가상 사입현황 표)을 실제로 읽는다 — PATH 를 비운 채로.
// DPR_OCR_REQUIRE=1 이면 인식까지 반드시 되어야 하고, DPR_OCR_REQUIRE_KO=1 이면 한국어 인식까지 확인한다 (CI).
func TestOCRSelfTestOnWindows(t *testing.T) {
	t.Setenv("PATH", "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := OCRSelfTest(ctx)
	t.Logf("점검: ok=%v powershell=%s engine=%s korean=%v found=%d ms=%d problem=%q\n%s", c.OK, c.PowerShell, c.Engine, c.Korean, c.Found, c.Ms, c.Problem, c.Text)
	if c.PowerShell == "" {
		t.Fatal("PowerShell 을 찾지 못함")
	}
	if os.Getenv("DPR_OCR_REQUIRE") == "1" && (!c.OK || c.Found < 3) {
		t.Fatalf("글자 인식이 되어야 함: %+v", c)
	}
	if os.Getenv("DPR_OCR_REQUIRE_KO") == "1" {
		if !c.Korean || !strings.Contains(c.Text, "도매") {
			t.Fatalf("한국어 인식이 되어야 함: %+v", c)
		}
	}
}
