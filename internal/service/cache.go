package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync"
	"time"
)

// 화면 계산 캐시 — 재고·단가처럼 여러 화면이 같은 값을 반복해서 계산하는 것을 한 번으로 줄인다.
//
// 무효화는 SQLite 'PRAGMA data_version' 으로 한다. 이 값은 '다른 연결'이 커밋할 때마다 바뀐다.
// 버전 확인 전용 연결을 하나 따로 두므로, 이 앱의 모든 쓰기(다른 연결)와
// 작업 스케줄러로 실행된 다른 프로세스(DPR crawl)의 쓰기까지 감지된다.

// dataVer : 데이터 버전. data_version 은 연결마다 따로 세므로, 확인용 연결을 새로 열면 세대(gen)를 올려
// 이전 연결 때 저장한 캐시를 쓰지 않게 한다 (새 연결의 값이 우연히 같을 수 있다).
type dataVer struct{ gen, v int64 }

type memo struct {
	mu      sync.Mutex
	ver     dataVer
	entries map[string]any
}

func (s *Service) dataVersion() (dataVer, bool) {
	s.verMu.Lock()
	defer s.verMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if s.verConn == nil {
		c, err := s.DB.Conn(ctx)
		if err != nil {
			return dataVer{}, false
		}
		s.verConn = c
		s.verGen++
	}
	var v int64
	if err := s.verConn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v); err != nil {
		_ = s.verConn.Raw(func(any) error { return driver.ErrBadConn }) // 풀로 돌려보내지 않고 버린다
		_ = s.verConn.Close()
		s.verConn = nil
		return dataVer{}, false
	}
	return dataVer{s.verGen, v}, true
}

func (s *Service) closeCache() {
	s.verMu.Lock()
	defer s.verMu.Unlock()
	if s.verConn != nil {
		_ = s.verConn.Close()
		s.verConn = nil
	}
}

// cached 는 key 의 값을 데이터가 바뀌지 않은 동안 재사용한다. clone 으로 복사본을 돌려준다(호출한 쪽이 고쳐도 안전).
func cached[T any](s *Service, key string, fn func() (T, error), clone func(T) T) (T, error) {
	ver, ok := s.dataVersion()
	if !ok { // 버전을 알 수 없으면 캐시하지 않는다
		return fn()
	}
	s.cache.mu.Lock()
	if s.cache.entries == nil || s.cache.ver != ver {
		s.cache.entries, s.cache.ver = map[string]any{}, ver
	}
	if v, ok := s.cache.entries[key]; ok {
		s.cache.mu.Unlock()
		return clone(v.(T)), nil
	}
	s.cache.mu.Unlock()
	v, err := fn()
	if err != nil {
		return v, err
	}
	s.cache.mu.Lock()
	if s.cache.entries != nil && s.cache.ver == ver { // 계산하는 동안 데이터가 바뀌었으면 저장하지 않는다
		s.cache.entries[key] = v
	}
	s.cache.mu.Unlock()
	return clone(v), nil
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

var _ = sql.ErrNoRows
