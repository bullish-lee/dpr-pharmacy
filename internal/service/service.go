// Package service 는 화면(Wails 바인딩)과 CLI 가 호출하는 유스케이스 계층이다.
// DB(store) + 파서(ingest) + 계산(domain) + 수집기(crawler)를 조립하며, 화면 로직은 두지 않는다.
package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Windows 에서도 Asia/Seoul 시간대를 쓰기 위해 내장

	"dpr/internal/applog"
	"dpr/internal/crawler"
	"dpr/internal/domain"
	"dpr/internal/store"
)

// AppVersion 은 빌드할 때 VERSION 파일 값으로 정해진다 (Makefile: -ldflags -X). 개발 실행은 "dev".
var AppVersion = "dev"

// 설정 키
const (
	SetBaselineDate   = "baseline_date"   // 초기재고 기준일
	SetBaselineTiming = "baseline_timing" // start | end
	SetCrawlEnabled   = "crawl_enabled"   // "1" | "0"
	SetCrawlTimes     = "crawl_times"     // "08:30,12:30,17:30"
	SetCrawlInclude   = "crawl_include"   // 제목 포함 키워드 (| 구분, 모두 포함해야 함)
	SetCrawlExclude   = "crawl_exclude"   // 제목 제외 키워드 (| 구분, 하나라도 포함 시 제외)
	SetLastBackup     = "last_backup"
	SetOSTasks        = "os_tasks_installed" // Windows 작업 스케줄러 등록 여부
	SetZoom           = "ui_zoom"            // 화면 배율 (100 | 112.5 | 125)
	SetTheme          = "ui_theme"           // light | dark | system
	SetCheckOnStart   = "check_on_start"     // 프로그램을 켤 때 고시·표준코드·업데이트 확인 ("1" | "0")
	SetStdAuto        = "stdcode_auto"       // 표준코드 새 판 매일 확인 ("1" | "0")
	SetStdUDDI        = "stdcode_uddi"       // 반영한 표준코드 판 (odcloud uddi)
	SetStdDate        = "stdcode_date"       // 반영한 판 기준일
	SetStdChecked     = "stdcode_checked_at" // 마지막 확인 시각 (RFC3339)
	SetUpdateCheck    = "update_check"       // 새 버전 확인 ("1" | "0")
	SetUpdateRepo     = "update_repo"        // GitHub 저장소 owner/name
	SetUpdateToken    = "update_token"       // 비공개 포크용 GitHub 토큰 — 공개 저장소에서는 필요 없음 (화면에 다시 보여주지 않음)
	SetUpdateChecked  = "update_checked_at"
	SetLogDebug       = "log_debug"   // 자세한 기록 (DEBUG) "1" | "0"
	SetDataGoKey      = "datago_key"  // 공공데이터포털 인증키 (약가기준정보 — 화면에 다시 보여주지 않음, 기록·보고서에서 가림)
	SetMasterAuto     = "master_auto" // 약제급여목록 매달 자동 받기 ("1" | "0", 인증키가 있을 때)
)

// DefaultUpdateRepo 는 업데이트를 받아올 기본 저장소 (공개 저장소 — 토큰 없이 받는다)
const DefaultUpdateRepo = "bullish-lee/dpr-pharmacy"

var defaultSettings = map[string]string{
	SetCheckOnStart:   "1",
	SetStdAuto:        "1",
	SetMasterAuto:     "1",
	SetUpdateCheck:    "1",
	SetUpdateRepo:     DefaultUpdateRepo,
	SetTheme:          "light",
	SetCrawlEnabled:   "1",
	SetCrawlTimes:     "08:30,12:30,17:30",
	SetCrawlInclude:   "약제 급여 목록|상한금액표",
	SetCrawlExclude:   "한약제제",
	SetBaselineTiming: "start",
}

