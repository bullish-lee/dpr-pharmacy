package service

// 실데이터 DB(환경변수 DPR_SCREEN_DB)가 있을 때만 실행. 화면별 응답 시간만 기록하고 기대값은 코드에 두지 않는다.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRealData_ScreenLatency(t *testing.T) {
	src := os.Getenv("DPR_SCREEN_DB")
	if src == "" {
		t.Skip("DPR_SCREEN_DB 미설정")
	}
	dir := t.TempDir()
	data, _ := os.ReadFile(src)
	_ = os.WriteFile(filepath.Join(dir, "dpr.db"), data, 0o644)
	s, err := NewTemp(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	eff := "2026-11-01"
	code := os.Getenv("DPR_SCREEN_CODE")
	measure := func(name string, fn func() error) {
		// 처음(데이터가 바뀐 직후) 과 다시 열 때를 따로 잰다
		_ = s.DB.SetSetting("perf_probe", time.Now().String()) // 다른 연결의 커밋 → 캐시 무효
		t0 := time.Now()
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cold := time.Since(t0)
		t1 := time.Now()
		_ = fn()
		t.Logf("%-16s 처음 %-10v 다시 %v", name, cold.Round(100*time.Microsecond), time.Since(t1).Round(100*time.Microsecond))
	}
	measure("대시보드", func() error { _, err := s.Dashboard(); return err })
	measure("시행일 목록", func() error { _, err := s.PriceCutDates(); return err })
	measure("손실 순위", func() error { _, err := s.Ranking(eff, true); return err })
	measure("손실 순위(전체)", func() error { _, err := s.Ranking(eff, false); return err })
	measure("재고", func() error { _, err := s.Inventory("", false); return err })
	measure("보유 삭제", func() error { _, err := s.HeldDeletions(eff); return err })
	measure("고시 목록", func() error { _, err := s.ListNotices(); return err })
	if code != "" {
		measure("품목 판정", func() error { _, err := s.ProductDetail(eff, code); return err })
	}
	if gtin := os.Getenv("DPR_SCREEN_GTIN"); gtin != "" && code != "" { // 바코드 스캔 1건 (일련번호를 바꿔 가며)
		var total time.Duration
		const n = 50
		for i := 0; i < n; i++ {
			raw := "(01)" + gtin + "(17)281231(10)LOT1(21)PERF" + time.Now().Format("150405.000000") + string(rune('A'+i%26))
			t0 := time.Now()
			if _, err := s.RecordScan(ScanInput{EffectiveDate: eff, ProductCode: code, Raw: raw}); err != nil {
				t.Fatal(err)
			}
			total += time.Since(t0)
		}
		t.Logf("%-16s 평균 %v", "바코드 스캔 1건", (total / n).Round(10*time.Microsecond))
	}
}
