package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"dpr/internal/applog"
	"dpr/internal/domain"
	"dpr/internal/ingest"
	"dpr/internal/platform"
	"dpr/internal/service"
	"dpr/internal/updater"
)

// App 은 화면(React)에 노출되는 얇은 바인딩 계층이다. 모든 로직은 internal/service 에 있다.
// 화면에서는 frontend/wailsjs/go/main/App 의 같은 이름 함수로 호출한다.
type App struct {
	ctx    context.Context
	cancel context.CancelFunc
	svc    *service.Service
	log    *slog.Logger
	phone  phoneHub   // 휴대폰 연결 (phone.go)
	relay  relayState // 외부 중계 — 모바일 데이터·약국 밖 (relay.go)
}

func NewApp(svc *service.Service, log *slog.Logger) *App { return &App{svc: svc, log: log} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	bg, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.svc.OnNotify = func(n service.Notification) { runtime.EventsEmit(a.ctx, "notify", n) }
	a.svc.AsyncRematch = true // 가져오기 응답을 먼저 돌려주고 이름 재매칭은 뒤에서
	a.svc.OnDataChanged = func(what string) { runtime.EventsEmit(a.ctx, "data-changed", what) }
	if exe, err := os.Executable(); err == nil {
		if fresh := updater.ConsumeUpdateMarker(exe); justUpdated || fresh {
			a.log.Info("업데이트 후 첫 실행 — 이전 버전(DPR.exe.old)은 다음에 켤 때 지웁니다 (문제가 있으면 되돌릴 수 있게)")
		} else {
			updater.CleanupOld(exe) // 지난 업데이트가 남긴 DPR.exe.old (업데이트 뒤 한 번 정상으로 켜진 다음)
		}
	}
	service.OnStartupCheck = func(ctx context.Context) { // 켤 때 확인: 새 버전
		if info, err := a.svc.CheckUpdate(ctx, false); err == nil && info.Available {
			runtime.EventsEmit(a.ctx, "update-available", info)
		} else if err != nil {
			a.log.Warn("업데이트 확인 실패", "err", err)
		}
	}
	a.applyWindowTheme(a.svc.GetSettings().Theme)
	a.svc.StartScheduler(bg)
	a.phoneAutoStart()
}

// SetUIPrefs : 글자 크기·테마를 바로 바꾼다 (창 테두리·배경색도 함께)
func (a *App) SetUIPrefs(zoom, theme string) error {
	if err := a.svc.SetUIPrefs(zoom, theme); err != nil {
		return err
	}
	a.applyWindowTheme(theme)
	return nil
}

func (a *App) applyWindowTheme(theme string) {
	if a.ctx == nil {
		return
	}
	switch theme {
	case "dark":
		runtime.WindowSetDarkTheme(a.ctx)
		runtime.WindowSetBackgroundColour(a.ctx, 15, 19, 24, 255)
	case "system":
		runtime.WindowSetSystemDefaultTheme(a.ctx)
	default:
		runtime.WindowSetLightTheme(a.ctx)
		runtime.WindowSetBackgroundColour(a.ctx, 247, 248, 250, 255)
	}
}

// ── 기록 · 오류 보고 ──────────────────────────────────────────────

// LogClientError : 화면(React)에서 난 오류·실패한 작업을 기록에 남긴다.
func (a *App) LogClientError(kind, message, detail, route string) {
	a.log.Error("화면 오류", "kind", firstRunes(kind, 40), "msg", firstRunes(message, 1000), "route", firstRunes(route, 200), "detail", firstRunes(detail, 4000))
}

// firstRunes : 글자 단위로 자른다 (바이트로 자르면 한글이 깨진다)
func firstRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ReadLogs : 기록 파일 끝부분 (file: dpr = 앱, dpr-cli = 작업 스케줄러)
func (a *App) ReadLogs(file string, n int, minLevel string) ([]applog.Line, error) {
	if file != "dpr-cli" {
		file = "dpr"
	}
	return applog.ReadTail(a.svc.DataDir, file, n, minLevel)
}

// OpenLogFolder : 기록 폴더 열기
func (a *App) OpenLogFolder() error { return platform.OpenFolder(filepath.Join(a.svc.DataDir, "logs")) }