type Service struct {
	DB       *store.DB
	DataDir  string
	Log      *slog.Logger
	loc      *time.Location
	importMu sync.Mutex
	crawlMu  sync.Mutex
	// OnNotify 는 새 알림이 생길 때 호출된다 (Wails 이벤트·토스트 연결용).
	OnNotify func(n Notification)
	stdMu    sync.Mutex // 표준코드 확인 중복 방지
	masterMu sync.Mutex // 약제급여목록 받기 중복 방지
	updMu    sync.Mutex // 업데이트 확인 중복 방지
	// AsyncRematch: 앱(화면)에서는 이름 재매칭을 가져오기 뒤 백그라운드로 돌려 응답을 빠르게 한다 (명령줄·테스트는 바로 실행)
	AsyncRematch  bool
	OnDataChanged func(what string)
	rematchMu     sync.Mutex
	rematchDirty  bool
	rematchBusy   bool
	cache         memo // 화면 계산 캐시 (cache.go)
	verMu         sync.Mutex
	verConn       *sql.Conn
	verGen        int64
	DataDirSource string // 데이터 폴더를 정한 방법 (platform.Source*) — main 이 넣는다
	switchMu      sync.Mutex
	switching     *dataDirSwitch // 데이터 폴더를 바꾸고 다시 시작을 기다리는 중 (datadir.go)
}

// New 는 데이터 폴더를 준비하고 DB 를 연다.
func New(dataDir string, log *slog.Logger) (*Service, error) {
	if log == nil {
		log = slog.Default()
	}
	for _, sub := range []string{"", "raw", "backups", "logs"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("데이터 폴더 생성 실패: %w", err)
		}
	}
	db, err := store.Open(filepath.Join(dataDir, "dpr.db"))
	if err != nil {
		return nil, err
	}
	return newWithDB(db, dataDir, log)
}

// NewTemp 는 테스트용 서비스 (운영과 같은 파일 DB·WAL 설정, dataDir 은 임시 폴더).
func NewTemp(dataDir string) (*Service, error) {
	return New(dataDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newWithDB(db *store.DB, dataDir string, log *slog.Logger) (*Service, error) {
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		loc = time.FixedZone("KST", 9*3600)
	}
	s := &Service{DB: db, DataDir: dataDir, Log: log, loc: loc}
	if err := s.seed(); err != nil {
		db.Close()
		return nil, err
	}
	s.CleanupInterruptedJobs()
	applog.SetDebug(s.DB.GetSetting(SetLogDebug, "0") == "1")
	return s, nil
}

func (s *Service) Close() error {
	s.closeCache()
	_, _ = s.DB.Exec("PRAGMA optimize") // 쿼리 계획 통계 갱신 (SQLite 권장: 연결을 닫기 전에)
	return s.DB.Close()
}

func (s *Service) seed() error {
	for k, v := range defaultSettings {
		if s.DB.GetSetting(k, "\x00") == "\x00" {
			if err := s.DB.SetSetting(k, v); err != nil {
				return err
			}
		}
	}
	var n int
	if err := s.DB.QueryRow("SELECT COUNT(*) FROM comp_rule WHERE supplier_id IS NULL").Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		r := domain.DefaultCompRule()
		_, err := s.DB.Exec(`INSERT INTO comp_rule(supplier_id, valid_from, rate_bp, window_mode, lookback, cutoff_day, extend_to_anchor, anchor, rounding, net_returns, settlement_day, needs_review, memo, created_at, updated_at)
			VALUES(NULL, '2000-01-01', ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`,
			r.RateBP, r.WindowMode, r.Lookback, r.CutoffDay, b2i(r.ExtendToAnchor), r.Anchor, r.Rounding, b2i(r.NetReturns), r.SettlementDay,
			"흔한 기본값(30%·3개월) — 도매별 실제 기준을 받으면 수정하세요", store.Now(), store.Now())
		return err
	}
	return nil
}

// Today 는 한국 시간 기준 오늘 (YYYY-MM-DD).
func (s *Service) Today() string { return time.Now().In(s.loc).Format(domain.DateLayout) }

