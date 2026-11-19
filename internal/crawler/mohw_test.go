package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>보건복지부</title>
<item><title><![CDATA[「요양급여의 적용기준 및 방법에 관한 세부사항」 일부개정고시]]></title>
<link>https://www.mohw.go.kr/board.es?mid=a10409020000&amp;bid=0026&amp;list_no=1492514&amp;act=view</link>
<pubDate>Wed, 11 Nov 2026 05:59:00 GMT</pubDate></item>
<item><title><![CDATA[「한약제제 급여목록 및 상한금액표」 일부개정(고시 제2026-229호) 안내]]></title>
<link>https://www.mohw.go.kr/board.es?mid=a10409020000&amp;bid=0026&amp;list_no=1492423&amp;act=view</link>
<pubDate>Tue, 03 Nov 2026 06:33:00 GMT</pubDate></item>
<item><title><![CDATA[「약제 급여 목록 및 급여 상한금액표」일부개정고시(제2026-219호) 안내]]></title>
<link>https://www.mohw.go.kr/board.es?mid=a10409020000&amp;bid=0026&amp;list_no=1492330&amp;act=view</link>
<pubDate>Tue, 27 Oct 2026 06:06:00 GMT</pubDate></item>
</channel></rss>`

const sampleView = `<html><body><div class="file"><ul>
<li>약제 급여 목록 및 급여 상한금액표 일부개정고시(2026-219호).hwpx
<a class="btn_line" href="/boardDownload.es?bid=0026&amp;list_no=1492330&amp;seq=1" title="약제 급여 목록 및 급여 상한금액표 일부개정고시(2026-219호).hwpx">다운로드</a></li>
<li><a class="btn_line" href="/boardDownload.es?bid=0026&amp;list_no=1492330&amp;seq=3" title="약제 급여 목록 및 급여 상한금액표 일부개정고시(2026-219호)_별표1 목록표.xlsx">다운로드</a>
<a class="btn_line" href="/attachPreview.es?bid=0026&amp;list_no=1492330&amp;seq=3" title="미리보기">미리보기</a></li>
<li><a href="/boardDownload.es?bid=0026&amp;list_no=1492330&amp;seq=4" title="약제 급여 목록 및 급여 상한금액표 일부개정고시(2026-219호)_별표1 목록표.pdf">다운로드</a></li>
</ul></div></body></html>`

func TestParseRSSAndFilter(t *testing.T) {
	items, err := ParseRSS([]byte(sampleRSS))
	if err != nil || len(items) != 3 {
		t.Fatalf("RSS: %v %d", err, len(items))
	}
	if items[2].ListNo != "1492330" || items[2].Published.Year() != 2026 {
		t.Fatalf("항목: %+v", items[2])
	}
	inc, exc := SplitKeywords("약제 급여 목록|상한금액표"), SplitKeywords("한약제제")
	var matched []string
	for _, it := range items {
		if MatchTitle(it.Title, inc, exc) {
			matched = append(matched, it.ListNo)
		}
	}
	if len(matched) != 1 || matched[0] != "1492330" {
		t.Fatalf("필터 결과: %v", matched)
	}
}

func TestParseAttachmentsAndPick(t *testing.T) {
	atts, err := ParseAttachments([]byte(sampleView), DefaultBase)
	if err != nil || len(atts) != 3 {
		t.Fatalf("첨부: %v %+v", err, atts)
	}
	pick := PickNoticeAttachment(atts)
	if pick == nil || pick.Seq != 3 || !strings.HasPrefix(pick.URL, "https://www.mohw.go.kr/boardDownload.es") {
		t.Fatalf("선택: %+v", pick)
	}
}

func TestClientAgainstFakeServer(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasPrefix(r.URL.Path, "/rss"):
			if calls == 1 { // 첫 요청은 일시 장애 → 재시도 확인
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(sampleRSS))
		case strings.HasPrefix(r.URL.Path, "/view"):
			_, _ = w.Write([]byte(sampleView))
		case strings.HasPrefix(r.URL.Path, "/boardDownload.es"):
			w.Header().Set("Content-Disposition", `attachment;filename="%EB%AA%A9%EB%A1%9D%ED%91%9C.xlsx"`)
			_, _ = w.Write([]byte("PK-data"))
		}
	}))
	defer srv.Close()
	c := New()
	c.RSSURL, c.Base, c.Retries, c.Backoff = srv.URL+"/rss", srv.URL, 3, 10*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	items, err := c.FetchRSS(ctx)
	if err != nil || len(items) != 3 {
		t.Fatalf("FetchRSS: %v", err)
	}
	atts, err := c.FetchAttachments(ctx, srv.URL+"/view")
	if err != nil || len(atts) != 3 {
		t.Fatalf("FetchAttachments: %v", err)
	}
	data, name, err := c.Download(ctx, PickNoticeAttachment(atts).URL)
	if err != nil || string(data) != "PK-data" || name != "목록표.xlsx" {
		t.Fatalf("Download: %v %q %q", err, data, name)
	}
}

func TestRetryAfterAndNoRetryOn404(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		if calls < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(sampleRSS))
	}))
	defer srv.Close()
	c := New()
	c.RSSURL, c.Backoff = srv.URL+"/rss", time.Millisecond
	t0 := time.Now()
	if _, err := c.FetchRSS(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d < 2*time.Second {
		t.Fatalf("Retry-After(1초)를 지켜야 함: %v", d)
	}
	calls = 0
	c.RSSURL = srv.URL + "/missing"
	if _, err := c.FetchRSS(context.Background()); err == nil || calls != 1 {
		t.Fatalf("404 는 다시 시도하지 않음: calls=%d err=%v", calls, err)
	}
}
