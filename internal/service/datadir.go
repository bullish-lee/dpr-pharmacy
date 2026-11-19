package service

// 데이터 폴더 · 백업 폴더 — 위치 확인과 옮기기 (설정 → 데이터·백업)
//
// 데이터 폴더 = dpr.db + logs(기록) + raw(받은 원본) + reports(오류 보고서) + backups(기본 백업 위치).
// 옮긴 위치는 기본 위치 옆 DPR-data-location.txt 에 적는다(platform) — 앱과 작업 스케줄러가 같은 곳을 찾고,
// 원래 폴더를 지워도 기록은 남는다.
//
// 옮기기 순서: 작업이 없을 때만 → 기록·원본·보고서 복사(오래 걸림) → 수정 기록 → 자료 사본(VACUUM INTO, 마지막에)
// → 새 사본 확인 → 그동안 다른 연결(작업 스케줄러·자동 작업)이 자료를 바꿨으면 취소 → 위치 기록 → 다시 시작.
// 백업(30개)은 옮기지 않는다 — 새 폴더에서 새로 쌓인다. 원래 폴더는 지우지 않는다.
// 네트워크 폴더는 데이터 폴더로 쓰지 않는다 (SQLite 쓰기 기록(WAL)이 네트워크에서는 안전하지 않음, PC 두 대가 같이 쓰면 손상).

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dpr/internal/platform"
	"dpr/internal/store"
)

const (
	SetBackupDir          = "backup_dir"
	setBackupFailNotified = "backup_dir_fail_notified"
)

type DataDirInfo struct {
	Dir          string `json:"dir"`
	Source       string `json:"source"` // env | portable | custom | default
	SourceLabel  string `json:"sourceLabel"`
	CanMove      bool   `json:"canMove"`
	MoveNote     string `json:"moveNote"` // 옮길 수 없는 이유
	DefaultDir   string `json:"defaultDir"`
	LocationFile string `json:"locationFile"` // 옮긴 경우 위치를 적은 파일 (지우면 기본 위치로)
	LogDir       string `json:"logDir"`
	BackupDir    string `json:"backupDir"`
	BackupCustom bool   `json:"backupCustom"`
	BackupNote   string `json:"backupNote"` // 백업 폴더에 쓸 수 없을 때
	DBSize       int64  `json:"dbSize"`     // dpr.db + 쓰기 기록(-wal)
	BackupCount  int    `json:"backupCount"`
	LastBackup   string `json:"lastBackup"`
}

func sourceLabel(src string) string {
	switch src {
	case platform.SourceEnv:
		return "환경변수 DPR_DATA_DIR 로 지정 (개발·지원용)"
	case platform.SourcePortable:
		return "휴대용 — DPR.exe 옆 dpr-data 폴더"
	case platform.SourceCustom:
		return "옮긴 위치"
	case platform.SourceDefault:
		return "기본 위치"
	}
	return "지정됨"
}

func (s *Service) canMoveDataDir() (bool, string) {
	switch s.DataDirSource {
	case platform.SourceDefault, platform.SourceCustom:
		return true, ""
	case platform.SourcePortable:
		return false, "휴대용 모드입니다 — 옮기려면 프로그램을 끄고 DPR.exe 와 dpr-data 폴더를 함께 옮기세요"
	case platform.SourceEnv:
		return false, "환경변수 DPR_DATA_DIR 로 지정되어 있어 화면에서 바꿀 수 없습니다"
	}
	return false, "이 실행 방식에서는 바꿀 수 없습니다"
}

// DataDirInfo : 데이터·기록·백업 폴더 위치와 크기
func (s *Service) DataDirInfo() DataDirInfo {
	info := DataDirInfo{Dir: s.DataDir, Source: s.DataDirSource, SourceLabel: sourceLabel(s.DataDirSource),
		DefaultDir: platform.DefaultDataDir(), LogDir: filepath.Join(s.DataDir, "logs"), LastBackup: s.DB.GetSetting(SetLastBackup, "")}
	info.CanMove, info.MoveNote = s.canMoveDataDir()
	if s.DataDirSource == platform.SourceCustom {
		info.LocationFile = platform.LocationFileInUse()
	}
	for _, f := range []string{"dpr.db", "dpr.db-wal"} {
		if st, err := os.Stat(filepath.Join(s.DataDir, f)); err == nil {
			info.DBSize += st.Size()
		}
	}
	dir, custom, err := s.backupDir()
	info.BackupDir, info.BackupCustom = dir, custom
	if err != nil {
		info.BackupNote = err.Error()
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if isBackupFile(e.Name()) {
				info.BackupCount++
			}
		}
	}
	return info
}

