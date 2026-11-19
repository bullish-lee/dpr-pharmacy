package service

import (
	"database/sql"
)

type CrawlStatus struct {
	Enabled    bool   `json:"enabled"`
	Times      string `json:"times"`
	LastRun    string `json:"lastRun"`
	LastStatus string `json:"lastStatus"`
	LastOK     string `json:"lastOk"`
	Message    string `json:"message"`
	Stale      bool   `json:"stale"` // 3일 넘게 성공 기록 없음 (RSS 는 최근 30건만 보여주므로 누락 위험)
}

type Pending struct {
	Unresolved     int `json:"unresolved"`     // 초기재고 약품 미지정
	AmbiguousScans int `json:"ambiguousScans"` // 도매 확인 필요 바코드
	Planned        int `json:"planned"`        // 처리 예정(미완료)
	RulesToReview  int `json:"rulesToReview"`  // 보상 기준 확인 필요
	Unread         int `json:"unread"`
}

type Dashboard struct {
	Today        string        `json:"today"`
	Upcoming     []CutDate     `json:"upcoming"` // 다가오는(또는 최근) 시행일
	Deletions    []DeletionRow `json:"deletions"`
	LatestNotice *NoticeView   `json:"latestNotice"`
	Crawl        CrawlStatus   `json:"crawl"`
	LastPurchase string        `json:"lastPurchase"`
	LastDispense string        `json:"lastDispense"`
	Baseline     string        `json:"baseline"`
	Products     int           `json:"products"`
	Pending      Pending       `json:"pending"`
	Warnings     []string      `json:"warnings"`
	Health       []HealthItem  `json:"health"` // 데이터 점검 (재고·단위 이상)
}

func (s *Service) Dashboard() (*Dashboard, error) {
	d := &Dashboard{Today: s.Today(), Baseline: s.DB.GetSetting(SetBaselineDate, "")}
	d.LastPurchase, d.LastDispense = s.DataFreshness()
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM product").Scan(&d.Products)
	dates, err := s.PriceCutDates()
	if err != nil {
		return nil, err
	}
	for _, c := range dates {
		if c.Computed || c.DaysLeft >= 0 {
			d.Upcoming = append(d.Upcoming, c)
		}
		if len(d.Upcoming) >= 6 {
			break
		}
	}
	if dl, err := s.HeldDeletions(""); err == nil {
		d.Deletions = dl
	}
	if ns, err := s.ListNotices(); err == nil && len(ns) > 0 {
		d.LatestNotice = &ns[0]
	}
	d.Crawl = s.CrawlStatus()
	q := func(sqlText string, dst *int) { _ = s.DB.QueryRow(sqlText).Scan(dst) }
	q("SELECT COUNT(*) FROM initial_unresolved WHERE resolved_code = '' AND ignored = 0", &d.Pending.Unresolved)
	q("SELECT COUNT(*) FROM scan_unit WHERE supplier_id IS NULL AND match_status = 'ambiguous'", &d.Pending.AmbiguousScans)
	q("SELECT COUNT(*) FROM settlement WHERE status = 'planned'", &d.Pending.Planned)
	q("SELECT COUNT(*) FROM comp_rule WHERE needs_review = 1", &d.Pending.RulesToReview)
	q("SELECT COUNT(*) FROM notification WHERE read_at = ''", &d.Pending.Unread)
	d.Warnings = s.dataWarnings(d.Today)
	if h, err := s.DataHealth(); err == nil {
		d.Health = h
	}
	return d, nil
}

func (s *Service) CrawlStatus() CrawlStatus {
	st := CrawlStatus{Enabled: s.DB.GetSetting(SetCrawlEnabled, "1") == "1", Times: s.DB.GetSetting(SetCrawlTimes, defaultSettings[SetCrawlTimes])}
	var started, status, msg sql.NullString
	_ = s.DB.QueryRow("SELECT started_at, status, message FROM job_run WHERE job = 'crawl' ORDER BY id DESC LIMIT 1").Scan(&started, &status, &msg)
	st.LastRun, st.LastStatus, st.Message = nullStr(started), nullStr(status), nullStr(msg)
	var ok sql.NullString
	_ = s.DB.QueryRow("SELECT MAX(finished_at) FROM job_run WHERE job = 'crawl' AND status = 'ok'").Scan(&ok)
	st.LastOK = nullStr(ok)
	st.Stale = st.Enabled && s.isStale(st.LastOK)
	return st
}
