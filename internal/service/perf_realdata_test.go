package service

// 실데이터 폴더(환경변수 DPR_REALDATA)가 있을 때만 실행. 가져오기 단계별 시간만 기록하고 기대값은 코드에 두지 않는다.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"dpr/internal/ingest"
	"dpr/internal/sheet"
)

func TestRealData_ImportPhases(t *testing.T) {
	dir := os.Getenv("DPR_REALDATA")
	if dir == "" {
		t.Skip("DPR_REALDATA 미설정")
	}
	for _, f := range []struct{ kind, file string }{{"purchase", "purchase.xlsx"}, {"dispense", "dispense.xlsx"}} {
		path := filepath.Join(dir, f.file)
		t0 := time.Now()
		data, _ := os.ReadFile(path)
		sheets, err := sheet.ReadBytes(f.file, data)
		if err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()
		schema, _ := ingest.SchemaByKind(f.kind)
		tb := ingest.DetectTable(sheets[0], schema, nil)
		t2 := time.Now()
		var n int
		if f.kind == "purchase" {
			r, _ := ingest.ParsePurchases(tb)
			n = len(r)
		} else {
			r, _ := ingest.ParseDispenses(tb)
			n = len(r)
		}
		t3 := time.Now()
		s, _ := NewTemp(t.TempDir())
		res := mustImport(t, s, ImportRequest{Kind: f.kind, Path: path})
		t4 := time.Now()
		s.Close()
		t.Logf("%s %d행: 엑셀 읽기 %v · 표 인식 %v · 행 해석 %v · 가져오기 전체 %v (DB 포함, 앱 측정 %dms)",
			f.kind, n, t1.Sub(t0).Round(time.Millisecond), t2.Sub(t1).Round(time.Millisecond), t3.Sub(t2).Round(time.Millisecond), t4.Sub(t3).Round(time.Millisecond), res.DurationMs)
	}
}