func isBackupFile(name string) bool {
	return strings.HasPrefix(name, "dpr-") && strings.HasSuffix(name, ".db")
}

// backupDir : 백업 폴더. 따로 정한 폴더에 쓸 수 없으면(드라이브가 빠짐 등) 데이터 폴더 안 backups 와 그 이유.
func (s *Service) backupDir() (dir string, custom bool, err error) {
	def := filepath.Join(s.DataDir, "backups")
	c := strings.TrimSpace(s.DB.GetSetting(SetBackupDir, ""))
	if c == "" {
		return def, false, nil
	}
	if werr := checkWritable(c); werr != nil {
		return def, false, fmt.Errorf("백업 폴더(%s)에 쓸 수 없어 기본 위치에 백업합니다: %v", c, werr)
	}
	return c, true, nil
}

// checkWritable : 폴더에 파일을 만들고 지워 본다
func checkWritable(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return errors.New("폴더를 찾을 수 없음")
	}
	if !st.IsDir() {
		return errors.New("폴더가 아님")
	}
	f, err := os.CreateTemp(dir, ".dpr-write-test-*")
	if err != nil {
		return errors.New("쓰기 권한이 없음")
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// SetBackupFolder : 백업 폴더를 따로 정한다 ("" = 기본: 데이터 폴더 안 backups). 수정 기록에 남긴다.
func (s *Service) SetBackupFolder(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		dir = abs
		if err := checkWritable(dir); err != nil {
			return fmt.Errorf("이 폴더에 백업할 수 없습니다 (%s): %v", dir, err)
		}
	}
	old := s.DB.GetSetting(SetBackupDir, "")
	if old == dir {
		return nil
	}
	label := func(v string) string {
		if v == "" {
			return "기본 (데이터 폴더 안 backups)"
		}
		return v
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO app_setting(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", SetBackupDir, dir); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM app_setting WHERE key = ?", setBackupFailNotified); err != nil {
			return err
		}
		return logChange(tx, ChangeEntry{Kind: "backup_dir", OldValue: label(old), NewValue: label(dir), Note: "백업 폴더", Source: "설정"})
	})
}

// ── 데이터 폴더 옮기기 ────────────────────────────────────────────

// FolderCheck : 고른 폴더를 미리 살펴본 결과 (화면이 어떤 확인 창을 띄울지 정한다)
type FolderCheck struct {
	Path      string `json:"path"`
	Empty     bool   `json:"empty"`     // 비어 있음 → 지금 자료를 옮길 수 있음
	HasData   bool   `json:"hasData"`   // 급여약품 관리 자료(dpr.db)가 있음 → 그 자료로 바꿀 수 있음
	Newer     bool   `json:"newer"`     // 더 새 버전이 만든 자료 (바꿀 수 없음)
	Same      bool   `json:"same"`      // 지금 쓰는 폴더
	Writable  bool   `json:"writable"`  // 쓸 수 있음
	LastWrite string `json:"lastWrite"` // 그 자료의 마지막 수정 시각
	Note      string `json:"note"`
}

// ignorable : 빈 폴더로 봐도 되는 OS 파일
func ignorable(name string) bool {
	switch strings.ToLower(name) {
	case ".ds_store", "desktop.ini", "thumbs.db", dataLocationName:
		return true
	}
	return strings.HasPrefix(name, ".dpr-write-test-")
}

const dataLocationName = "data-location.txt"