// SubmitReport : 오류 보고 (토큰이 있으면 바로 이슈, 없으면 브라우저로 새 이슈 창)
func (a *App) SubmitReport(note string) (*service.ReportResult, error) {
	res, err := a.svc.SubmitReport(a.ctx, note)
	if err != nil {
		return nil, err
	}
	if res.OpenURL != "" {
		runtime.BrowserOpenURL(a.ctx, res.OpenURL)
		_ = platform.OpenFolder(filepath.Dir(res.Path))
	}
	return res, nil
}

// ── 업데이트 ──────────────────────────────────────────────────────

func (a *App) CheckUpdate(force bool) (*service.UpdateInfo, error) {
	return a.svc.CheckUpdate(a.ctx, force)
}

// ApplyUpdate 는 새 버전(version: 화면에서 확인한 버전)으로 바꾼 뒤 새 프로그램을 띄우고 지금 창을 닫는다.
// 새 프로그램은 --after-update=<지금 PID> 를 받아 이 프로세스가 끝날 때까지 기다린 뒤 시작한다 (한 번에 하나만 실행 규칙 때문).
func (a *App) ApplyUpdate(version string) (*service.UpdateInfo, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	info, err := a.svc.ApplyUpdate(a.ctx, exe, version)
	if err != nil {
		return nil, err
	}
	if err := a.restart("--after-update"); err != nil {
		return info, fmt.Errorf("새 버전으로 바꿨지만 다시 시작하지 못했습니다 — 프로그램을 직접 다시 켜주세요: %w", err)
	}
	return info, nil
}

// restart 는 새 프로세스를 띄우고(flag=<지금 PID> — 새 프로세스는 이 프로세스가 끝날 때까지 기다린다) 지금 창을 닫는다.
func (a *App) restart(flag string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, fmt.Sprintf("%s=%d", flag, os.Getpid()))
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		time.Sleep(400 * time.Millisecond)
		runtime.Quit(a.ctx)
	}()
	return nil
}

// ── 데이터·백업 폴더 ──────────────────────────────────────────────

func (a *App) DataDirInfo() service.DataDirInfo { return a.svc.DataDirInfo() }

// PickFolder 는 OS 폴더 선택 창을 연다 (취소하면 "")
func (a *App) PickFolder(title string) (string, error) {
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: title, CanCreateDirectories: true})
}

func (a *App) CheckFolder(path string) service.FolderCheck { return a.svc.CheckFolder(path) }

// MoveDataDir : 지금 자료를 빈 폴더로 옮기고 다시 시작한다
func (a *App) MoveDataDir(path string) (string, error) {
	msg, err := a.svc.MoveDataDir(path)
	if err != nil {
		return "", err
	}
	if err := a.restart("--restart-after"); err != nil {
		return "", a.cancelSwitch(err)
	}
	return msg, nil
}

// UseDataDir : 이미 있는 자료 폴더로 바꾸고 다시 시작한다
func (a *App) UseDataDir(path string) (string, error) {
	msg, err := a.svc.UseDataDir(path)
	if err != nil {
		return "", err
	}
	if err := a.restart("--restart-after"); err != nil {
		return "", a.cancelSwitch(err)
	}
	return msg, nil
}

// cancelSwitch : 폴더를 바꿨지만 다시 시작하지 못함 — 위치 기록을 되돌려 이 창과 다음 창이 같은 자료를 쓰게 한다
func (a *App) cancelSwitch(restartErr error) error {
	if err := a.svc.CancelDataDirSwitch(restartErr.Error()); err != nil {
		return fmt.Errorf("다시 시작하지 못했고 되돌리지도 못했습니다 — 프로그램을 닫았다가 직접 다시 켜 주세요 (새 폴더로 열립니다): %v", restartErr)
	}
	return fmt.Errorf("다시 시작하지 못해 바꾸기를 취소했습니다 — 지금 폴더를 그대로 씁니다 (새 폴더에 만든 사본은 지워도 됩니다): %w", restartErr)
}

func (a *App) SetBackupFolder(path string) error { return a.svc.SetBackupFolder(path) }

func (a *App) OpenBackupFolder() error {
	dir := a.svc.DataDirInfo().BackupDir
	_ = os.MkdirAll(dir, 0o755)
	return platform.OpenFolder(dir)
}

func (a *App) shutdown(ctx context.Context) {
	if a.cancel != nil {
		a.cancel()
	}
	a.stopPhone()
}

