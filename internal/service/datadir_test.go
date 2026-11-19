package service

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dpr/internal/platform"
	"dpr/internal/store"
)

func dataDirService(t *testing.T) *Service {
	t.Helper()
	base := t.TempDir()
	old := platform.ConfigBase
	platform.ConfigBase = func() (string, error) { return base, nil }
	t.Cleanup(func() { platform.ConfigBase = old })
	s, err := NewTemp(filepath.Join(base, "DPR"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.DataDirSource = platform.SourceDefault
	importPurchases(t, s, "p.xlsx", []any{"20260302", "가나정", "600000010", "가나약품", "사입", "정", 1, 10, 100, 1000})
	_ = os.MkdirAll(filepath.Join(s.DataDir, "logs"), 0o755)
	_ = os.WriteFile(filepath.Join(s.DataDir, "logs", "dpr.log"), []byte("time=x level=INFO msg=시작\n"), 0o644)
	return s
}

// 빈 폴더로 옮기기: 사본·기록 복사·확인 뒤 위치를 기록하고, 원래 폴더는 그대로 둔다
func TestMoveDataDir(t *testing.T) {
	s := dataDirService(t)
	target := filepath.Join(t.TempDir(), "새 자료")
	_ = os.MkdirAll(target, 0o755)
	if fc := s.CheckFolder(target); !fc.Empty || !fc.Writable || fc.HasData {
		t.Fatalf("빈 폴더: %+v", fc)
	}
	msg, err := s.MoveDataDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "다시 시작") {
		t.Fatalf("메시지: %s", msg)
	}
	if d, src, err := platform.Locate(); err != nil || src != platform.SourceCustom || d != target {
		t.Fatalf("위치 기록: %s %s %v", d, src, err)
	}
	if v, _ := store.ReadUserVersion(filepath.Join(target, "dpr.db")); v != store.SchemaVersion() {
		t.Fatalf("사본 스키마 %d", v)
	}
	db, err := store.Open(filepath.Join(target, "dpr.db"))
	if err != nil {
		t.Fatal(err)
	}
	var n, logs int
	_ = db.QueryRow("SELECT COUNT(*) FROM purchase_line").Scan(&n)
	_ = db.QueryRow("SELECT COUNT(*) FROM change_log WHERE kind = 'data_dir'").Scan(&logs)
	db.Close()
	if n != 1 || logs != 1 {
		t.Fatalf("사본: 사입 %d행, 옮김 기록 %d", n, logs)
	}
	if _, err := os.Stat(filepath.Join(target, "logs", "dpr.log")); err != nil {
		t.Fatal("기록(로그)도 옮겨야 함")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "dpr.db")); err != nil {
		t.Fatal("원래 자료는 그대로 둬야 함")
	}
}

// 옮길 수 없는 경우: 비어 있지 않은 폴더, 지금 폴더 안, 휴대용·환경변수 실행
func TestMoveDataDirRefusals(t *testing.T) {
	s := dataDirService(t)
	full := t.TempDir()
	_ = os.WriteFile(filepath.Join(full, "다른 파일.txt"), []byte("x"), 0o644)
	for label, dir := range map[string]string{"비어 있지 않음": full, "지금 폴더 안": filepath.Join(s.DataDir, "logs"), "지금 폴더": s.DataDir} {
		if _, err := s.MoveDataDir(dir); err == nil {
			t.Fatalf("%s: 거부해야 함", label)
		}
	}
	s.DataDirSource = platform.SourcePortable
	if info := s.DataDirInfo(); info.CanMove || info.MoveNote == "" {
		t.Fatalf("휴대용은 화면에서 옮기지 않음: %+v", info)
	}
	if _, err := s.MoveDataDir(t.TempDir()); err == nil {
		t.Fatal("휴대용은 거부")
	}
}

