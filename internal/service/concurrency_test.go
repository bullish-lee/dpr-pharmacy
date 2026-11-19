package service

import (
	"sync"
	"testing"
	"time"
)

// 앱처럼 이름 재매칭을 뒤에서 돌리면서(AsyncRematch) 가져오기·화면 조회를 동시에 해도 경쟁 상태가 없어야 한다 (go test -race)
func TestConcurrentImportsRematchAndScreens(t *testing.T) {
	s := unitTestService(t, [][]any{
		{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000},
		{"600000020", "다라정", "1", "정", 100},
	})
	s.AsyncRematch = true
	changed := make(chan string, 64)
	s.OnDataChanged = func(what string) {
		select {
		case changed <- what:
		default:
		}
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ { // 화면 조회
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = s.Inventory("", false)
				_, _ = s.DataHealth()
				_, _ = s.Dashboard()
				_, _ = s.ChangeLog(ChangeFilter{})
			}
		}()
	}
	for round := 0; round < 3; round++ {
		importPurchases(t, s, "p.xlsx",
			[]any{"20260302", "가나팩정", "600000010", "가나약품", "사입", "정", 28, 1, 6000, 6000},
			[]any{"20260303", "다라정", "600000020", "가나약품", "사입", "정", 1, 30, 100, 3000})
		mustImport(t, s, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-02-02", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
			{"약품명", "재고수량", "단가"},
			{"가나팩정", 2, 6000},
			{"다라정 1정", 10, 100},
			{"없는약정", 1, 50},
		}})})
		_, _ = s.SaveCount(CountInput{Code: "600000020", Qty: 12, Source: "시험"})
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.rematchMu.Lock()
		busy := s.rematchBusy
		s.rematchMu.Unlock()
		if !busy || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
	if _, err := s.Inventory("", false); err != nil {
		t.Fatal(err)
	}
}
