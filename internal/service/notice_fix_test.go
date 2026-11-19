package service

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dpr/internal/crawler"
)

// 정정 고시: 같은 품목·시행일에 나중 고시가 '변경 없음'이면 앞선 인하는 취소된다.
// 제품명 변경 같은 가격 아닌 항목은 앞선 인하를 취소하지 않는다. 순위·상세·시행일 목록이 같은 기준을 쓴다.
func TestCorrectionNoticeAndUnknownAfter(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ex := func(q string, a ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, a...); err != nil {
			t.Fatal(q, err)
		}
	}
	ex("INSERT INTO product(code, name, updated_at) VALUES('600000010','가나정',''), ('600000020','다라정',''), ('600000030','마바정','')")
	ex(`INSERT INTO stock_anchor(product_code, as_of, timing, qty, source, created_at) VALUES
		('600000010','2026-11-01','end',100000,'count',''), ('600000020','2026-11-01','end',100000,'count',''), ('600000030','2026-11-01','end',100000,'count','')`)
	// 고시 1(먼저 게시): 세 품목 인하. 고시 2(나중 게시, 먼저 저장): 600000010 변경 없음(정정), 600000030 제품명 변경
	ex(`INSERT INTO notice(id, source, notice_no, published_at, status, created_at, updated_at) VALUES
		(1,'upload','2026-2 정정','2026-11-15','parsed','',''), (2,'upload','2026-1','2026-11-10','parsed','','')`)
	ex(`INSERT INTO price_change(notice_id, product_code, kind, detail, price_before, price_after, effective_date) VALUES
		(2,'600000010','change','price_down',1000,800,'2026-12-01'),
		(2,'600000020','change','price_down',1000,0,'2026-12-01'),
		(2,'600000030','change','price_down',1000,900,'2026-12-01'),
		(1,'600000010','change','same',1000,1000,'2026-12-01'),
		(1,'600000030','change','name_change',0,0,'2026-12-01')`)
	rk, err := s.Ranking("2026-12-01", true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]RankRow{}
	for _, r := range rk.Rows {
		got[r.Code] = r
	}
	if _, ok := got["600000010"]; ok {
		t.Fatalf("정정 고시(변경 없음)가 인하를 취소해야 함: %+v", got["600000010"])
	}
	if r := got["600000030"]; r.Detail != "price_down" || r.GrossLoss != 10000 {
		t.Fatalf("제품명 변경은 인하를 취소하지 않음: %+v", r)
	}
	if r := got["600000020"]; r.GrossLoss != 0 || r.Recommendation != "check_price" {
		t.Fatalf("변경 후 가격 0(빈칸)은 전액 손실이 아니라 확인 필요: gross=%d rec=%s", r.GrossLoss, r.Recommendation)
	}
	for _, code := range []string{"600000010", "600000020", "600000030"} {
		pd, err := s.ProductDetail("2026-12-01", code)
		if err != nil {
			t.Fatal(err)
		}
		if r, ok := got[code]; ok && (pd.Row.Detail != r.Detail || pd.Row.GrossLoss != r.GrossLoss) {
			t.Fatalf("%s: 순위와 상세가 다름 — 순위 %s/%d, 상세 %s/%d", code, r.Detail, r.GrossLoss, pd.Row.Detail, pd.Row.GrossLoss)
		}
		if code == "600000010" && pd.Row.Detail != "same" {
			t.Fatalf("상세도 정정 고시 기준: %s", pd.Row.Detail)
		}
	}
	dates, err := s.PriceCutDates()
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != 1 || dates[0].Down != 2 {
		t.Fatalf("시행일 목록의 인하 수도 최신 고시 기준 (2개): %+v", dates)
	}
}