// 옮기다 실패하면 이번에 만든 것을 지워 고른 폴더를 비운 상태로 되돌린다 (다시 시도할 수 있게)
func TestMoveDataDirCleansUpOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 는 읽기 권한을 무시한다")
	}
	s := dataDirService(t)
	bad := filepath.Join(s.DataDir, "raw", "읽을 수 없음.bin")
	_ = os.MkdirAll(filepath.Dir(bad), 0o755)
	_ = os.WriteFile(bad, []byte("x"), 0o644)
	_ = os.Chmod(bad, 0o000)
	defer os.Chmod(bad, 0o644)
	target := filepath.Join(t.TempDir(), "새 자료")
	_ = os.MkdirAll(target, 0o755)
	if _, err := s.MoveDataDir(target); err == nil {
		t.Fatal("복사 실패를 알려야 함")
	}
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		t.Fatalf("만든 것을 지워야 함: %d개 남음", len(entries))
	}
	if _, src, _ := platform.Locate(); src != platform.SourceDefault {
		t.Fatal("실패하면 위치를 바꾸지 않음")
	}
	if _, err := s.Import(ImportRequest{Kind: "purchase", FileName: "p2.xlsx", Force: true, DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {purchaseHdr,
		{"20260303", "가나정", "600000010", "가나약품", "사입", "정", 1, 5, 100, 500}}})}); err != nil {
		t.Fatalf("실패 뒤 작업 잠금이 풀려야 함: %v", err)
	}
}

// 이미 있는 자료 폴더로 바꾸기 — 더 새 버전이 만든 자료는 거부
func TestUseExistingDataDir(t *testing.T) {
	s := dataDirService(t)
	other := filepath.Join(t.TempDir(), "다른 PC 자료")
	_ = os.MkdirAll(other, 0o755)
	db, err := store.Open(filepath.Join(other, "dpr.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if fc := s.CheckFolder(other); !fc.HasData || fc.Newer {
		t.Fatalf("자료 있는 폴더: %+v", fc)
	}
	newer := filepath.Join(t.TempDir(), "새 버전 자료")
	_ = os.MkdirAll(newer, 0o755)
	ndb, err := store.Open(filepath.Join(newer, "dpr.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = ndb.Exec("PRAGMA user_version = 999")
	ndb.Close()
	if _, err := s.UseDataDir(newer); err == nil || !strings.Contains(err.Error(), "더 새 버전") {
		t.Fatalf("더 새 버전 자료는 거부: %v", err)
	}
	if _, err := s.UseDataDir(other); err != nil {
		t.Fatal(err)
	}
	if d, _, err := platform.Locate(); err != nil || d != other {
		t.Fatalf("위치: %s %v", d, err)
	}
}

// 따로 정한 백업 폴더에 쓸 수 없으면 기본 위치에 백업하고, 그 장애 동안 한 번만 알린다
func TestBackupFolderFallback(t *testing.T) {
	s := dataDirService(t)
	usb := filepath.Join(t.TempDir(), "USB 백업")
	_ = os.MkdirAll(usb, 0o755)
	if err := s.SetBackupFolder(usb); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Backup(); err != nil || filepath.Dir(p) != usb {
		t.Fatalf("정한 폴더에 백업: %s %v", p, err)
	}
	_ = os.RemoveAll(usb) // USB 를 뺌
	for i := 0; i < 3; i++ {
		p, err := s.Backup()
		if err != nil || filepath.Dir(p) != filepath.Join(s.DataDir, "backups") {
			t.Fatalf("기본 위치로 백업: %s %v", p, err)
		}
	}
	var notes int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '백업 폴더에 쓸 수 없습니다'").Scan(&notes)
	if notes != 1 {
		t.Fatalf("알림은 한 번: %d", notes)
	}
	if info := s.DataDirInfo(); info.BackupNote == "" || info.BackupCustom {
		t.Fatalf("화면에 이유 표시: %+v", info)
	}
	if err := s.SetBackupFolder(filepath.Join(t.TempDir(), "없는 폴더")); err == nil {
		t.Fatal("쓸 수 없는 폴더는 정하지 않음")
	}
	log, _ := s.ChangeLog(ChangeFilter{Kind: "backup_dir"})
	if len(log) != 1 {
		t.Fatalf("백업 폴더 변경 기록: %d", len(log))
	}
	var _ = sql.ErrNoRows
}
