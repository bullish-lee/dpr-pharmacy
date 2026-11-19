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
)

// 열 이름을 알아보지 못한 표준코드 파일은 '반영함'으로 기록하지 않고 알린다. 같은 판은 다시 받지 않는다.
func TestStdCodeUnknownFormatIsFailure(t *testing.T) {
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/docs":
			_, _ = w.Write([]byte(`{"paths":{"/15067462/v1/uddi:bbbb":{"get":{"summary":"건강보험심사평가원_약가마스터_의약품표준코드_20260930"}}}}`))
		case "/tcs/dss/selectFileDataDownload.do":
			_, _ = w.Write([]byte(`{"fileDataRegistVO":{"atchFileId":"FILE_2","fileDetailSn":1,"orginlFileNm":"std.csv"}}`))
		case "/cmm/cmm/check-limit.json":
			_, _ = w.Write([]byte(`{"needCaptcha":false}`))
		case "/cmm/cmm/fileDownload.do":
			downloads.Add(1)
			w.Header().Set("Content-Disposition", `attachment; filename="std.csv"`)
			_, _ = w.Write([]byte("A열,B열,C열\nx,y,z\n" + strings.Repeat("\n", 1200)))
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
	if res, err := s.CheckStdCode(ctx, true); err == nil {
		t.Fatalf("형식을 모르는 파일은 실패여야 함: %+v", res)
	}
	if s.DB.GetSetting(SetStdUDDI, "") != "" {
		t.Fatal("반영한 판으로 기록하면 안 됨")
	}
	var notes int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '표준코드 자동 반영 실패'").Scan(&notes)
	if notes != 1 {
		t.Fatalf("알림 %d건", notes)
	}
	res, err := s.CheckStdCode(ctx, true)
	if err != nil || downloads.Load() != 1 || !strings.Contains(res.Message, "건너뜁니다") {
		t.Fatalf("같은 판은 다시 받지 않음: %+v %v (내려받기 %d)", res, err, downloads.Load())
	}
}

func fakeCrawler(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := NewCrawler
	NewCrawler = func() *crawler.Client {
		c := crawler.New()
		c.RSSURL, c.Base, c.Backoff, c.Pause, c.Retries = srv.URL+"/rss", srv.URL, time.Millisecond, time.Millisecond, 1
		return c
	}
	t.Cleanup(func() { NewCrawler = old })
}

// 앱을 끄거나 시간 한도에 걸려 중단된 수집은 실패로 세지 않는다 (시도 횟수·오류 알림 없음)
func TestCrawlCancelIsNotFailure(t *testing.T) {
	rss := `<?xml version="1.0" encoding="UTF-8"?><rss><channel>
<item><title>「약제 급여 목록 및 급여 상한금액표」 일부개정</title><link>LINK/view?list_no=900003&amp;act=view</link><pubDate>Tue, 17 Nov 2026 09:00:00 +0900</pubDate></item>
<item><title>「약제 급여 목록 및 급여 상한금액표」 일부개정</title><link>LINK/view?list_no=900004&amp;act=view</link><pubDate>Tue, 17 Nov 2026 09:00:00 +0900</pubDate></item>
</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/rss"):
			_, _ = w.Write([]byte(strings.ReplaceAll(rss, "LINK", "http://"+r.Host)))
		case strings.HasPrefix(r.URL.Path, "/view"):
			<-r.Context().Done() // 응답하지 않음 (멈춘 서버)
		}
	}))
	defer srv.Close()
	fakeCrawler(t, srv)
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _ = s.RunCrawl(ctx)
	var attempts, notes int
	_ = s.DB.QueryRow("SELECT IFNULL(SUM(attempts), 0) FROM notice").Scan(&attempts)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE level = 'error'").Scan(&notes)
	if attempts != 0 || notes != 0 {
		t.Fatalf("중단은 실패가 아님: 시도 %d, 오류 알림 %d", attempts, notes)
	}
}

// RSS 접속 실패 알림은 장애마다 한 번 (켤 때·정해진 시각마다 반복하지 않음), 다시 연결되면 다음 장애 때 다시 알림
func TestRSSFailureNotifiesOnce(t *testing.T) {
	var up atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><rss><channel></channel></rss>`))
	}))
	defer srv.Close()
	fakeCrawler(t, srv)
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	count := func() int {
		var n int
		_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '고시 확인 실패'").Scan(&n)
		return n
	}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, _ = s.RunCrawl(ctx)
	}
	if count() != 1 {
		t.Fatalf("같은 장애 알림은 한 번: %d", count())
	}
	up.Store(true)
	if _, err := s.RunCrawl(ctx); err != nil {
		t.Fatal(err)
	}
	up.Store(false)
	_, _ = s.RunCrawl(ctx)
	if count() != 2 {
		t.Fatalf("다시 연결된 뒤 새 장애는 다시 알림: %d", count())
	}
}