func (s *Service) NowKST() time.Time { return time.Now().In(s.loc) }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func nullF(nf sql.NullFloat64) float64 {
	if nf.Valid {
		return nf.Float64
	}
	return 0
}

// inClause 는 "?,?,?" 와 인자 목록을 만든다.
func inClause(items []string) (string, []any) {
	if len(items) == 0 {
		return "''", nil
	}
	args := make([]any, len(items))
	for i, it := range items {
		args[i] = it
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(items)), ","), args
}

var ErrNotFound = errors.New("대상을 찾을 수 없습니다")

// ── 알림 ──────────────────────────────────────────────────────────

type Notification struct {
	ID        int64  `json:"id"`
	CreatedAt string `json:"createdAt"`
	Level     string `json:"level"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Link      string `json:"link"`
	ReadAt    string `json:"readAt"`
}

func (s *Service) notify(level, title, body, link string) {
	res, err := s.DB.Exec("INSERT INTO notification(created_at, level, title, body, link) VALUES(?, ?, ?, ?, ?)", store.Now(), level, title, body, link)
	if err != nil {
		s.Log.Error("알림 저장 실패", "err", err)
		return
	}
	id, _ := res.LastInsertId()
	if s.OnNotify != nil {
		s.OnNotify(Notification{ID: id, CreatedAt: store.Now(), Level: level, Title: title, Body: body, Link: link})
	}
}

func (s *Service) Notifications(limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.DB.Query("SELECT id, created_at, level, title, body, link, read_at FROM notification ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.CreatedAt, &n.Level, &n.Title, &n.Body, &n.Link, &n.ReadAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Service) MarkNotificationsRead() error {
	_, err := s.DB.Exec("UPDATE notification SET read_at = ? WHERE read_at = ''", store.Now())
	return err
}

// ── 설정 ──────────────────────────────────────────────────────────

type Settings struct {
	DataDir        string `json:"dataDir"`
	DBPath         string `json:"dbPath"`
	Version        string `json:"version"`
	BaselineDate   string `json:"baselineDate"`
	BaselineTiming string `json:"baselineTiming"`
	CrawlEnabled   bool   `json:"crawlEnabled"`
	CrawlTimes     string `json:"crawlTimes"`
	CrawlInclude   string `json:"crawlInclude"`
	CrawlExclude   string `json:"crawlExclude"`
	LastBackup     string `json:"lastBackup"`
	OSTasks        bool   `json:"osTasks"`
	Zoom           string `json:"zoom"`
	Theme          string `json:"theme"`
	CheckOnStart   bool   `json:"checkOnStart"`
	StdAuto        bool   `json:"stdAuto"`
	StdDate        string `json:"stdDate"`
	StdCheckedAt   string `json:"stdCheckedAt"`
	StdCount       int    `json:"stdCount"` // 공식 표준코드 매핑 수
	UpdateCheck    bool   `json:"updateCheck"`
	UpdateRepo     string `json:"updateRepo"`
	HasUpdateToken bool   `json:"hasUpdateToken"`
	UpdateChecked  string `json:"updateCheckedAt"`
	LogDebug       bool   `json:"logDebug"`
	Engine         string `json:"engine"` // SQLite 엔진 (C / Go)
	MasterAuto     bool   `json:"masterAuto"`
	HasDataGoKey   bool   `json:"hasDataGoKey"`
	MasterDate     string `json:"masterDate"`      // 지금 급여목록의 적용일
	MasterCount    int    `json:"masterCount"`     // 급여목록 품목 수
	MasterSource   string `json:"masterSource"`    // 마지막으로 바꾼 곳 (파일 이름 또는 공공데이터포털)
	MasterFetched  string `json:"masterFetchedAt"` // 자동으로 마지막 받은 시각
	MasterFailedAt string `json:"masterFailedAt"`  // 자동 받기 마지막 실패 (성공하면 비움)
	MasterFailMsg  string `json:"masterFailMsg"`   // 그때 오류
}

func (s *Service) GetSettings() Settings {
	var stdCount, masterCount int
	var masterDate, masterSource string
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM gtin_map WHERE source = 'official'").Scan(&stdCount)
	_ = s.DB.QueryRow("SELECT COUNT(*), IFNULL(MAX(list_date), '') FROM drug_master WHERE "+sqlCurrentMaster).Scan(&masterCount, &masterDate)
	_ = s.DB.QueryRow(`SELECT b.filename FROM import_batch b WHERE b.id = (SELECT MAX(m.batch_id) FROM drug_master m WHERE m.in_list = 1)`).Scan(&masterSource)
	failedAt, failMsg := s.DB.GetSetting(setMasterFailedAt, ""), ""
	if failedAt != "" {
		_ = s.DB.QueryRow("SELECT IFNULL(message, '') FROM job_run WHERE job = 'drug_master' AND status = 'error' ORDER BY id DESC LIMIT 1").Scan(&failMsg)
	}
	return Settings{
		MasterCount: masterCount, MasterDate: masterDate, MasterSource: masterSource,
		DataDir: s.DataDir, DBPath: s.DB.Path, Version: AppVersion, StdCount: stdCount,
		BaselineDate:   s.DB.GetSetting(SetBaselineDate, ""),
		BaselineTiming: s.DB.GetSetting(SetBaselineTiming, "start"),
		CrawlEnabled:   s.DB.GetSetting(SetCrawlEnabled, "1") == "1",
		CrawlTimes:     s.DB.GetSetting(SetCrawlTimes, defaultSettings[SetCrawlTimes]),
		CrawlInclude:   s.DB.GetSetting(SetCrawlInclude, defaultSettings[SetCrawlInclude]),
		CrawlExclude:   s.DB.GetSetting(SetCrawlExclude, defaultSettings[SetCrawlExclude]),
		LastBackup:     s.DB.GetSetting(SetLastBackup, ""),
		OSTasks:        s.DB.GetSetting(SetOSTasks, "0") == "1",
		Zoom:           s.DB.GetSetting(SetZoom, "100"),
		Theme:          s.DB.GetSetting(SetTheme, defaultSettings[SetTheme]),
		CheckOnStart:   s.DB.GetSetting(SetCheckOnStart, "1") == "1",
		StdAuto:        s.DB.GetSetting(SetStdAuto, "1") == "1",
		StdDate:        s.DB.GetSetting(SetStdDate, ""),
		StdCheckedAt:   s.DB.GetSetting(SetStdChecked, ""),
		UpdateCheck:    s.DB.GetSetting(SetUpdateCheck, "1") == "1",
		UpdateRepo:     s.DB.GetSetting(SetUpdateRepo, DefaultUpdateRepo),
		HasUpdateToken: s.DB.GetSetting(SetUpdateToken, "") != "",
		UpdateChecked:  s.DB.GetSetting(SetUpdateChecked, ""),
		LogDebug:       s.DB.GetSetting(SetLogDebug, "0") == "1",
		Engine:         store.Engine(),
		MasterAuto:     s.DB.GetSetting(SetMasterAuto, "1") == "1",
		HasDataGoKey:   s.DB.GetSetting(SetDataGoKey, "") != "",
		MasterFetched:  s.DB.GetSetting(setMasterFetched, ""),
		MasterFailedAt: failedAt,
		MasterFailMsg:  failMsg,
	}
}

type SettingsInput struct {
	Zoom         string `json:"zoom"`
	Theme        string `json:"theme"`
	CrawlEnabled bool   `json:"crawlEnabled"`
	CrawlTimes   string `json:"crawlTimes"`
	CrawlInclude string `json:"crawlInclude"`
	CrawlExclude string `json:"crawlExclude"`
	CheckOnStart bool   `json:"checkOnStart"`
	StdAuto      bool   `json:"stdAuto"`
	UpdateCheck  bool   `json:"updateCheck"`
	UpdateRepo   string `json:"updateRepo"`
	UpdateToken  string `json:"updateToken"` // 빈 값이면 그대로 둠, "-" 면 지움
	LogDebug     bool   `json:"logDebug"`
	MasterAuto   bool   `json:"masterAuto"`
	DataGoKey    string `json:"dataGoKey"` // 빈 값이면 그대로 둠, "-" 면 지움
}

func (s *Service) SaveSettings(in SettingsInput) error {
	times, err := ParseCrawlTimes(in.CrawlTimes)
	if err != nil {
		return err
	}
	if strings.TrimSpace(in.CrawlInclude) == "" {
		return errors.New("수집 키워드(포함)는 비울 수 없습니다")
	}
	repo := strings.Trim(strings.TrimSpace(in.UpdateRepo), "/")
	repo = strings.TrimPrefix(strings.TrimPrefix(repo, "https://github.com/"), "github.com/")
	if repo == "" {
		repo = DefaultUpdateRepo
	}
	if strings.Count(repo, "/") != 1 {
		return errors.New("업데이트 저장소는 '소유자/저장소' 형식으로 적어주세요 (예: bullish-lee/dpr-pharmacy)")
	}
	theme := in.Theme
	if theme != "dark" && theme != "system" {
		theme = "light"
	}
	b := map[bool]string{true: "1", false: "0"}
	vals := map[string]string{
		SetCrawlEnabled: b[in.CrawlEnabled],
		SetCrawlTimes:   strings.Join(times, ","),
		SetCrawlInclude: strings.TrimSpace(in.CrawlInclude),
		SetCrawlExclude: strings.TrimSpace(in.CrawlExclude),
		SetZoom:         zoomOrDefault(in.Zoom),
		SetTheme:        theme,
		SetCheckOnStart: b[in.CheckOnStart],
		SetStdAuto:      b[in.StdAuto],
		SetUpdateCheck:  b[in.UpdateCheck],
		SetUpdateRepo:   repo,
	}
	switch tok := strings.TrimSpace(in.UpdateToken); tok {
	case "":
	case "-":
		vals[SetUpdateToken] = ""
	default:
		vals[SetUpdateToken] = tok
	}
	switch k := strings.TrimSpace(in.DataGoKey); k {
	case "":
	case "-":
		vals[SetDataGoKey] = ""
	default:
		vals[SetDataGoKey] = crawler.NormalizeServiceKey(k)
	}
	if v, ok := vals[SetDataGoKey]; ok && v != s.DB.GetSetting(SetDataGoKey, "") {
		// 인증키를 고쳤으면 실패 대기(6시간)·실패 알림을 지워 바로 다시 받게 한다
		vals[setMasterFailedAt], vals[setMasterFailNotified] = "", ""
	}
	vals[SetLogDebug] = b[in.LogDebug]
	vals[SetMasterAuto] = b[in.MasterAuto]
	var changed []string
	for k, v := range vals {
		if s.DB.GetSetting(k, "") != v {
			if k == SetUpdateToken || k == SetDataGoKey {
				changed = append(changed, k+"(값 숨김)")
			} else {
				changed = append(changed, k+"="+v)
			}
		}
		if err := s.DB.SetSetting(k, v); err != nil {
			return err
		}
	}
	applog.SetDebug(in.LogDebug)
	if len(changed) > 0 {
		sort.Strings(changed)
		s.Log.Info("설정 저장", "changed", strings.Join(changed, " "))
	}
	return nil
}

// ParseCrawlTimes 는 "8:30, 12:30,17:30" → ["08:30","12:30","17:30"] 로 검증·정규화한다.
func ParseCrawlTimes(s string) ([]string, error) {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		t, err := time.Parse("15:04", part)
		if err != nil {
			return nil, fmt.Errorf("시간 형식이 잘못되었습니다: %q (예: 08:30)", part)
		}
		out = append(out, t.Format("15:04"))
	}
	if len(out) == 0 {
		return nil, errors.New("수집 시간을 1개 이상 입력하세요")
	}
	return out, nil
}

// Backup 은 DB 스냅샷을 backups 폴더에 만들고 최근 30개만 남긴다.
// Backup 은 백업 폴더(설정에서 따로 정했으면 그곳, 아니면 데이터 폴더 안 backups)에 사본을 만들고 최근 30개만 남긴다.
// 따로 정한 폴더에 쓸 수 없으면(USB·네트워크가 빠짐) 기본 위치에 백업하고, 그 장애 동안 한 번만 알린다.
func (s *Service) Backup() (string, error) {
	dir, custom, why := s.backupDir()
	if why != nil {
		s.Log.Warn("백업 폴더 대신 기본 위치에 백업", "err", why)
		if s.DB.GetSetting(setBackupFailNotified, "") == "" {
			_ = s.DB.SetSetting(setBackupFailNotified, store.Now())
			s.notify("warn", "백업 폴더에 쓸 수 없습니다", why.Error()+" — 드라이브가 연결되면 다시 그 폴더에 백업합니다.", "settings")
		}
	} else if custom {
		_ = s.DB.SetSetting(setBackupFailNotified, "") // 다시 쓸 수 있게 됨 → 다음 장애 때 다시 알림
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("백업 실패: %w", err)
	}
	stamp := "dpr-" + s.NowKST().Format("20060102-150405")
	dest := filepath.Join(dir, stamp+".db")
	for i := 2; ; i++ { // 같은 초에 두 번 백업하면 이름이 겹친다 (VACUUM INTO 는 있는 파일에 쓰지 않음)
		_, err := os.Stat(dest)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil { // 드라이브가 빠짐·권한 없음 — 이름을 바꿔도 소용없다 (계속 돌지 않게)
			return "", fmt.Errorf("백업 실패: 백업 폴더를 확인할 수 없습니다: %w", err)
		}
		if i > 100 {
			return "", errors.New("백업 실패: 같은 시각 백업 파일이 너무 많습니다")
		}
		dest = filepath.Join(dir, fmt.Sprintf("%s-%d.db", stamp, i))
	}
	if err := s.DB.Backup(dest); err != nil {
		return "", fmt.Errorf("백업 실패: %w", err)
	}
	_ = s.DB.SetSetting(SetLastBackup, store.Now())
	entries, _ := os.ReadDir(dir)
	var files []string
	for _, e := range entries {
		if isBackupFile(e.Name()) {
			files = append(files, e.Name())
		}
	}
	for len(files) > 30 { // 이름이 시각 순이라 앞쪽이 오래된 것
		_ = os.Remove(filepath.Join(dir, files[0]))
		files = files[1:]
	}
	return dest, nil
}

// AutoBackupIfDue 는 하루 1회 자동 백업.
func (s *Service) AutoBackupIfDue() {
	last := s.DB.GetSetting(SetLastBackup, "")
	if t, err := time.Parse(time.RFC3339, last); err == nil && time.Since(t) < 20*time.Hour {
		return
	}
	if _, err := s.Backup(); err != nil {
		s.Log.Warn("자동 백업 실패", "err", err)
	}
}

// ZoomSteps 는 화면 배율 단계 (사이드바 A−/A+, Ctrl +/−)
var ZoomSteps = []string{"87.5", "100", "112.5", "125", "137.5", "150"}

func zoomOrDefault(z string) string {
	for _, v := range ZoomSteps {
		if z == v {
			return z
		}
	}
	return "100"
}

// SetUIPrefs 는 화면 배율·테마만 저장한다 (사이드바에서 바로 바꿀 때).
func (s *Service) SetUIPrefs(zoom, theme string) error {
	if theme != "dark" && theme != "system" {
		theme = "light"
	}
	if err := s.DB.SetSetting(SetZoom, zoomOrDefault(zoom)); err != nil {
		return err
	}
	return s.DB.SetSetting(SetTheme, theme)
}