// CheckFolder : 데이터 폴더로 쓸 폴더를 살펴본다
func (s *Service) CheckFolder(dir string) FolderCheck {
	fc := FolderCheck{}
	abs, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil || strings.TrimSpace(dir) == "" {
		fc.Note = "폴더를 고르세요"
		return fc
	}
	fc.Path = abs
	if platform.IsNetworkPath(strings.TrimSpace(dir)) || platform.IsNetworkPath(abs) {
		fc.Note = "네트워크 폴더는 데이터 폴더로 쓸 수 없습니다 (네트워크에서는 자료가 손상될 수 있음) — 이 PC의 디스크나 USB를 고르세요. 백업 폴더로는 쓸 수 있습니다."
		return fc
	}
	if samePath(abs, s.DataDir) {
		fc.Same, fc.Note = true, "지금 쓰고 있는 데이터 폴더입니다"
		return fc
	}
	if within(abs, s.DataDir) || within(s.DataDir, abs) {
		fc.Note = "지금 데이터 폴더의 안이나 바깥(상위) 폴더는 고를 수 없습니다 — 따로 새 폴더를 만들어 고르세요"
		return fc
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		fc.Note = "폴더를 열 수 없습니다"
		return fc
	}
	fc.Writable = checkWritable(abs) == nil
	empty := true
	for _, e := range entries {
		if !ignorable(e.Name()) {
			empty = false
		}
	}
	dbPath := filepath.Join(abs, "dpr.db")
	if st, err := os.Stat(dbPath); err == nil && st.Mode().IsRegular() {
		fc.LastWrite = st.ModTime().Format(time.RFC3339)
		v, err := store.ReadUserVersion(dbPath)
		switch {
		case err != nil:
			fc.Note = "이 폴더의 dpr.db 는 급여약품 관리 자료가 아닙니다"
		case v > store.SchemaVersion():
			fc.HasData, fc.Newer = true, true
			fc.Note = "더 새 버전의 프로그램이 만든 자료입니다 — 프로그램을 먼저 업데이트하세요"
		default:
			fc.HasData = true
		}
		return fc
	}
	fc.Empty = empty
	switch {
	case !fc.Writable:
		fc.Note = "이 폴더에 쓸 권한이 없습니다"
	case !empty:
		fc.Note = "비어 있지 않은 폴더입니다 — 빈 폴더(새 폴더)를 만들어 고르세요"
	}
	return fc
}

func samePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if platform.IsWindows() {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

// within : p 가 dir 안(하위)에 있는가
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)
}

// lockAll : 가져오기·수집·표준코드·급여목록·업데이트가 하나도 진행 중이 아닐 때만 잡는다 (기다리지 않는다)
func (s *Service) lockAll() (func(), error) {
	locks := []struct {
		name string
		try  func() bool
		un   func()
	}{
		{"가져오기", s.importMu.TryLock, s.importMu.Unlock},
		{"고시 확인", s.crawlMu.TryLock, s.crawlMu.Unlock},
		{"표준코드 확인", s.stdMu.TryLock, s.stdMu.Unlock},
		{"약제급여목록 받기", s.masterMu.TryLock, s.masterMu.Unlock},
		{"업데이트", s.updMu.TryLock, s.updMu.Unlock},
	}
	var held []func()
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i]()
		}
	}
	for _, l := range locks {
		if !l.try() {
			release()
			return nil, fmt.Errorf("%s 작업이 진행 중입니다 — 끝난 뒤 다시 해 주세요", l.name)
		}
		held = append(held, l.un)
	}
	return release, nil
}

// dataDirSwitch : 위치를 바꾼 뒤 다시 시작하기 전 — 다시 시작하지 못하면 CancelDataDirSwitch 로 되돌린다
type dataDirSwitch struct {
	prevLoc string // 되돌릴 위치 기록 ("" = 기본 위치)
	target  string
	release func()
}

// prevLocation : 지금 쓰는 폴더로 되돌릴 때 적을 위치 ("" = 기본 위치)
func (s *Service) prevLocation() string {
	if s.DataDirSource == platform.SourceCustom {
		return s.DataDir
	}
	return ""
}

