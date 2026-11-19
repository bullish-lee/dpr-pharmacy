package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 합성 데이터: 표준코드 새 판 확인 → 바뀐 날에만 내려받기 → 매핑 반영
func TestCheckStdCode(t *testing.T) {
	downloads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs":
			_, _ = w.Write([]byte(`{"paths":{"/15067462/v1/uddi:aaaa":{"get":{"summary":"건강보험심사평가원_약가마스터_의약품표준코드_20260831"}}}}`))
		case "/tcs/dss/selectFileDataDownload.do":
			_, _ = w.Write([]byte(`{"fileDataRegistVO":{"atchFileId":"FILE_1","fileDetailSn":1,"orginlFileNm":"std.csv"}}`))
		case "/cmm/cmm/check-limit.json":
			_, _ = w.Write([]byte(`{"needCaptcha":false}`))
		case "/cmm/cmm/fileDownload.do":
			downloads++
			w.Header().Set("Content-Disposition", `attachment; filename="std.csv"`)
			_, _ = w.Write([]byte("한글상품명,표준코드,제품코드(개정후),제품총수량\n가나정,8806000000018,600000010,30\n다라정,8806000000025,600000020,100\n" + strings.Repeat("\n", 1200)))
		}
	}))
	defer srv.Close()
	stdDocsURL, stdBaseURL = srv.URL+"/docs", srv.URL
	defer func() { stdDocsURL, stdBaseURL = "", "" }()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	res, err := s.CheckStdCode(ctx, false)
	if err != nil || !res.Updated || res.Rows != 2 || downloads != 1 {
		t.Fatalf("첫 확인: %+v %v (내려받기 %d)", res, err, downloads)
	}
	if res, _ := s.CheckStdCode(ctx, false); !res.Skipped {
		t.Fatalf("20시간 안에는 건너뛰어야 함: %+v", res)
	}
	if res, _ := s.CheckStdCode(ctx, true); res.Updated || downloads != 1 {
		t.Fatalf("같은 판이면 다시 받지 않아야 함: %+v (내려받기 %d)", res, downloads)
	}
	var n int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM gtin_map WHERE source = 'official'").Scan(&n)
	if n != 2 || s.GetSettings().StdDate != "2026-08-31" {
		t.Fatalf("매핑 %d건, 판 %q", n, s.GetSettings().StdDate)
	}
}
