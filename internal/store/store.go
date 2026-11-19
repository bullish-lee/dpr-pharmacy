// Package store 는 SQLite 연결과 스키마 마이그레이션을 담당한다.
//
//   - SQLite 엔진: cgo 로 빌드하면 C SQLite(mattn/go-sqlite3, 조회 약 2배 빠름), cgo 가 없으면 순수 Go(modernc.org/sqlite).
//     맥에서 Windows exe 는 mingw-w64(x86_64-w64-mingw32-gcc)로 교차 빌드한다 (Makefile). driver_cgo.go / driver_purego.go 참고.
//   - WAL 모드: 가져오기(쓰기) 중에도 화면 조회(읽기)가 막히지 않는다.
//   - 마이그레이션은 migrations/*.sql 을 실행 파일에 내장하고 PRAGMA user_version 으로 관리한다.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type DB struct {
	*sql.DB
	Path string
}

// 연결마다 적용하는 설정
var connPragmas = []string{"busy_timeout = 5000", "foreign_keys = ON", "synchronous = NORMAL", "journal_mode = WAL", "temp_store = MEMORY", "cache_size = -16000"}

// Engine 은 이 실행 파일에 들어간 SQLite 엔진 이름 (설정·오류 보고에 표시)
func Engine() string { return engineName }

// Open 은 DB 파일을 열고 최신 스키마로 마이그레이션한다. path 가 ":memory:" 면 테스트용 메모리 DB.
func Open(path string) (*DB, error) {
	var target string
	if path == ":memory:" {
		target = "file:dpr-mem-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "?mode=memory&cache=shared"
	} else {
		target = fileURI(path)
	}
	db, err := openSQLite(target)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(8)
	}
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("DB 열기 실패: %w", err)
	}
	d := &DB{DB: db, Path: path}
	if err := d.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

// fileURI : DB 파일 경로 → SQLite file: URI.
//   - Windows 경로(C:\...)는 슬래시로 바꾼다.
//   - '%'·'?'·'#' 는 이스케이프한다. 그대로 두면 '#' 뒤를 잘라 다른 파일을 만들고('약국#2' → '약국'), '%41' 은 'A' 로 읽는다.
//   - 네트워크 경로(\\server\share\…)는 file:////server/share/… — file://server/… 는 '주소 권한' 오류
func fileURI(p string) string {
	u := uriEscaper.Replace(strings.ReplaceAll(p, "\\", "/"))
	if strings.HasPrefix(u, "//") {
		return "file://" + u
	}
	return "file:" + u
}

var uriEscaper = strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F")

func (d *DB) migrate() error {
	var current int
	if err := d.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		ver, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil || ver <= current {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := d.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("마이그레이션 %s 실패: %w", name, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", ver)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Tx 는 함수 안의 작업을 하나의 트랜잭션으로 실행한다 (에러·패닉 시 롤백).
// 패닉이 나도 롤백해야 쓰기 잠금이 풀린다 (화면 바인딩은 패닉을 복구하므로, 롤백하지 않으면 이후 모든 쓰기가 막힌다).
func (d *DB) Tx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	done = true
	return tx.Commit()
}

// Backup 은 실행 중에도 일관된 스냅샷을 dest 파일로 만든다 (VACUUM INTO).
func (d *DB) Backup(dest string) error {
	_, err := d.Exec("VACUUM INTO ?", dest)
	return err
}

// Now 는 저장용 시각 문자열.
func Now() string { return time.Now().Format(time.RFC3339) }

// Setting 읽기/쓰기
func (d *DB) GetSetting(key, def string) string {
	var v string
	if err := d.QueryRow("SELECT value FROM app_setting WHERE key = ?", key).Scan(&v); err != nil {
		return def
	}
	return v
}

func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec("INSERT INTO app_setting(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

// SchemaVersion : 이 실행 파일이 아는 최신 스키마 번호 (내장 마이그레이션 중 가장 큰 번호)
func SchemaVersion() int {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return 0
	}
	max := 0
	for _, e := range entries {
		if v, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0]); err == nil && v > max {
			max = v
		}
	}
	return max
}

// ReadUserVersion 은 DB 파일 머리(100바이트)에서 스키마 번호(user_version)를 읽는다 — 열지 않으므로 마이그레이션되지 않는다.
// 다른 폴더의 자료로 바꾸기 전에 '더 새 버전이 만든 자료'인지 확인할 때 쓴다. SQLite 파일이 아니면 오류.
func ReadUserVersion(path string) (int, error) {
	// 정상 종료 전이면 최신 번호가 아직 -wal 에만 있다 → 열어서 묻는다 (마이그레이션은 하지 않음)
	if st, err := os.Stat(path + "-wal"); err == nil && st.Size() > 0 {
		if db, err := openSQLite(fileURI(path)); err == nil {
			var v int
			err := db.QueryRow("PRAGMA user_version").Scan(&v)
			db.Close()
			if err == nil {
				return v, nil
			}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	head := make([]byte, 100)
	if _, err := io.ReadFull(f, head); err != nil {
		return 0, errors.New("SQLite 파일이 아닙니다 (너무 짧음)")
	}
	if string(head[:16]) != "SQLite format 3\x00" {
		return 0, errors.New("SQLite 파일이 아닙니다")
	}
	return int(binary.BigEndian.Uint32(head[60:64])), nil
}