// beginSwitch : 위치를 기록했다 — 다시 시작할 때까지 작업 잠금을 쥐고 있는다 (옛 폴더에 새 자료가 쓰이지 않게)
func (s *Service) beginSwitch(target string, release func()) {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	s.switching = &dataDirSwitch{prevLoc: s.prevLocation(), target: target, release: release}
}

// CancelDataDirSwitch : 위치를 바꿨지만 다시 시작하지 못했을 때 — 위치 기록을 되돌리고 잠금을 푼다.
// (그대로 두면 이 창은 옛 폴더에, 다음에 켜는 창은 새 폴더에 쓴다)
func (s *Service) CancelDataDirSwitch(reason string) error {
	s.switchMu.Lock()
	sw := s.switching
	s.switching = nil
	s.switchMu.Unlock()
	if sw == nil {
		return nil
	}
	if err := platform.SetDataLocation(sw.prevLoc); err != nil {
		// 되돌리지 못함 — 잠금을 쥔 채로 둔다 (다음에 켜면 새 폴더로 열린다)
		s.Log.Error("데이터 폴더 바꾸기를 되돌리지 못함", "target", sw.target, "reason", reason, "err", err)
		return err
	}
	_ = logChange(s.DB, ChangeEntry{Kind: "data_dir", OldValue: sw.target, NewValue: s.DataDir, Note: "다시 시작하지 못해 데이터 폴더 바꾸기를 취소함: " + reason, Source: "설정"})
	sw.release()
	s.Log.Warn("데이터 폴더 바꾸기 취소", "target", sw.target, "reason", reason)
	return nil
}

// testHookAfterMoveCopy : 테스트에서 '사본을 만든 뒤 다른 연결이 쓰는' 순간을 만든다 (평소에는 nil)
var testHookAfterMoveCopy func()

// connQuerier : 한 연결(sql.Conn)로 logChange 를 쓰기 위한 감싸개
type connQuerier struct{ c *sql.Conn }

func (q connQuerier) Exec(query string, args ...any) (sql.Result, error) {
	return q.c.ExecContext(nilCtx(), query, args...)
}
func (q connQuerier) QueryRow(query string, args ...any) *sql.Row {
	return q.c.QueryRowContext(nilCtx(), query, args...)
}
func (q connQuerier) Query(query string, args ...any) (*sql.Rows, error) {
	return q.c.QueryContext(nilCtx(), query, args...)
}

