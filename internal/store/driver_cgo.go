//go:build cgo

package store

import (
	"database/sql"
	"strings"
	"sync"

	"github.com/mattn/go-sqlite3"
)

// C SQLite (cgo). 연결이 새로 만들어질 때마다 connPragmas 를 적용한다.
const engineName = "C SQLite " + "(mattn/go-sqlite3)"

var registerOnce sync.Once

func openSQLite(target string) (*sql.DB, error) {
	registerOnce.Do(func() {
		sql.Register("sqlite3_dpr", &sqlite3.SQLiteDriver{ConnectHook: func(c *sqlite3.SQLiteConn) error {
			for _, p := range connPragmas {
				if _, err := c.Exec("PRAGMA "+p, nil); err != nil {
					return err
				}
			}
			return nil
		}})
	})
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	return sql.Open("sqlite3_dpr", target+sep+"_txlock=immediate")
}
