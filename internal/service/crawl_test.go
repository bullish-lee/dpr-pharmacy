package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dpr/internal/crawler"
	"dpr/internal/store"
)

// 합성: 첨부 페이지가 계속 실패하면 간격을 늘려 다시 시도하고, 알림은 한 번만 (하루 3번 반복하지 않음)
func TestCrawlFailureBackoff(t *testing.T) {
	var viewHits atomic.Int32
	rss := `<?xml version="1.0" encoding="UTF-8"?><rss><channel>
<item><title>「약제 급여 목록 및 급여 상한금액표」 일부개정</title><link>LINK/view?list_no=900001&amp;act=view</link><pubDate>Tue, 17 Nov 2026 09:00:00 +0900</pubDate></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/rss"):
			_, _ = w.Write([]byte(strings.ReplaceAll(rss, "LINK", "http://"+r.Host)))
		case strings.HasPrefix(r.URL.Path, "/view"):
			viewHits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	old := NewCrawler
	NewCrawler = func() *crawler.Client {
		c := crawler.New()
		c.RSSURL, c.Base, c.Backoff, c.Pause, c.Retries = srv.URL+"/rss", srv.URL, time.Millisecond, time.Millisecond, 2
		return c
	}
	defer func() { NewCrawler = old }()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.RunCrawl(ctx); err != nil {
		t.Fatal(err)
	}
	if viewHits.Load() != 2 { // 2번 시도 후 실패 처리
		t.Fatalf("첫 수집: 첨부 페이지 요청 %d회", viewHits.Load())
	}
	res, _ := s.RunCrawl(ctx)
	if viewHits.Load() != 2 || res.Deferred != 1 {
		t.Fatalf("바로 다시 수집하면 기다려야 함: 요청 %d회, 대기 %d", viewHits.Load(), res.Deferred)
	}
	var attempts int
	var next string
	_ = s.DB.QueryRow("SELECT attempts, next_try FROM notice WHERE list_no = '900001'").Scan(&attempts, &next)
	if attempts != 1 || next == "" {
		t.Fatalf("시도 %d, 다음 %q", attempts, next)
	}
	// 시각이 지나면 다시 시도 → 두 번째 실패 (알림 없음)
	_, _ = s.DB.Exec("UPDATE notice SET next_try = '2000-01-01T00:00:00Z'")
	_, _ = s.RunCrawl(ctx)
	var notes int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '고시 수집 중 오류'").Scan(&notes)
	if notes != 1 {
		t.Fatalf("같은 실패 알림은 처음(과 세 번째)에만: %d", notes)
	}
}

func TestCleanupInterruptedJobs(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := time.Now().Add(-time.Hour).Format(time.RFC3339)
	_, _ = s.DB.Exec("INSERT INTO job_run(job, started_at, status) VALUES('crawl', ?, 'running'), ('crawl', ?, 'running')", old, store.Now())
	s.CleanupInterruptedJobs()
	var running int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM job_run WHERE status = 'running'").Scan(&running)
	if running != 1 { // 방금 시작한 것(다른 프로세스일 수 있음)은 그대로
		t.Fatalf("running %d", running)
	}
}