// MoveDataDir 는 지금 자료를 빈 폴더 target 으로 옮기고 그 폴더를 쓰도록 기록한다. 성공하면 호출한 쪽이 다시 시작해야 한다
// (다시 시작하지 못하면 CancelDataDirSwitch). 성공하면 작업 잠금을 풀지 않는다 — 다시 시작하기 전에 옛 폴더에 새 자료가 쓰이지 않게.
func (s *Service) MoveDataDir(target string) (string, error) {
	if ok, why := s.canMoveDataDir(); !ok {
		return "", errors.New(why)
	}
	fc := s.CheckFolder(target)
	if fc.Path == "" || fc.Same || !fc.Empty || !fc.Writable || fc.HasData {
		if fc.Note == "" {
			fc.Note = "옮길 수 없는 폴더입니다"
		}
		return "", errors.New(fc.Note)
	}
	release, err := s.lockAll()
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			release()
		}
	}()
	dst := fc.Path
	created := []string{}
	cleanup := func() { // 실패하면 이번에 만든 것만 지운다 (고른 폴더는 비어 있었다)
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.RemoveAll(created[i])
		}
	}
	// 1) 기록·받은 원본·오류 보고서 — 오래 걸릴 수 있어 자료 사본보다 먼저 (백업은 옮기지 않는다)
	for _, sub := range []string{"logs", "raw", "reports"} {
		src := filepath.Join(s.DataDir, sub)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		created = append(created, filepath.Join(dst, sub))
		if err := copyTree(src, filepath.Join(dst, sub)); err != nil {
			cleanup()
			return "", fmt.Errorf("%s 폴더를 복사하지 못했습니다: %w", sub, err)
		}
	}
	// 2) 한 연결로: 바뀜 번호(data_version) → 수정 기록 → 자료 사본. data_version 은 '다른' 연결이 쓸 때만 바뀌므로
	//    같은 연결로 다시 읽어 그사이 작업 스케줄러·자동 작업·화면이 쓴 것이 있는지 본다.
	conn, err := s.DB.Conn(nilCtx())
	if err != nil {
		cleanup()
		return "", err
	}
	defer conn.Close()
	dataVersion := func() (v int64) {
		_ = conn.QueryRowContext(nilCtx(), "PRAGMA data_version").Scan(&v)
		return
	}
	v0 := dataVersion()
	// 작업 스케줄러(다른 프로세스)가 이미 돌고 있으면, 사본을 만든 뒤에 옛 폴더에 쓸 수 있다 — data_version 은 그 뒤 쓰기를 못 막는다
	if runningJobs(connQuerier{conn}) > 0 {
		cleanup()
		return "", errors.New("자동 확인(고시·표준코드·급여목록)이 진행 중입니다 — 몇 분 뒤 다시 해 주세요")
	}
	if err := logChange(connQuerier{conn}, ChangeEntry{Kind: "data_dir", OldValue: s.DataDir, NewValue: dst, Note: "데이터 폴더 옮김 (원래 폴더는 그대로 둠)", Source: "설정"}); err != nil {
		cleanup()
		return "", err
	}
	var logID int64
	_ = conn.QueryRowContext(nilCtx(), "SELECT last_insert_rowid()").Scan(&logID)
	failed := func(err error) (string, error) { // 사본에는 '옮김' 기록이 있지만 지운다 — 옛 자료에는 실패로 남긴다
		cleanup()
		_, _ = conn.ExecContext(nilCtx(), "UPDATE change_log SET note = ? WHERE id = ?", "데이터 폴더 옮기기 실패 — 지금 폴더를 그대로 씀: "+err.Error(), logID)
		return "", err
	}
	dbDst := filepath.Join(dst, "dpr.db")
	created = append(created, dbDst, dbDst+"-wal", dbDst+"-shm")
	if _, err := conn.ExecContext(nilCtx(), "VACUUM INTO ?", dbDst); err != nil {
		return failed(fmt.Errorf("자료를 새 폴더에 쓰지 못했습니다: %w", err))
	}
	// 3) 새 사본 확인 — 스키마 번호·주요 표 행 수가 같고 손상이 없어야 한다
	if err := s.verifyCopy(dbDst); err != nil {
		return failed(err)
	}
	if testHookAfterMoveCopy != nil {
		testHookAfterMoveCopy()
	}
	changed := errors.New("옮기는 동안 다른 작업(자동 확인·작업 스케줄러)이 자료를 바꿨습니다 — 옮기지 않았습니다. 잠시 뒤 다시 해 주세요")
	if dataVersion() != v0 {
		return failed(changed)
	}
	// 4) 위치 기록 (기본 위치로 되돌리는 경우에는 기록을 지운다) — 기록한 뒤에도 한 번 더 본다
	loc := dst
	if samePath(dst, platform.DefaultDataDir()) {
		loc = ""
	}
	if err := platform.SetDataLocation(loc); err != nil {
		return failed(fmt.Errorf("새 위치를 기록하지 못했습니다: %w", err))
	}
	if dataVersion() != v0 {
		_ = platform.SetDataLocation(s.prevLocation())
		return failed(changed)
	}
	ok = true
	s.beginSwitch(dst, release)
	s.Log.Info("데이터 폴더 옮김", "from", s.DataDir, "to", dst)
	return fmt.Sprintf("자료를 %s 로 옮겼습니다. 다시 시작합니다. 원래 폴더(%s)에는 옮기기 전 자료가 그대로 있습니다 — 새 폴더에서 잘 열리는지 확인한 뒤 지워도 됩니다. 백업은 새 폴더에서 새로 쌓입니다.", dst, s.DataDir), nil
}

