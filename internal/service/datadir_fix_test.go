package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dpr/internal/platform"
	"dpr/internal/store"
)

// 사본을 만든 뒤 다른 연결(작업 스케줄러·자동 작업·화면)이 자료를 바꾸면 옮기지 않는다 — 예전에는 그 수정이 새 폴더에서 사라졌다
func TestMoveDataDirAbortsOnConcurrentWrite(t *testing.T) {
	for _, who := range []string{"같은 앱의 다른 연결", "다른 프로세스(작업 스케줄러)"} {
		t.Run(who, func(t *testing.T) {
			s := dataDirService(t)
			target := filepath.Join(t.TempDir(), "새 자료")
			_ = os.MkdirAll(target, 0o755)
			testHookAfterMoveCopy = func() {
				if who == "같은 앱의 다른 연결" {
					if _, err := s.SaveSupplier(SupplierInput{Name: "새도매", Kind: "wholesaler", Active: true}); err != nil {
						t.Error(err)
					}
					return
				}
				other, err := store.Open(filepath.Join(s.DataDir, "dpr.db"))
				if err != nil {
					t.Error(err)
					return
				}
				defer other.Close()
				_ = other.SetSetting("cli_probe", "1")
			}
			defer func() { testHookAfterMoveCopy = nil }()
			_, err := s.MoveDataDir(target)
			if err == nil || !strings.Contains(err.Error(), "바꿨습니다") {
				t.Fatalf("옮기지 않아야 함: %v", err)
			}
			if entries, _ := os.ReadDir(target); len(entries) != 0 {
				t.Fatalf("만든 것을 지워야 함: %d개 남음", len(entries))
			}
			if d, src, err := platform.Locate(); err != nil || src != platform.SourceDefault || d != s.DataDir {
				t.Fatalf("위치는 그대로: %s %s %v", d, src, err)
			}
			var note string
			_ = s.DB.QueryRow("SELECT note FROM change_log WHERE kind = 'data_dir' ORDER BY id DESC LIMIT 1").Scan(&note)
			if !strings.Contains(note, "실패") {
				t.Fatalf("수정 기록에 실패로 남김: %q", note)
			}
			// 잠금을 풀었다 — 다시 옮길 수 있다
			testHookAfterMoveCopy = nil
			if _, err := s.MoveDataDir(target); err != nil {
				t.Fatalf("다시 옮기기: %v", err)
			}
		})
	}
}

// 옮긴 뒤 다시 시작하지 못하면 위치 기록을 되돌리고 잠금을 푼다 (그대로 두면 이 창과 다음 창이 다른 자료를 쓴다)
func TestCancelDataDirSwitch(t *testing.T) {
	s := dataDirService(t)
	target := filepath.Join(t.TempDir(), "새 자료")
	_ = os.MkdirAll(target, 0o755)
	if _, err := s.MoveDataDir(target); err != nil {
		t.Fatal(err)
	}
	if _, err := s.lockAll(); err == nil {
		t.Fatal("다시 시작 전에는 잠금을 쥐고 있어야 함")
	}
	if err := s.CancelDataDirSwitch("테스트"); err != nil {
		t.Fatal(err)
	}
	if d, src, err := platform.Locate(); err != nil || src != platform.SourceDefault || d != s.DataDir {
		t.Fatalf("위치 되돌림: %s %s %v", d, src, err)
	}
	release, err := s.lockAll()
	if err != nil {
		t.Fatalf("잠금을 풀어야 함: %v", err)
	}
	release()
	if err := s.CancelDataDirSwitch("두 번"); err != nil {
		t.Fatal("두 번 불러도 괜찮음")
	}
}

// 폴더 이름에 '#' 가 있어도 그 폴더의 자료를 쓴다 (예전에는 '약국#2' 대신 '약국' 이라는 빈 DB 를 만들어 썼다)
func TestDataDirWithHashInName(t *testing.T) {
	s := dataDirService(t)
	parent := t.TempDir()
	other := filepath.Join(parent, "약국#2")
	_ = os.MkdirAll(other, 0o755)
	if err := s.DB.Backup(filepath.Join(other, "dpr.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UseDataDir(other); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 1 {
		t.Fatalf("다른 파일이 생김: %d개", len(entries))
	}
	_ = s.CancelDataDirSwitch("테스트 정리")
	moved := filepath.Join(parent, "a#b%41")
	_ = os.MkdirAll(moved, 0o755)
	if _, err := s.MoveDataDir(moved); err != nil {
		t.Fatal(err)
	}
	d, _, err := platform.Locate()
	if err != nil || d != moved {
		t.Fatalf("%s %v", d, err)
	}
	s2, err := NewTemp(d) // 다시 켠 것처럼
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var n int
	_ = s2.DB.QueryRow("SELECT COUNT(*) FROM purchase_line").Scan(&n)
	if n != 1 {
		t.Fatalf("옮긴 자료의 사입 %d행", n)
	}
}

// 네트워크 폴더는 데이터 폴더로 고를 수 없다 (SQLite 쓰기 기록이 네트워크에서는 안전하지 않음)
func TestCheckFolderRejectsNetwork(t *testing.T) {
	s := dataDirService(t)
	fc := s.CheckFolder("//server/share/DPR")
	if fc.Empty || fc.HasData || !strings.Contains(fc.Note, "네트워크") {
		t.Fatalf("%+v", fc)
	}
	if _, err := s.MoveDataDir("//server/share/DPR"); err == nil || !strings.Contains(err.Error(), "네트워크") {
		t.Fatalf("옮기기: %v", err)
	}
}

// 백업 폴더를 확인할 수 없으면(드라이브 빠짐·권한) 이름 고르기를 계속 돌지 않고 실패한다
func TestBackupStatErrorDoesNotSpin(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root 는 권한 검사를 건너뛴다")
	}
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bdir := filepath.Join(s.DataDir, "backups")
	_ = os.MkdirAll(bdir, 0o755)
	if err := os.Chmod(bdir, 0o600); err != nil { // 있지만 들어갈 수 없음 → 안의 파일 Stat 이 권한 오류
		t.Skip(err)
	}
	defer os.Chmod(bdir, 0o755)
	done := make(chan error, 1)
	go func() { _, err := s.Backup(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("백업 폴더에 들어갈 수 없는데 성공")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("백업이 끝나지 않음 (무한 반복)")
	}
}

// 작업 스케줄러가 이미 돌고 있으면(실행 중 기록) 옮기지 않는다 — 사본을 만든 뒤 옛 폴더에 쓰면 data_version 으로 못 막는다
func TestMoveRefusedWhileJobRunning(t *testing.T) {
	s := dataDirService(t)
	other, err := store.Open(filepath.Join(s.DataDir, "dpr.db")) // 작업 스케줄러 프로세스처럼
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec("INSERT INTO job_run(job, started_at, status) VALUES('crawl', ?, 'running')", time.Now().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "새 자료")
	_ = os.MkdirAll(target, 0o755)
	if _, err := s.MoveDataDir(target); err == nil || !strings.Contains(err.Error(), "진행 중") {
		t.Fatalf("옮기지 않아야 함: %v", err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Fatalf("만든 것을 지워야 함: %d개", len(entries))
	}
	if _, err := s.UseDataDir(target); err == nil {
		t.Fatal("바꾸기도 막아야 함")
	}
}