func (a *App) onSecondInstance(_ options.SecondInstanceData) {
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

// ── 앱 정보 · 설정 ────────────────────────────────────────────────

type AppInfo struct {
	Version    string `json:"version"`
	DataDir    string `json:"dataDir"`
	IsWindows  bool   `json:"isWindows"`
	StartRoute string `json:"startRoute"` // 개발·지원용: DPR_START_ROUTE 로 시작 화면 지정
}

func (a *App) AppInfo() AppInfo {
	return AppInfo{Version: service.AppVersion, DataDir: a.svc.DataDir, IsWindows: platform.IsWindows(), StartRoute: os.Getenv("DPR_START_ROUTE")}
}
func (a *App) GetSettings() service.Settings               { return a.svc.GetSettings() }
func (a *App) SaveSettings(in service.SettingsInput) error { return a.svc.SaveSettings(in) }
func (a *App) Backup() (string, error)                     { return a.svc.Backup() }
func (a *App) OpenDataFolder() error                       { return platform.OpenFolder(a.svc.DataDir) }
func (a *App) Notifications(limit int) ([]service.Notification, error) {
	return a.svc.Notifications(limit)
}
func (a *App) MarkNotificationsRead() error { return a.svc.MarkNotificationsRead() }

// InstallWindowsTasks 는 창이 닫혀 있어도 수집되도록 작업 스케줄러에 등록한다.
func (a *App) InstallWindowsTasks() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	times, err := service.ParseCrawlTimes(a.svc.GetSettings().CrawlTimes)
	if err != nil {
		return "", err
	}
	msg, err := platform.InstallCrawlTasks(exe, times)
	if err == nil {
		_ = a.svc.DB.SetSetting(service.SetOSTasks, "1")
	}
	return msg, err
}

func (a *App) RemoveWindowsTasks() error {
	err := platform.RemoveCrawlTasks()
	if err == nil {
		_ = a.svc.DB.SetSetting(service.SetOSTasks, "0")
	}
	return err
}

// ── 대시보드 · 분석 ───────────────────────────────────────────────

func (a *App) Dashboard() (*service.Dashboard, error)    { return a.svc.Dashboard() }
func (a *App) PriceCutDates() ([]service.CutDate, error) { return a.svc.PriceCutDates() }
func (a *App) Ranking(eff string, heldOnly bool) (*service.Ranking, error) {
	return a.svc.Ranking(eff, heldOnly)
}
func (a *App) ProductDetail(eff, code string) (*service.ProductDetail, error) {
	return a.svc.ProductDetail(eff, code)
}
func (a *App) HeldDeletions(eff string) ([]service.DeletionRow, error) {
	return a.svc.HeldDeletions(eff)
}
func (a *App) SetWindowOverride(eff string, supplierID int64, from, to, memo string) error {
	return a.svc.SetWindowOverride(eff, supplierID, from, to, memo)
}
func (a *App) ClearWindowOverride(eff string, supplierID int64) error {
	return a.svc.ClearWindowOverride(eff, supplierID)
}

// ── 재고 ──────────────────────────────────────────────────────────

func (a *App) Inventory(query string, onlyNonZero bool) (*service.InventoryView, error) {
	return a.svc.Inventory(query, onlyNonZero)
}
func (a *App) SaveCount(in service.CountInput) (int64, error) { return a.svc.SaveCount(in) }
func (a *App) UnitOptions(code string) []service.UnitOption   { return a.svc.UnitOptions(code) }
func (a *App) SetUnitFactor(code string, factor float64, note, source string) error {
	return a.svc.SetUnitFactor(code, factor, note, source)
}
func (a *App) SetPackSize(code string, size float64, note, source string) error {
	return a.svc.SetPackSize(code, size, note, source)
}

// ── 수정 기록 · 데이터 점검 확인 ─────────────────────────────────

func (a *App) ChangeLog(f service.ChangeFilter) ([]service.ChangeEntry, error) {
	return a.svc.ChangeLog(f)
}
func (a *App) AckHealth(code, flag, note, source string) error {
	return a.svc.AckHealth(code, flag, note, source)
}
func (a *App) UnackHealth(code, flag, source string) error {
	return a.svc.UnackHealth(code, flag, source)
}
func (a *App) DeleteAnchor(id int64) error { return a.svc.DeleteAnchor(id) }
func (a *App) ProductAnchors(code string) ([]service.AnchorView, error) {
	return a.svc.ProductAnchors(code)
}
func (a *App) SetBaseline(date, timing string) error { return a.svc.SetBaseline(date, timing) }
func (a *App) SearchProducts(q string) ([]service.Candidate, error) {
	return a.svc.SearchProducts(q, 30)
}