// UseDataDir 는 이미 있는 급여약품 관리 자료 폴더(dpr.db 가 든 폴더)를 쓰도록 바꾼다. 지금 폴더의 자료는 그대로 남는다.
// 성공하면 호출한 쪽이 다시 시작해야 한다 (다시 시작하지 못하면 CancelDataDirSwitch).
func (s *Service) UseDataDir(target string) (string, error) {
	if ok, why := s.canMoveDataDir(); !ok {
		return "", errors.New(why)
	}
	fc := s.CheckFolder(target)
	switch {
	case fc.Path == "" || fc.Same:
		return "", errors.New(orDefaultStr(fc.Note, "바꿀 수 없는 폴더입니다"))
	case !fc.HasData:
		return "", errors.New(orDefaultStr(fc.Note, "이 폴더에는 급여약품 관리 자료(dpr.db)가 없습니다"))
	case fc.Newer:
		return "", errors.New(fc.Note)
	case !fc.Writable:
		return "", errors.New("이 폴더에 쓸 권한이 없습니다")
	}
	release, err := s.lockAll()
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			release()
		}
	}()
	if runningJobs(s.DB) > 0 { // 진행 중인 작업이 지금 폴더에 쓰는 중 — 바꾼 뒤에는 보이지 않는다
		return "", errors.New("자동 확인(고시·표준코드·급여목록)이 진행 중입니다 — 몇 분 뒤 다시 해 주세요")
	}
	if err := checkDB(filepath.Join(fc.Path, "dpr.db")); err != nil {
		return "", err
	}
	loc := fc.Path
	if samePath(fc.Path, platform.DefaultDataDir()) {
		loc = ""
	}
	if err := platform.SetDataLocation(loc); err != nil {
		return "", fmt.Errorf("새 위치를 기록하지 못했습니다: %w", err)
	}
	_ = logChange(s.DB, ChangeEntry{Kind: "data_dir", OldValue: s.DataDir, NewValue: fc.Path, Note: "다른 폴더의 자료로 바꿈 (지금 자료는 그대로 둠)", Source: "설정"})
	ok = true
	s.beginSwitch(fc.Path, release)
	s.Log.Info("데이터 폴더 바꿈", "from", s.DataDir, "to", fc.Path)
	return fmt.Sprintf("%s 의 자료로 바꿉니다. 다시 시작합니다. 지금까지 쓰던 자료(%s)는 그대로 남아 있습니다.", fc.Path, s.DataDir), nil
}

// verifyCopy : 새 사본이 지금 자료와 같은지 (스키마 번호·주요 표 행 수·손상 검사)
func (s *Service) verifyCopy(path string) error {
	v, err := store.ReadUserVersion(path)
	if err != nil {
		return fmt.Errorf("새 사본을 읽지 못했습니다: %w", err)
	}
	var cur int
	_ = s.DB.QueryRow("PRAGMA user_version").Scan(&cur)
	if v != cur {
		return fmt.Errorf("새 사본의 스키마 번호가 다릅니다 (%d ≠ %d)", v, cur)
	}
	db, err := store.Open(path)
	if err != nil {
		return fmt.Errorf("새 사본을 열지 못했습니다: %w", err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("새 사본 검사 실패: %s %v", check, err)
	}
	for _, t := range []string{"purchase_line", "dispense_line", "initial_line", "stock_anchor", "price_change", "change_log"} {
		var a, b int
		_ = s.DB.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&a)
		_ = db.QueryRow("SELECT COUNT(*) FROM " + t).Scan(&b)
		if a != b {
			return fmt.Errorf("새 사본의 %s 행 수가 다릅니다 (%d ≠ %d)", t, b, a)
		}
	}
	return nil
}

// checkDB : 다른 폴더의 자료가 열리고 손상되지 않았는지 (스키마 번호는 CheckFolder 에서 확인)
func checkDB(path string) error {
	db, err := store.Open(path)
	if err != nil {
		return fmt.Errorf("그 폴더의 자료를 열지 못했습니다: %w", err)
	}
	defer db.Close()
	var check string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		return fmt.Errorf("그 폴더의 자료가 손상되었습니다: %s %v", check, err)
	}
	return nil
}

// copyTree : 폴더를 통째로 복사한다 (일반 파일만, 연결·특수 파일은 건너뜀)
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return copyFile(p, out)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
