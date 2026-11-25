package service

import (
	"context"
	"database/sql"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"dpr/internal/crawler"
	"dpr/internal/store"
)

type CrawlResult struct {
	Checked  int      `json:"checked"` // RSS 항목 수
	Matched  int      `json:"matched"` // 약제 급여 목록 고시 수
	New      int      `json:"new"`     // 새로 가져온 고시
	Notices  []string `json:"notices"`
	Errors   []string `json:"errors"`
	Skipped  bool     `json:"skipped"`  // 이미 다른 수집이 실행 중
	Deferred int      `json:"deferred"` // 실패해서 다음 시도 시각을 기다리는 고시
}

// Crawler 는 테스트에서 가짜 서버로 바꿀 수 있다.
var NewCrawler = crawler.New

// RunCrawl 은 복지부 RSS 를 확인하고 새 약제 급여 목록 고시를 내려받아 분석한다.
func (s *Service) RunCrawl(ctx context.Context) (*CrawlResult, error) {
	if !s.crawlMu.TryLock() {
		return &CrawlResult{Skipped: true}, nil
	}
	defer s.crawlMu.Unlock()
	res := &CrawlResult{}
	// 다른 프로세스(작업 스케줄러로 실행된 DPR crawl)가 막 수집 중이면 건너뜀 — 확인과 '실행 중' 기록을 한 번에(원자적)
	jobID, ok := s.tryStartJob("crawl")
	if !ok {
		return &CrawlResult{Skipped: true}, nil
	}
	finish := func(status, msg string) { s.finishJob(jobID, status, msg) }

	c := NewCrawler()
	items, err := c.FetchRSS(ctx)
	if err != nil {
		if ctx.Err() != nil {
			finish("error", "중단됨 (프로그램 종료 또는 시간 초과)")
			return res, err
		}
		finish("error", err.Error())
		s.notifyRSSFailure(err)
		return res, err
	}
	_ = s.DB.SetSetting(setRSSFailNotified, "") // 다시 연결됨 → 다음 장애 때 다시 알림
	res.Checked = len(items)
	include := crawler.SplitKeywords(s.DB.GetSetting(SetCrawlInclude, defaultSettings[SetCrawlInclude]))
	exclude := crawler.SplitKeywords(s.DB.GetSetting(SetCrawlExclude, defaultSettings[SetCrawlExclude]))
	for _, it := range items {
		if it.ListNo == "" || !crawler.MatchTitle(it.Title, include, exclude) {
			continue
		}
		res.Matched++
		var status, next string
		if err := s.DB.QueryRow("SELECT status, next_try FROM notice WHERE list_no = ?", it.ListNo).Scan(&status, &next); err == nil {
			if status == "parsed" || status == "duplicate" {
				continue // 이미 수집한 게시글(또는 같은 목록표를 다른 게시글로 수집함)은 다시 요청하지 않음
			}
			if next != "" && next > time.Now().Format(time.RFC3339) {
				res.Deferred++
				continue // 실패한 게시글은 정한 간격이 지나야 다시 시도
			}
		}
		if ctx.Err() != nil { // 취소(앱 종료·시간 한도)는 실패로 세지 않는다 — 다음 수집 때 이어서
			res.Errors = append(res.Errors, "중단됨")
			break
		}
		published := it.Published.In(s.loc).Format("2006-01-02")
		s.markDetected(it, published)
		if crawler.Pause(ctx, c.Pause) != nil {
			res.Errors = append(res.Errors, "중단됨")
			break
		}
		atts, err := c.FetchAttachments(ctx, it.Link)
		if err != nil {
			if ctx.Err() != nil {
				res.Errors = append(res.Errors, "중단됨")
				break
			}
			s.markFailed(it.ListNo, "첨부 목록 조회 실패: "+err.Error())
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		att := crawler.PickNoticeAttachment(atts)
		if att == nil {
			s.markRetry(it.ListNo, "no_attachment", "목록표 첨부를 찾지 못함")
			continue
		}
		if crawler.Pause(ctx, c.Pause) != nil {
			res.Errors = append(res.Errors, "중단됨")
			break
		}
		data, fname, err := c.Download(ctx, att.URL)
		if err != nil {
			if ctx.Err() != nil {
				res.Errors = append(res.Errors, "중단됨")
				break
			}
			s.markFailed(it.ListNo, "첨부 다운로드 실패: "+err.Error())
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		if att.Name != "" {
			fname = att.Name
		}
		id, msg, dup, err := s.ingestNoticeFile("mohw", it.ListNo, it.Title, it.Link, published, fname, data)
		if err != nil {
			s.markFailed(it.ListNo, "목록표 해석 실패: "+err.Error())
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		if dup { // 직접 올렸거나 다른 게시글로 이미 받은 목록표 → 새 고시가 아니므로 알리지 않는다
			s.Log.Info("이미 가져온 고시 목록표 — 게시글만 연결", "list_no", it.ListNo, "notice", id)
			continue
		}
		res.New++
		res.Notices = append(res.Notices, msg)
		s.notify("info", "새 약가 고시가 수집되었습니다", msg+s.heldSummary(id), fmt.Sprintf("notice:%d", id))
	}
	status, msg := "ok", fmt.Sprintf("RSS %d건 확인, 대상 %d건, 신규 %d건", res.Checked, res.Matched, res.New)
	if len(res.Errors) > 0 {
		status, msg = "error", msg+" / 오류: "+strings.Join(res.Errors, "; ")
	}
	finish(status, msg)
	return res, nil
}

func (s *Service) heldSummary(noticeID int64) string {
	rows, err := s.NoticeChanges(noticeID, "all", true)
	if err != nil || len(rows) == 0 {
		return ""
	}
	down, del := 0, 0
	for _, r := range rows {
		switch {
		case r.Detail == "price_down":
			down++
		case r.Kind == "delete":
			del++
		}
	}
	return fmt.Sprintf(" — 보유 품목 중 인하 %d건, 삭제 %d건", down, del)
}

func (s *Service) markDetected(it crawler.Item, published string) {
	now := store.Now()
	_, err := s.DB.Exec(`INSERT INTO notice(source, list_no, title, url, published_at, status, created_at, updated_at) VALUES('mohw', ?, ?, ?, ?, 'detected', ?, ?)
		ON CONFLICT(list_no) DO UPDATE SET title = excluded.title, url = excluded.url, published_at = excluded.published_at, updated_at = excluded.updated_at`,
		it.ListNo, it.Title, it.Link, published, now, now)
	if err != nil {
		s.Log.Warn("고시 감지 기록 실패", "err", err)
	}
}

// 계속 실패하는 고시의 다음 시도까지 간격 (시도 횟수별)
var retryLadder = []time.Duration{time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}

func nextTry(attempts int) string {
	i := attempts - 1
	if i < 0 {
		i = 0
	}
	if i >= len(retryLadder) {
		i = len(retryLadder) - 1
	}
	return time.Now().Add(retryLadder[i]).Format(time.RFC3339)
}

// markFailed : 실패 횟수를 세고 다음 시도 시각을 미룬다. 알림은 처음과 세 번째 실패에만 (하루 3번 같은 알림 반복 방지).
func (s *Service) markFailed(listNo, msg string) {
	n := s.markRetry(listNo, "failed", msg)
	if n == 1 || n == 3 {
		s.notify("error", "고시 수집 중 오류", fmt.Sprintf("%s (실패 %d회, 간격을 늘려 다시 시도합니다) — '고시' 화면에서 목록표 엑셀을 직접 올릴 수도 있습니다.", msg, n), "notices")
	}
}

func (s *Service) markRetry(listNo, status, msg string) int {
	var attempts int
	_ = s.DB.QueryRow("SELECT attempts FROM notice WHERE list_no = ?", listNo).Scan(&attempts)
	attempts++
	_, _ = s.DB.Exec("UPDATE notice SET status = ?, error = ?, attempts = ?, next_try = ?, updated_at = ? WHERE list_no = ? AND status <> 'parsed'",
		status, msg, attempts, nextTry(attempts), store.Now(), listNo)
	return attempts
}

// jobStale : 이보다 오래 '실행 중'인 작업은 멈춘 것으로 본다 — 앱과 작업 스케줄러(DPR crawl)가 같은 값을 쓴다.
// 각 작업의 시간 한도(고시 5분·표준코드 10분·급여목록 15분)보다 길어야 한다. 짧으면 아직 도는 작업을 '중단됨'으로 바꾸고
// 다른 프로세스가 같은 작업을 또 시작한다.
var jobStale = map[string]time.Duration{"crawl": 10 * time.Minute, "stdcode": 30 * time.Minute, "drug_master": 30 * time.Minute, "update": 30 * time.Minute}

func staleAfter(job string) time.Duration {
	if d, ok := jobStale[job]; ok {
		return d
	}
	return 30 * time.Minute
}

// runningJobs : 지금 (어느 프로세스에서든) 실행 중인 작업 수 — 멈춘 것으로 볼 만큼 오래된 것은 뺀다
func runningJobs(q interface {
	QueryRow(string, ...any) *sql.Row
}) int {
	n := 0
	for job, d := range jobStale {
		var k int
		_ = q.QueryRow("SELECT COUNT(*) FROM job_run WHERE job = ? AND status = 'running' AND started_at > ?", job, time.Now().Add(-d).Format(time.RFC3339)).Scan(&k)
		n += k
	}
	return n
}

// CleanupInterruptedJobs : 프로그램이 도중에 꺼져 '진행 중'으로 남은 작업을 정리한다 (켤 때, 앱·작업 스케줄러 모두).
// 반년 지난 작업 기록과 읽은 알림도 지운다 (하루 10건 남짓 쌓인다).
func (s *Service) CleanupInterruptedJobs() {
	now := store.Now()
	for job, d := range jobStale {
		_, _ = s.DB.Exec("UPDATE job_run SET status = 'error', finished_at = ?, message = '중단됨 (프로그램이 끝나기 전에 종료됨)' WHERE job = ? AND status = 'running' AND started_at < ?",
			now, job, time.Now().Add(-d).Format(time.RFC3339))
	}
	old := time.Now().AddDate(0, -6, 0).Format(time.RFC3339)
	_, _ = s.DB.Exec("DELETE FROM job_run WHERE status <> 'running' AND started_at < ?", old)
	_, _ = s.DB.Exec("DELETE FROM notification WHERE read_at <> '' AND created_at < ?", old)
}

const setRSSFailNotified = "crawl_rss_fail_notified"

// notifyRSSFailure : RSS 접속 실패 알림은 장애가 시작될 때 한 번, 계속되면 하루에 한 번만 (켤 때·정해진 시각마다 반복하지 않게)
func (s *Service) notifyRSSFailure(err error) {
	if t, e := time.Parse(time.RFC3339, s.DB.GetSetting(setRSSFailNotified, "")); e == nil && time.Since(t) < 24*time.Hour {
		s.Log.Warn("고시 확인 실패 (알림은 이미 보냄)", "err", err)
		return
	}
	_ = s.DB.SetSetting(setRSSFailNotified, time.Now().Format(time.RFC3339))
	s.notify("warn", "고시 확인 실패", "복지부 RSS 에 접속하지 못했습니다: "+err.Error()+" — 자동으로 다시 시도합니다 (같은 알림은 하루 한 번).", "settings")
}

// tryStartJob : 같은 작업이 (다른 프로세스에서도) 실행 중이 아니면 '실행 중'으로 기록하고 시작한다.
// 확인과 기록을 한 문장으로 해서 두 프로세스가 동시에 시작하지 못한다. staleAfter 보다 오래된 '실행 중'은 멈춘 것으로 본다.
func (s *Service) tryStartJob(job string) (int64, bool) {
	res, err := s.DB.Exec(`INSERT INTO job_run(job, started_at, status) SELECT ?, ?, 'running'
		WHERE NOT EXISTS (SELECT 1 FROM job_run WHERE job = ? AND status = 'running' AND started_at > ?)`,
		job, store.Now(), job, time.Now().Add(-staleAfter(job)).Format(time.RFC3339))
	if err != nil {
		s.Log.Warn("작업 기록 실패", "job", job, "err", err)
		return 0, true // 기록을 못 해도 작업은 한다 (같은 프로세스 안의 중복은 잠금이 막는다)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, false
	}
	id, _ := res.LastInsertId()
	return id, true
}

func (s *Service) startJob(job string) int64 {
	res, err := s.DB.Exec("INSERT INTO job_run(job, started_at, status) VALUES(?, ?, 'running')", job, store.Now())
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

func (s *Service) finishJob(id int64, status, msg string) {
	_, _ = s.DB.Exec("UPDATE job_run SET finished_at = ?, status = ?, message = ? WHERE id = ?", store.Now(), status, msg, id)
}

func (s *Service) isStale(lastOK string) bool {
	if lastOK == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, lastOK)
	return err != nil || time.Since(t) > 72*time.Hour
}

type JobRun struct {
	ID         int64  `json:"id"`
	Job        string `json:"job"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	Status     string `json:"status"`
	Message    string `json:"message"`
}

func (s *Service) JobRuns(limit int) ([]JobRun, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.DB.Query("SELECT id, job, started_at, finished_at, status, message FROM job_run ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobRun
	for rows.Next() {
		var j JobRun
		if err := rows.Scan(&j.ID, &j.Job, &j.StartedAt, &j.FinishedAt, &j.Status, &j.Message); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ── 앱 내 스케줄러 (창이 열려 있는 동안) ───────────────────────────

// StartScheduler 는 설정한 시각마다 수집하고, 하루 1회 자동 백업한다.
// 창이 닫혀 있어도 수집되게 하려면 Windows 작업 스케줄러 등록(InstallWindowsTasks)을 함께 쓴다.
// StartScheduler 는 앱이 켜져 있는 동안의 자동 확인을 돌린다.
//   - 켤 때 확인(설정): 고시(마지막 시도 30분 이내면 건너뜀) · 표준코드(하루 1번) · 새 버전
//   - 정해진 시각: 고시 + 표준코드(하루 1번). Windows 작업 스케줄러를 등록했으면 그쪽이 맡으므로 건너뛴다.
func (s *Service) StartScheduler(ctx context.Context) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.Log.Error("자동 확인 중 비정상 종료", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			}
		}()
		ran := map[string]string{} // "HH:MM" → 마지막 실행 날짜
		select {
		case <-ctx.Done():
			return
		case <-time.After(8 * time.Second):
		}
		s.AutoBackupIfDue()
		if err := s.EnsureSettlePairs(); err != nil {
			s.Log.Warn("보상 정산 쌍 표시 실패", "err", err)
		}
		// 프로그램에 넣어 둔 지난 1년 고시 (처음 한 번 — 인터넷 요청 없음)
		if n, err := s.SeedPastNotices(); err != nil {
			s.Log.Warn("지난 고시 넣기 실패", "err", err)
		} else if n > 0 {
			s.notify("info", "지난 고시를 넣었습니다", fmt.Sprintf("프로그램에 들어 있던 지난 1년 고시 %d건(인하·인상·신설·삭제·변경)을 넣었습니다. 고시 화면에서 볼 수 있습니다.", n), "notices")
		}
		if s.DB.GetSetting(SetCheckOnStart, "1") == "1" {
			s.startupChecks(ctx)
		}
		// 켜기 전에 지난 시각은 이미 처리한 것으로 본다 (켤 때 확인이 맡음)
		markPassed := func(now time.Time) {
			times, _ := ParseCrawlTimes(s.DB.GetSetting(SetCrawlTimes, defaultSettings[SetCrawlTimes]))
			for _, t := range times {
				if t <= now.Format("15:04") {
					ran[t] = now.Format("2006-01-02")
				}
			}
		}
		markPassed(s.NowKST())
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		lastVer, _ := s.dataVersion()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 작업 스케줄러(다른 프로세스)가 새 고시·알림을 저장했으면 화면이 알 수 있게 (화면은 자기 프로세스의 알림만 받는다)
				if v, ok := s.dataVersion(); ok {
					if v.gen == lastVer.gen && v.v != lastVer.v && s.OnDataChanged != nil {
						s.OnDataChanged("external")
					}
					lastVer = v
				}
				// 작업 스케줄러를 등록했어도 앱 안 확인은 계속한다 — 등록이 깨져도(exe 를 옮김 등) 확인이 멈추지 않게.
				// 같은 시각에 둘 다 돌면 tryStartJob 이 하나만 돌리고, 방금 한 고시 확인은 건너뛴다.
				if s.DB.GetSetting(SetCrawlEnabled, "1") != "1" {
					continue
				}
				now := s.NowKST()
				hm, day := now.Format("15:04"), now.Format("2006-01-02")
				times, err := ParseCrawlTimes(s.DB.GetSetting(SetCrawlTimes, defaultSettings[SetCrawlTimes]))
				if err != nil {
					continue
				}
				// 정한 시각이 지났는데 오늘 아직 안 했으면 실행 (긴 작업 중에 그 분을 놓쳐도 따라잡는다)
				due := false
				for _, t := range times {
					if t <= hm && ran[t] != day {
						ran[t] = day
						due = true
					}
				}
				if due {
					if !s.ranRecently("crawl", 10*time.Minute) {
						s.runCrawlLogged(ctx)
					}
					s.runStdCodeLogged(ctx)
					s.runMasterLogged(ctx)
				}
			}
		}
	}()
}

// OnStartupCheck 는 켤 때 확인에 덧붙일 작업 (업데이트 확인 등, app.go 에서 연결)
var OnStartupCheck func(ctx context.Context)

// ranRecently : 작업이 (어느 프로세스에서든) d 안에 시작됐는가
func (s *Service) ranRecently(job string, d time.Duration) bool {
	var last sql.NullString
	_ = s.DB.QueryRow("SELECT MAX(started_at) FROM job_run WHERE job = ?", job).Scan(&last)
	t, err := time.Parse(time.RFC3339, nullStr(last))
	return err == nil && time.Since(t) < d
}

func (s *Service) startupChecks(ctx context.Context) {
	if s.DB.GetSetting(SetCrawlEnabled, "1") == "1" && !s.ranRecently("crawl", 30*time.Minute) {
		s.runCrawlLogged(ctx)
	}
	s.runStdCodeLogged(ctx)
	s.runMasterLogged(ctx)
	if OnStartupCheck != nil {
		OnStartupCheck(ctx)
	}
}

// runStdCodeLogged : 표준코드 새 판 확인 (설정이 꺼져 있거나 오늘 확인했으면 건너뜀)
func (s *Service) runStdCodeLogged(ctx context.Context) {
	if s.DB.GetSetting(SetStdAuto, "1") != "1" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if res, err := s.CheckStdCode(cctx, false); err != nil {
		s.Log.Warn("표준코드 확인 실패", "err", err)
	} else if !res.Skipped {
		s.Log.Info("표준코드 확인", "updated", res.Updated, "date", res.Snapshot.Date)
	}
}

func (s *Service) runCrawlLogged(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if res, err := s.RunCrawl(cctx); err != nil {
		s.Log.Warn("고시 수집 실패", "err", err)
	} else {
		s.Log.Info("고시 수집", "checked", res.Checked, "matched", res.Matched, "new", res.New)
	}
}
