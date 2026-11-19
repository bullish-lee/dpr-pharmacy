package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const docsSample = `{"swagger":"2.0","paths":{
 "/15067462/v1/uddi:8cae86af-2ac7-4dde-a515-a7511994f74b":{"get":{"summary":"건강보험심사평가원_약가마스터_의약품표준코드_10/01/2021"}},
 "/15067462/v1/uddi:456729a5-28ed-494d-b5a8-ba5000eb6bab":{"get":{"summary":"건강보험심사평가원_약가마스터_의약품표준코드_20251031"}},
 "/15067462/v1/uddi:8116acc9-2f07-4a1f-bec0-ddc8047bec3e":{"get":{"summary":"건강보험심사평가원_약가마스터_의약품표준코드_20260831"}}}}`

func TestParseStdDocs(t *testing.T) {
	s, err := ParseStdDocs([]byte(docsSample))
	if err != nil || s.UDDI != "uddi:8116acc9-2f07-4a1f-bec0-ddc8047bec3e" || s.Date != "2026-08-31" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestDownloadStdCode(t *testing.T) {
	csv := "한글상품명,표준코드,제품코드(개정후),제품총수량\n" + strings.Repeat("가나정,8806000000001,600000010,30\n", 100)
	captcha := false
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/docs":
			_, _ = w.Write([]byte(docsSample))
		case "/tcs/dss/selectFileDataDownload.do":
			_ = r.ParseForm()
			if r.Form.Get("publicDataDetailPk") != "uddi:8116acc9-2f07-4a1f-bec0-ddc8047bec3e" {
				http.Error(w, "bad", 400)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "x", Path: "/"})
			_, _ = w.Write([]byte(`{"fileDataRegistVO":{"atchFileId":"FILE_1","fileDetailSn":1,"orginlFileNm":"약가마스터_2026.08.csv"}}`))
		case "/cmm/cmm/check-limit.json":
			if c, err := r.Cookie("SESSION"); err != nil || c.Value != "x" {
				http.Error(w, "no session", 403)
				return
			}
			if captcha {
				_, _ = w.Write([]byte(`{"needCaptcha":true}`))
				return
			}
			_, _ = w.Write([]byte(`{"needCaptcha":false}`))
		case "/cmm/cmm/fileDownload.do":
			w.Header().Set("Content-Disposition", `attachment; filename="약가마스터_2026.08.csv"`)
			_, _ = w.Write([]byte(csv))
		}
	}))
	defer srv.Close()
	c := New()
	c.Pause = time.Millisecond
	snap, err := c.LatestStdSnapshot(context.Background(), srv.URL+"/docs")
	if err != nil || snap.Date != "2026-08-31" {
		t.Fatalf("snapshot %+v %v", snap, err)
	}
	name, data, err := c.DownloadStdCode(context.Background(), srv.URL, snap)
	if err != nil || name != "약가마스터_2026.08.csv" || string(data) != csv {
		t.Fatalf("download %q %d %v", name, len(data), err)
	}
	captcha = true
	if _, _, err := c.DownloadStdCode(context.Background(), srv.URL, snap); err != ErrNeedCaptcha {
		t.Fatalf("보안문자면 멈춰야 함: %v", err)
	}
	if n := strings.Count(strings.Join(hits, ","), "fileDownload"); n != 1 {
		t.Fatalf("보안문자 뒤에는 내려받지 않아야 함: %v", hits)
	}
}