// ── 약품 조회 (코드·이름·바코드·주성분코드) ──────────────────────

func (a *App) LookupDrugs(q string) ([]service.DrugHit, error)   { return a.svc.LookupDrugs(q) }
func (a *App) DrugInfo(code string) (*service.DrugDetail, error) { return a.svc.DrugInfo(code) }

// ── 가져오기 ──────────────────────────────────────────────────────

type ImportKind struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Help  string `json:"help"`
}

func (a *App) ImportKinds() []ImportKind {
	out := make([]ImportKind, 0, len(service.ImportKinds))
	for _, k := range service.ImportKinds {
		out = append(out, ImportKind{k.Kind, k.Label, k.Help})
	}
	return out
}
func (a *App) ImportSchema(kind string) ingest.Schema {
	s, _ := ingest.SchemaByKind(kind)
	return s
}
func (a *App) PreviewImport(req service.ImportRequest) (*service.ImportPreview, error) {
	return a.svc.PreviewImport(req)
}
func (a *App) Import(req service.ImportRequest) (*service.ImportResult, error) {
	res, err := a.svc.Import(req)
	if err == nil && !res.NeedsMapping {
		runtime.EventsEmit(a.ctx, "data-changed", req.Kind)
	}
	return res, err
}
func (a *App) ListImports() ([]service.BatchView, error)        { return a.svc.ListImports(100) }
func (a *App) DeleteImport(id int64) error                      { return a.svc.DeleteImport(id) }
func (a *App) ListUnresolved() ([]service.UnresolvedRow, error) { return a.svc.ListUnresolved() }
func (a *App) ResolveUnresolved(id int64, code string) error {
	return a.svc.ResolveUnresolved(id, code)
}
func (a *App) ResolveUnresolvedByCode(id int64, code string) error {
	return a.svc.ResolveUnresolvedByCode(id, code)
}
func (a *App) CheckProductCode(code string) (*service.ProductCodeInfo, error) {
	return a.svc.CheckProductCode(code)
}
func (a *App) DefaultBaselineDate() string { return a.svc.DefaultBaselineDate() }

// PickFile 은 OS 파일 선택 창을 연다 (엑셀/CSV).
func (a *App) PickFile(title string) (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: title, Filters: []runtime.FileFilter{
		{DisplayName: "엑셀/CSV (*.xlsx, *.csv)", Pattern: "*.xlsx;*.xlsm;*.csv"},
	}})
}

// ── 고시 · 수집 ───────────────────────────────────────────────────

func (a *App) ListNotices() ([]service.NoticeView, error) { return a.svc.ListNotices() }
func (a *App) NoticeChanges(id int64, group string, heldOnly bool) ([]service.ChangeRow, error) {
	return a.svc.NoticeChanges(id, group, heldOnly)
}
func (a *App) DeleteNotice(id int64) error { return a.svc.DeleteNotice(id) }
func (a *App) RunCrawl() (*service.CrawlResult, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute) // 시간 한도는 작업 기록의 '멈춤' 판단(jobStale)보다 짧게
	defer cancel()
	res, err := a.svc.RunCrawl(ctx)
	if err == nil && res.New > 0 {
		runtime.EventsEmit(a.ctx, "data-changed", "notice")
	}
	return res, err
}
func (a *App) CrawlStatus() service.CrawlStatus { return a.svc.CrawlStatus() }

// ImportNoticePost : 복지부 게시글 주소(또는 번호)로 지난 고시 하나 가져오기 (사용자가 요청할 때만)
func (a *App) ImportNoticePost(link string) (*service.NoticePostResult, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Minute)
	defer cancel()
	return a.svc.ImportNoticePost(ctx, link)
}

// CheckStdCode : 표준코드 새 판 확인 (force=true: 오늘 확인했어도 다시)
func (a *App) CheckStdCode(force bool) (*service.StdCodeResult, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Minute)
	defer cancel()
	res, err := a.svc.CheckStdCode(ctx, force)
	if err == nil && res.Updated {
		runtime.EventsEmit(a.ctx, "data-changed", "gtin")
	}
	return res, err
}

