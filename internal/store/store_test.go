package store

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"testing"
)

func TestOpenMigrate(t *testing.T) {
	for _, p := range []string{":memory:", filepath.Join(t.TempDir(), "dpr.db")} {
		db, err := Open(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		var v int
		if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 11 {
			t.Fatalf("user_version=%d err=%v", v, err)
		}
		var mode string
		_ = db.QueryRow("PRAGMA journal_mode").Scan(&mode)
		if p != ":memory:" && mode != "wal" {
			t.Fatalf("journal_mode=%s", mode)
		}
		if err := db.SetSetting("k", "v"); err != nil || db.GetSetting("k", "") != "v" {
			t.Fatal("setting")
		}
		if p != ":memory:" {
			if err := db.Backup(filepath.Join(t.TempDir(), "b.db")); err != nil {
				t.Fatal(err)
			}
		}
		db.Close()
		// 재오픈 시 마이그레이션 재실행 없이 통과
		if p != ":memory:" {
			db2, err := Open(p)
			if err != nil {
				t.Fatal(err)
			}
			db2.Close()
		}
	}
}

// 마이그레이션은 추가만 한다 — 업데이트 뒤 DPR.exe.old 로 되돌려도(옛 exe 가 새 DB 를 열어도) 자료가 그대로 쓰이게.
// 표·열을 지우거나 이름을 바꿔야 하면 이 정책과 되돌리기 안내(RELEASE_README)를 함께 다시 정한다.
func TestMigrationsAreAdditive(t *testing.T) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	bad := regexp.MustCompile(`(?i)\b(DROP\s+TABLE|DROP\s+COLUMN|RENAME\s+(TO|COLUMN)|ALTER\s+TABLE\s+\S+\s+RENAME)\b`)
	for _, e := range entries {
		b, _ := migrationFS.ReadFile("migrations/" + e.Name())
		if m := bad.Find(b); m != nil {
			t.Errorf("%s: %q — 옛 exe 로 되돌리면 깨지는 변경", e.Name(), m)
		}
	}
}

// 정상 종료 전(쓰기 기록이 -wal 에만 있음)에도 스키마 번호를 읽는다
func TestReadUserVersionWithWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dpr.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA wal_autocheckpoint = 0"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 42"); err != nil {
		t.Fatal(err)
	}
	if v, err := ReadUserVersion(path); err != nil || v != 42 {
		t.Fatalf("열려 있는 동안: %d %v", v, err)
	}
}
