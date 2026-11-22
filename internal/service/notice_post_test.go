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

// 지난 고시 하나 가져오기: 미래 시행일 시트('26.11.23. 시행 같은 가산 종료)가 들어오고, 다시 눌러도 요청하지 않는다
func TestImportNoticePost(t *testing.T) {
	hdr := []any{"주성분코드", "제품코드", "제품명", "업체명", "규격", "단위", "상한금액", "퇴장방지", "", "투여", "비고"}
	b64 := xlsxB64(t, map[string][][]any{
		"별지5_변경('26.11.23.시행)": {{"별지5"}, hdr, {"", "987654320", "가나정5밀리그램(가나성분)_(5mg/1정)", "가나제약(주)", "1", "정", 900, "", "", "내복", ""}},
		"변경대비표": {{"변경대비표"}, {},
			{"연번", "투여", "분류", "제품코드", "제품명", "제약사명", "규격", "단위", "상한금액", "변경 전", "변경 후", "비  고", "전문/일반", "시행일"},
			{1, "내복", 141, "987654320", "가나정5밀리그램(가나성분)_(5mg/1정)", "가나제약(주)", "1", "정", 900, 1000, 900, "상한금액", "전문", "2026-11-23"}},
	})
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	var views, downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/boardDownload"):
			downloads.Add(1)
			_, _ = w.Write(data)
		case r.URL.Path == "/board.es" && r.URL.Query().Get("list_no") == "1487997":
			views.Add(1)
			_, _ = w.Write([]byte(`<html><body><div><span>작성일</span> <span>2025-11-25</span></div>
				<a href="/boardDownload.es?bid=0026&list_no=1487997&seq=4" title="약제 급여 목록 및 급여 상한금액표 일부개정고시(2025-188호)_별표1 목록표.xlsx">목록표</a>
				<a href="/boardDownload.es?bid=0026&list_no=1487997&seq=1" title="일부개정고시(2025-188호).pdf">pdf</a></body></html>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	old := NewCrawler
	NewCrawler = func() *crawler.Client {
		c := crawler.New()
		c.Base, c.Backoff, c.Pause, c.Retries = srv.URL, time.Millisecond, time.Millisecond, 1
		return c
	}
	defer func() { NewCrawler = old }()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	res, err := s.ImportNoticePost(ctx, "1487997")
	if err != nil || res.Already || res.NoticeID == 0 {
		t.Fatalf("가져오기: %+v %v", res, err)
	}
	var title, pub, src string
	_ = s.DB.QueryRow("SELECT title, published_at, source FROM notice WHERE list_no = '1487997'").Scan(&title, &pub, &src)
	if title != "약제 급여 목록 및 급여 상한금액표 일부개정고시(2025-188호)" || pub != "2025-11-25" || src != "mohw" {
		t.Fatalf("고시 정보: %q %q %q", title, pub, src)
	}
	var detail, eff string
	var before, after float64
	if err := s.DB.QueryRow("SELECT detail, effective_date, IFNULL(price_before, 0), IFNULL(price_after, 0) FROM price_change WHERE product_code = '987654320'").Scan(&detail, &eff, &before, &after); err != nil {
		t.Fatal(err)
	}
	if detail != "price_down" || eff != "2026-11-23" || before != 1000 || after != 900 {
		t.Fatalf("인하: %s %s %v→%v", detail, eff, before, after)
	}
	if views.Load() != 1 || downloads.Load() != 1 {
		t.Fatalf("요청 수: 상세 %d, 첨부 %d", views.Load(), downloads.Load())
	}
	// 다시 누르면 요청하지 않는다
	if res, err := s.ImportNoticePost(ctx, srv.URL+"/board.es?mid=a10409020000&bid=0026&list_no=1487997&act=view"); err == nil || res != nil {
		// 테스트 서버 주소는 복지부 주소가 아니라 거부돼야 한다
		t.Fatalf("복지부가 아닌 주소: %+v %v", res, err)
	}
	if res, err := s.ImportNoticePost(ctx, "1487997"); err != nil || !res.Already {
		t.Fatalf("이미 가져온 고시: %+v %v", res, err)
	}
	if views.Load() != 1 || downloads.Load() != 1 {
		t.Fatalf("이미 가져온 고시를 다시 요청함: 상세 %d, 첨부 %d", views.Load(), downloads.Load())
	}
}

// 여러 줄로 붙여 넣은 게시글 주소·번호를 하나씩 나눈다 (빈 줄·중복 제거)
func TestSplitPostInputs(t *testing.T) {
	got := SplitPostInputs(" 1487997\r\nhttps://www.mohw.go.kr/board.es?mid=a10409020000&bid=0026&list_no=1490001&act=view\n\n1487997, 1490002\t1490003 ")
	want := []string{"1487997", "https://www.mohw.go.kr/board.es?mid=a10409020000&bid=0026&list_no=1490001&act=view", "1490002", "1490003"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %q", i, got[i])
		}
	}
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ImportNoticePosts(context.Background(), nil); err == nil {
		t.Fatal("빈 입력은 오류")
	}
	if _, err := s.ImportNoticePosts(context.Background(), make([]string, 41)); err == nil {
		t.Fatal("너무 많으면 오류")
	}
	// 잘못된 주소는 요청하지 않고 그 줄만 실패로 남긴다
	res, err := s.ImportNoticePosts(context.Background(), []string{"https://example.com/board.es?list_no=1"})
	if err != nil || len(res.Failed) != 1 || res.Imported != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}