// VerifyDataGoKey : 공공데이터포털 인증키 확인 (저장하지 않음)
func (a *App) VerifyDataGoKey(key string) service.KeyCheck {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return a.svc.VerifyDataGoKey(ctx, key)
}

// SetDataGoKey : 공공데이터포털 인증키 저장 ("" = 지움)
func (a *App) SetDataGoKey(key string) error { return a.svc.SetDataGoKey(key) }

// CheckDrugMaster : 약제급여목록 전체를 공공데이터포털에서 받기 (force=true: 이번 달에 받았어도 다시)
func (a *App) CheckDrugMaster(force bool) (*service.MasterResult, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
	defer cancel()
	res, err := a.svc.CheckDrugMaster(ctx, force)
	if err == nil && res.Updated {
		runtime.EventsEmit(a.ctx, "data-changed", "drug_master")
	}
	return res, err
}
func (a *App) JobRuns() ([]service.JobRun, error) { return a.svc.JobRuns(30) }
func (a *App) OpenURL(url string)                 { runtime.BrowserOpenURL(a.ctx, url) }

// ── 도매처 · 보상기준 ─────────────────────────────────────────────

func (a *App) ListSuppliers(includeDeleted bool) ([]service.SupplierView, error) {
	return a.svc.ListSuppliers(includeDeleted)
}
func (a *App) SaveSupplier(in service.SupplierInput) (int64, error) { return a.svc.SaveSupplier(in) }
func (a *App) DeleteSupplier(id int64) (string, error)              { return a.svc.DeleteSupplier(id) }
func (a *App) RestoreSupplier(id int64) error                       { return a.svc.RestoreSupplier(id) }
func (a *App) MergeSupplier(fromID, intoID int64) error             { return a.svc.MergeSupplier(fromID, intoID) }
func (a *App) ListRules(supplierID int64) ([]service.RuleView, error) {
	return a.svc.ListRules(supplierID)
}
func (a *App) SaveRules(in []service.RuleInput) error { return a.svc.SaveRules(in) }
func (a *App) ApplyRuleToSuppliers(t service.RuleInput, ids []int64) error {
	return a.svc.ApplyRuleToSuppliers(t, ids)
}
func (a *App) DeleteRule(id int64) error { return a.svc.DeleteRule(id) }

type RulePreview struct {
	Description    string `json:"description"`
	WindowFrom     string `json:"windowFrom"`
	WindowTo       string `json:"windowTo"`
	SettlementDate string `json:"settlementDate"`
	Error          string `json:"error"`
}

// PreviewRule 은 규칙을 저장하기 전에 '이 시행일이면 대상기간이 언제인지' 미리 보여준다.
func (a *App) PreviewRule(rule domain.CompRule, eff string) RulePreview {
	if err := rule.Validate(); err != nil {
		return RulePreview{Error: err.Error()}
	}
	t, err := domain.ParseDate(eff)
	if err != nil {
		t, _ = domain.ParseDate(a.svc.Today())
	}
	f, to := domain.CompWindow(rule, t, nil)
	return RulePreview{Description: rule.Describe(), WindowFrom: domain.FormatDate(f), WindowTo: domain.FormatDate(to),
		SettlementDate: domain.FormatDate(domain.NextSettlementDate(t, rule.SettlementDay))}
}

// ── 바코드 · 사입 근거 ────────────────────────────────────────────

func (a *App) RecordScan(in service.ScanInput) (*service.ScanResult, error) {
	return a.svc.RecordScan(in)
}
func (a *App) ListScans(eff, code string) ([]service.ScanView, error) {
	return a.svc.ListScans(eff, code)
}
func (a *App) ConfirmGTIN(gtin, code string) error    { return a.svc.ConfirmGTIN(gtin, code) }
func (a *App) ResolveScan(id, supplierID int64) error { return a.svc.ResolveScan(id, supplierID) }
func (a *App) SetScanRegistered(id int64, registered bool) error {
	return a.svc.SetScanRegistered(id, registered)
}
func (a *App) SetScanQty(id int64, qty float64) error { return a.svc.SetScanQty(id, qty) }
func (a *App) DeleteScan(id int64) error              { return a.svc.DeleteScan(id) }
func (a *App) ParseBarcode(raw string) *domain.GS1 {
	g, ok := domain.ParseGS1(raw)
	if !ok {
		return nil
	}
	return &g
}
func (a *App) ListLots(code string) ([]service.LotView, error) { return a.svc.ListLots(code) }
func (a *App) AddLot(in service.LotInput) (int64, error)       { return a.svc.AddLot(in) }
func (a *App) DeleteLot(id int64) error                        { return a.svc.DeleteLot(id) }

