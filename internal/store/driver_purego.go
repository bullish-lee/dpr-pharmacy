//go:build !cgo

package store

import (
	"database/sql"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

// 순수 Go SQLite (cgo 없이 빌드할 때). 연결마다 _pragma 로 설정을 적용한다.
const engineName = "Go SQLite (modernc.org/sqlite)"

func openSQLite(target string) (*sql.DB, error) {
	params := url.Values{}
	for _, p := range connPragmas {
		kv := strings.SplitN(p, " = ", 2)
		params.Add("_pragma", kv[0]+"("+kv[1]+")")
	}
	// 쓰기 트랜잭션은 시작할 때 잠금을 잡는다 (읽은 뒤 쓰기로 올릴 때 다른 쓰기와 부딪혀 바로 'database is locked' 나는 것 방지)
	params.Set("_txlock", "immediate")
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	return sql.Open("sqlite", target+sep+params.Encode())
}
