package service

import (
	"database/sql"
	"errors"
	"testing"
)

// 같은 성분·용량의 다른 회사 제품(둘 다 현재 급여목록)은 한 약으로 묶지 않는다 — 이름에 적힌 회사가 아닌 제품으로 확정되면 안 됨
func TestMatcherKeepsDifferentMakersApart(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustImport(t, s, ImportRequest{Kind: "drug_master", FileName: "약제급여목록표.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"목록": {
		{"제품코드", "제품명", "업체명", "규격", "단위", "상한금액"},
		{"641100010", "환인쿠에타핀정25mg", "환인제약(주)", "1", "정", 150},
		{"655500020", "명인쿠에타핀정25mg", "명인제약(주)", "1", "정", 120},
	}})})
	ix := s.buildNameIndex()
	for _, tc := range []struct {
		name  string
		price float64
	}{{"명인쿠에타핀정25mg", 0}, {"명인쿠에타핀정25mg", 120}, {"쿠에타핀정25mg", 120}, {"쿠에타핀정25mg", 0}} {
		code, cands := ix.match(tc.name, false, tc.price)
		if code == "641100010" {
			t.Fatalf("%q (단가 %v): 다른 회사(환인) 제품으로 확정됨 — 후보 %+v", tc.name, tc.price, cands)
		}
		if tc.name == "명인쿠에타핀정25mg" && code != "" && code != "655500020" {
			t.Fatalf("%q: %s", tc.name, code)
		}
	}
	// 회사 이름이 맞으면 그 제품으로
	if code, _ := ix.match("환인쿠에타핀정25mg", false, 150); code != "641100010" {
		t.Fatalf("환인 이름·단가가 맞으면 환인: %q", code)
	}
}

// 뒤에서 다시 매칭하는 동안 초기재고를 새로 올리면 같은 id 가 다른 행이 된다 — 읽을 때의 이름과 다르면 지정하지 않는다
func TestResolveGuardsAgainstReusedIDs(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	init := func(name string) {
		mustImport(t, s, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-02-02", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
			{"약품명", "재고수량"},
			{name, 3},
		}})})
	}
	init("없는약품가나정")
	list, _ := s.ListUnresolved()
	if len(list) != 1 {
		t.Fatalf("준비: %+v", list)
	}
	stale := list[0] // 뒤에서 매칭할 때 읽어 둔 행
	init("전혀다른마바정")  // 그사이 초기재고를 새로 올림 → 같은 id 가 다른 행
	err = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		return resolveUnresolvedTx(tx, s, stale.ID, stale.Name, "600000010", "name")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("다른 행이 된 id 는 지정하지 않아야 함: %v", err)
	}
	var n int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM initial_line").Scan(&n)
	if n != 0 {
		t.Fatalf("엉뚱한 행이 초기재고에 들어감: %d", n)
	}
}