// CheckOCR : 설정 화면 '글자 인식 점검' — 점검 그림으로 이 PC 에서 글자 인식이 되는지 본다
func (a *App) CheckOCR() platform.OCRCheck {
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	c := platform.OCRSelfTest(ctx)
	a.log.Info("글자 인식 점검", "ok", c.OK, "engine", c.Engine, "found", c.Found, "ms", c.Ms, "problem", c.Problem)
	return c
}

// ImportNoticePosts : 게시글 주소·번호 여러 개(줄바꿈으로 나눔)를 차례로 가져오기 — 지난 1년 고시를 한 번에
func (a *App) ImportNoticePosts(text string) (*service.NoticePostsResult, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
	defer cancel()
	res, err := a.svc.ImportNoticePosts(ctx, service.SplitPostInputs(text))
	if res != nil && res.Imported > 0 {
		runtime.EventsEmit(a.ctx, "data-changed", "notice")
	}
	return res, err
}

// ── 사입정보 사진 판독 (회차 3단계) ─────────────────────────────

// ReadLotPhoto : 사입정보 사진 1장을 Windows 기본 OCR(무료·오프라인)로 판독한다 (저장하지 않음 — 화면에서 확인·수정한 뒤 SaveLotPhotoRows)
func (a *App) ReadLotPhoto(req service.LotPhotoRequest) (*service.LotPhotoResult, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	return a.svc.ReadLotPhoto(ctx, req)
}
func (a *App) SaveLotPhotoRows(rows []service.LotInput) (int, error) { return a.svc.AddLots(rows) }
func (a *App) UpdateLot(id int64, in service.LotInput) error         { return a.svc.UpdateLot(id, in) }
func (a *App) ListLotEvidence(codes []string) ([]service.LotEvidenceView, error) {
	return a.svc.ListLotEvidence(codes)
}

// ── 처리 결과 · 리포트 ────────────────────────────────────────────

func (a *App) ConfirmDecision(eff, code string) (int, error) { return a.svc.ConfirmDecision(eff, code) }
func (a *App) ListSettlements(f service.SettlementFilter) ([]service.SettlementView, error) {
	return a.svc.ListSettlements(f)
}
func (a *App) UpdateSettlement(in service.SettlementInput) error { return a.svc.UpdateSettlement(in) }
func (a *App) FinishPhysical(eff, code string, supplierID int64) (*service.SettlementView, error) {
	return a.svc.FinishPhysical(eff, code, supplierID)
}
func (a *App) SetSettlementStatus(id int64, status string) error {
	return a.svc.SetSettlementStatus(id, status)
}
func (a *App) DeleteSettlement(id int64) error                            { return a.svc.DeleteSettlement(id) }
func (a *App) Report(f service.SettlementFilter) (*service.Report, error) { return a.svc.Report(f) }

// ExportReportCSV 는 처리 내역을 CSV(엑셀용 UTF-8 BOM)로 저장한다. 저장 위치는 OS 대화상자로 고른다.
func (a *App) ExportReportCSV(f service.SettlementFilter) (string, error) {
	rows, err := a.svc.ListSettlements(f)
	if err != nil {
		return "", err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "처리 내역 저장",
		DefaultFilename: "약가인하_처리내역_" + a.svc.Today() + ".csv",
		Filters:         []runtime.FileFilter{{DisplayName: "CSV (*.csv)", Pattern: "*.csv"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	var b strings.Builder
	b.WriteString("\ufeff" + "시행일,약품코드,약품명,도매,방식,수량,1단위 인하폭,보상액,상태,완료시각,메모\n")
	esc := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	for _, r := range rows {
		method := map[string]string{"document": "서류보상", "physical": "실물보상"}[r.Method]
		status := map[string]string{"done": "완료", "planned": "예정"}[r.Status]
		fmt.Fprintf(&b, "%s,%s,%s,%s,%s,%s,%.1f,%.0f,%s,%s,%s\n", r.EffectiveDate, r.Code, esc(r.Name), esc(r.SupplierName), method,
			r.Qty.String(), r.UnitDelta, r.Amount, status, r.DoneAt, esc(r.Memo))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