// 직접 올린 고시 파일을 크롤러가 같은 게시글에서 받으면: 새 고시로 알리지 않고, 게시글을 연결해 다음 수집 때 다시 받지 않는다.
func TestCrawlLinksManuallyUploadedNotice(t *testing.T) {
	hdr := []any{"주성분코드", "제품코드", "제품명", "업체명", "규격", "단위", "상한금액", "퇴장방지", "", "투여", "비고"}
	b64 := xlsxB64(t, map[string][][]any{
		"별지3_변경('26.12.01.시행)": {{"별지3"}, hdr, {"", "987654320", "가나정", "가나제약", "1", "정", 900, "", "", "내복", ""}},
		"변경대비표": {{"변경대비표"}, {},
			{"연번", "투여", "분류", "제품코드", "제품명", "제약사명", "규격", "단위", "상한금액", "변경 전", "변경 후", "비  고", "전문/일반", "시행일"},
			{1, "내복", 229, "987654320", "가나정", "가나제약", "1", "정", 900, 1000, 900, "상한금액", "전문", "2026-12-01"}},
	})
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	var downloads atomic.Int32
	rss := `<?xml version="1.0" encoding="UTF-8"?><rss><channel>
<item><title>「약제 급여 목록 및 급여 상한금액표」 일부개정</title><link>LINK/view?list_no=900002&amp;act=view</link><pubDate>Tue, 17 Nov 2026 09:00:00 +0900</pubDate></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/rss"):
			_, _ = w.Write([]byte(strings.ReplaceAll(rss, "LINK", "http://"+r.Host)))
		case strings.HasPrefix(r.URL.Path, "/view"):
			_, _ = w.Write([]byte(`<html><body><a href="/boardDownload.es?bid=0026&list_no=900002&seq=1" title="일부개정고시(2026-301호)_별표1 목록표.xlsx">목록표</a></body></html>`))
		case strings.HasPrefix(r.URL.Path, "/boardDownload"):
			downloads.Add(1)
			w.Header().Set("Content-Disposition", `attachment; filename="notice.xlsx"`)
			_, _ = w.Write(data)
		}
	}))
	defer srv.Close()
	old := NewCrawler
	NewCrawler = func() *crawler.Client {
		c := crawler.New()
		c.RSSURL, c.Base, c.Backoff, c.Pause, c.Retries = srv.URL+"/rss", srv.URL, time.Millisecond, time.Millisecond, 1
		return c
	}
	defer func() { NewCrawler = old }()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// 1) 크롤러가 게시글만 감지하고 실패해 둔 상태 (자리 표시 행) → 사용자가 파일을 직접 올림
	s.markDetected(crawler.Item{ListNo: "900002", Title: "일부개정", Link: srv.URL + "/view?list_no=900002"}, "2026-11-17")
	s.markFailed("900002", "첨부 다운로드 실패: 시험")
	_, _ = s.DB.Exec("UPDATE notice SET next_try = '' WHERE list_no = '900002'") // 다시 시도할 시각이 됨
	mustImport(t, s, ImportRequest{Kind: "notice", FileName: "일부개정고시(2026-301호)_별표1 목록표.xlsx", DataBase64: b64})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		res, err := s.RunCrawl(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if res.New != 0 {
			t.Fatalf("%d번째 수집: 이미 올린 고시를 새 고시로 셈 (%d)", i+1, res.New)
		}
	}
	if n := downloads.Load(); n != 1 {
		t.Fatalf("같은 게시글을 한 번만 받아야 함: %d회", n)
	}
	var notes, rows int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '새 약가 고시가 수집되었습니다'").Scan(&notes)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notice").Scan(&rows)
	if notes != 0 || rows != 1 {
		t.Fatalf("알림 %d건(0이어야), 고시 행 %d개(1이어야)", notes, rows)
	}
	var listNo, published string
	_ = s.DB.QueryRow("SELECT IFNULL(list_no, ''), published_at FROM notice").Scan(&listNo, &published)
	if listNo != "900002" || published != "2026-11-17" {
		t.Fatalf("올린 고시에 게시글 번호·게시일 연결: list_no=%q published=%q", listNo, published)
	}
	list, _ := s.ListNotices()
	if len(list) != 1 {
		t.Fatalf("고시 목록: %d건", len(list))
	}
	// 지우면 수정 기록에 남는다
	if err := s.DeleteNotice(list[0].ID); err != nil {
		t.Fatal(err)
	}
	log, _ := s.ChangeLog(ChangeFilter{Kind: "notice_delete"})
	if len(log) != 1 {
		t.Fatalf("고시 삭제 기록: %+v", log)
	}
}

// 직접 올린 고시는 게시일 대신 '올린 날'이 들어간다 — 고시 번호가 모두 있으면 번호(공식 일련번호)로 최신을 정해,
// 나중에 수집한 정정 고시(번호가 더 큼)가 앞서 올린 원래 고시를 이긴다
func TestUploadedNoticeDoesNotOverrideLaterCorrection(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ex := func(q string, a ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, a...); err != nil {
			t.Fatal(q, err)
		}
	}
	ex("INSERT INTO product(code, name, updated_at) VALUES('600000010','가나정','')")
	ex("INSERT INTO stock_anchor(product_code, as_of, timing, qty, source, created_at) VALUES('600000010','2026-11-01','end',100000,'count','')")
	ex(`INSERT INTO notice(id, source, notice_no, published_at, status, created_at, updated_at) VALUES
		(1,'upload','2026-219','2026-11-17','parsed','',''), (2,'mohw','2026-230','2026-11-15','parsed','','')`)
	ex(`INSERT INTO price_change(notice_id, product_code, kind, detail, price_before, price_after, effective_date) VALUES
		(1,'600000010','change','price_down',1000,800,'2026-12-01'),
		(2,'600000010','change','same',1000,1000,'2026-12-01')`)
	rk, err := s.Ranking("2026-12-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rk.Rows) != 0 {
		t.Fatalf("정정 고시(2026-230)가 원래 고시(2026-219)의 인하를 취소해야 함: %+v", rk.Rows[0])
	}
	if pd, err := s.ProductDetail("2026-12-01", "600000010"); err != nil || pd.Row.Detail != "same" {
		t.Fatalf("상세도 정정 고시 기준: %+v %v", pd, err)
	}
	if d, _ := s.PriceCutDates(); len(d) != 1 || d[0].Down != 0 {
		t.Fatalf("시행일 목록 인하 수: %+v", d)
	}
}

// 그 약이 나온 고시만 지우면 고시에서 온 단위 표시·상태도 비운다 (옛 값이 남지 않게)
func TestDeleteNoticeClearsStaleUnitInfo(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hdr := []any{"주성분코드", "제품코드", "제품명", "업체명", "규격", "단위", "상한금액", "퇴장방지", "", "투여", "비고"}
	res := mustImport(t, s, ImportRequest{Kind: "notice", FileName: "일부개정고시(2026-301호)_별표1 목록표.xlsx", DataBase64: xlsxB64(t, map[string][][]any{
		"별지3_변경('26.12.01.시행)": {{"별지3"}, hdr, {"", "987654320", "가나팩정", "가나제약", "28", "정/팩", 6000, "", "", "내복", ""}},
	})})
	var unit string
	_ = s.DB.QueryRow("SELECT base_unit FROM product WHERE code = '987654320'").Scan(&unit)
	if unit != "팩" {
		t.Fatalf("준비: 고시 단위 %q", unit)
	}
	if err := s.DeleteNotice(res.NoticeID); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.QueryRow("SELECT base_unit FROM product WHERE code = '987654320'").Scan(&unit)
	if unit != "" {
		t.Fatalf("고시를 지운 뒤에도 옛 고시 단위가 남음: %q", unit)
	}
}