// 다른 프로세스가 같은 작업을 실행 중이면 시작하지 않는다 (확인과 기록이 한 문장)
func TestTryStartJobIsExclusive(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, ok := s.tryStartJob("stdcode")
	if !ok || id == 0 {
		t.Fatal("처음은 시작")
	}
	if _, ok := s.tryStartJob("stdcode"); ok {
		t.Fatal("실행 중이면 시작하지 않음")
	}
	if _, ok := s.tryStartJob("crawl"); !ok {
		t.Fatal("다른 작업은 시작")
	}
	s.finishJob(id, "ok", "")
	if _, ok := s.tryStartJob("stdcode"); !ok {
		t.Fatal("끝난 뒤에는 다시 시작")
	}
}

// 켤 때 정리(CleanupInterruptedJobs)는 작업마다 같은 '멈춤' 시간을 쓴다 — 예전에는 15분으로 잘라, 20분째 받는 중인
// 급여목록 작업을 '중단됨'으로 바꾸고 작업 스케줄러가 같은 작업을 또 시작했다
func TestCleanupUsesPerJobStale(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	started := time.Now().Add(-20 * time.Minute).Format(time.RFC3339)
	res, err := s.DB.Exec("INSERT INTO job_run(job, started_at, status) VALUES('drug_master', ?, 'running')", started)
	if err != nil {
		t.Fatal(err)
	}
	master, _ := res.LastInsertId()
	res, _ = s.DB.Exec("INSERT INTO job_run(job, started_at, status) VALUES('crawl', ?, 'running')", started)
	crawl, _ := res.LastInsertId()
	s.CleanupInterruptedJobs()
	var st1, st2 string
	_ = s.DB.QueryRow("SELECT status FROM job_run WHERE id = ?", master).Scan(&st1)
	_ = s.DB.QueryRow("SELECT status FROM job_run WHERE id = ?", crawl).Scan(&st2)
	if st1 != "running" || st2 != "error" {
		t.Fatalf("급여목록(30분) %s, 고시(10분) %s", st1, st2)
	}
	if _, ok := s.tryStartJob("drug_master"); ok {
		t.Fatal("아직 도는 급여목록 작업이 있는데 또 시작")
	}
	if runningJobs(s.DB) != 1 {
		t.Fatalf("실행 중 %d", runningJobs(s.DB))
	}
	// 반년 지난 기록·읽은 알림은 지운다
	old := time.Now().AddDate(0, -7, 0).Format(time.RFC3339)
	_, _ = s.DB.Exec("INSERT INTO job_run(job, started_at, status) VALUES('crawl', ?, 'ok')", old)
	_, _ = s.DB.Exec("INSERT INTO notification(created_at, title, read_at) VALUES(?, '옛 알림', ?)", old, old)
	_, _ = s.DB.Exec("INSERT INTO notification(created_at, title) VALUES(?, '안 읽은 옛 알림')", old)
	s.CleanupInterruptedJobs()
	var jobs, notes int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM job_run WHERE started_at = ?", old).Scan(&jobs)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE created_at = ?", old).Scan(&notes)
	if jobs != 0 || notes != 1 {
		t.Fatalf("옛 기록 %d, 옛 알림 %d (안 읽은 것은 남김)", jobs, notes)
	}
}
