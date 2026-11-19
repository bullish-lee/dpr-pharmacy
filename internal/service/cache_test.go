package service

import (
	"database/sql"
	"testing"
)

// 버전 확인 연결을 새로 열면(오류 뒤 등) 이전 캐시를 쓰지 않는다.
// 새 연결의 data_version 은 그 연결이 열린 뒤의 변경만 세므로, 그 사이의 쓰기를 놓친 채 같은 값이 나올 수 있다.
func TestCacheNotReusedAfterVersionConnReset(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.DB.SetSetting("cache_probe", "A")
	read := func() (string, error) { return s.DB.GetSetting("cache_probe", ""), nil }
	same := func(v string) string { return v }
	if v, _ := cached(s, "probe", read, same); v != "A" {
		t.Fatalf("처음 %q", v)
	}
	s.closeCache() // 버전 확인 연결이 끊김
	_ = s.DB.SetSetting("cache_probe", "B")
	if v, _ := cached(s, "probe", read, same); v != "B" {
		t.Fatalf("연결을 다시 연 뒤에도 이전 캐시를 씀: %q", v)
	}
}

// 트랜잭션 안에서 패닉이 나도 롤백해 쓰기 잠금을 푼다
func TestTxRollsBackOnPanic(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	func() {
		defer func() { _ = recover() }()
		_ = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
			if _, err := tx.Exec("INSERT INTO app_setting(key, value) VALUES('panic_probe', '1')"); err != nil {
				return err
			}
			panic("시험용 패닉")
		})
	}()
	if err := s.DB.SetSetting("after_panic", "ok"); err != nil {
		t.Fatalf("패닉 뒤 쓰기가 막힘: %v", err)
	}
	if v := s.DB.GetSetting("panic_probe", ""); v != "" {
		t.Fatalf("패닉한 트랜잭션의 쓰기가 남음: %q", v)
	}
}
