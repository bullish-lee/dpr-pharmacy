package service

import (
	"testing"

	"dpr/internal/domain"
)

func unitTestService(t *testing.T, master [][]any) *Service {
	t.Helper()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	rows := append([][]any{{"제품코드", "제품명", "규격", "단위", "상한금액"}}, master...)
	mustImport(t, s, ImportRequest{Kind: "drug_master", FileName: "약제급여목록표.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"목록": rows})})
	return s
}

var purchaseHdr = []any{"일자", "약품명", "약품코드", "거래처", "구분", "단위", "단위수량", "수량", "단가", "합계"}

func importPurchases(t *testing.T, s *Service, file string, rows ...[]any) {
	t.Helper()
	mustImport(t, s, ImportRequest{Kind: "purchase", FileName: file, Force: true, DataBase64: xlsxB64(t, map[string][][]any{"Sheet": append([][]any{purchaseHdr}, rows...)})})
}

func anchorQty(t *testing.T, s *Service, code, source string) domain.Qty {
	t.Helper()
	var q int64
	if err := s.DB.QueryRow("SELECT IFNULL(SUM(qty), 0) FROM stock_anchor WHERE product_code = ? AND source = ?", code, source).Scan(&q); err != nil {
		t.Fatal(err)
	}
	return domain.Qty(q)
}

func purchaseQtys(t *testing.T, s *Service, code string) map[string]domain.Qty {
	t.Helper()
	rows, err := s.DB.Query("SELECT kind, unit_label, IFNULL(pack_size, 0), qty FROM purchase_line WHERE product_code = ? ORDER BY id", code)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]domain.Qty{}
	for rows.Next() {
		var k, l string
		var p float64
		var q int64
		_ = rows.Scan(&k, &l, &p, &q)
		out[k+"|"+l+"|"+trimF(p)] += domain.Qty(q)
	}
	return out
}

// 실사 파일도 초기재고와 같은 규칙으로 고시 단위로 바꾼다 (28정 1팩 약을 '정'으로 센 실사 → 팩)
func TestCountFileUsesUnitConversion(t *testing.T) {
	s := unitTestService(t, [][]any{
		{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000},
		{"600000020", "다라연고", "100(1)", "g/통", 50},
	})
	stock := map[string][][]any{"Sheet": {
		{"약품코드", "약품명", "재고수량", "단가", "제품규격", "재고단위"},
		{"600000010", "가나팩정", 2, 6000, "28T", "정"}, // 2팩 = 56정
		{"600000020", "다라연고", 2, 5000, "", "통"},    // 2통 = 200g
	}}
	mustImport(t, s, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", DataBase64: xlsxB64(t, stock), BaselineDate: "2026-02-02"})
	mustImport(t, s, ImportRequest{Kind: "stock_count", FileName: "count.xlsx", DataBase64: xlsxB64(t, stock), AsOf: "2026-03-10", Timing: "end"})
	for code, want := range map[string]domain.Qty{"600000010": domain.QtyFromFloat(2), "600000020": domain.QtyFromFloat(200)} {
		if q := anchorQty(t, s, code, "initial"); q != want {
			t.Fatalf("%s 초기재고 %s, 기대 %s", code, q, want)
		}
		if q := anchorQty(t, s, code, "count"); q != want {
			t.Fatalf("%s 실사 파일 %s, 기대 %s (초기재고와 같아야 함)", code, q, want)
		}
	}
	fin, _ := s.stockMap("2026-03-11", true)
	if q := fin["600000010"].Qty; q != domain.QtyFromFloat(2) {
		t.Fatalf("최종 재고 %s", q)
	}
	// 같은 날 실사 파일을 다시 올리면 그 기준점을 바꾼다 (행이 쌓이지 않음)
	mustImport(t, s, ImportRequest{Kind: "stock_count", FileName: "count2.xlsx", DataBase64: xlsxB64(t, stock), AsOf: "2026-03-10", Timing: "end"})
	var lines int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM count_line").Scan(&lines)
	if lines != 2 || anchorQty(t, s, "600000010", "count") != domain.QtyFromFloat(2) {
		t.Fatalf("다시 올린 실사: 행 %d, 수량 %s", lines, anchorQty(t, s, "600000010", "count"))
	}
}

// 단가 없는 반품 묶음은 같은 단위 이름의 묶음 판단을 따른다 — 가져온 순서·전체 재계산과 상관없이 같은 결과
func TestUnitInheritanceIsOrderIndependent(t *testing.T) {
	s := unitTestService(t, [][]any{{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000}})
	importPurchases(t, s, "p.xlsx",
		[]any{"20260305", "가나팩정", "600000010", "가나약품", "사입", "팩", 1, 1, 6000, 6000},
		[]any{"20260302", "가나팩정", "600000010", "가나약품", "사입", "정", 28, 1, 6000, 6000},
		[]any{"20260310", "가나팩정", "600000010", "가나약품", "반품", "정", 1, -28, 0, 0})
	check := func(when string) {
		t.Helper()
		got := purchaseQtys(t, s, "600000010")
		if got["return|정|1"] != domain.QtyFromFloat(-1) || got["purchase|정|28"] != domain.QtyFromFloat(1) || got["purchase|팩|1"] != domain.QtyFromFloat(1) {
			t.Fatalf("%s: 28정 반품은 -1팩이어야 함: %v", when, got)
		}
	}
	check("사입 가져온 뒤")
	if err := recomputeUnits(s.DB, nil); err != nil {
		t.Fatal(err)
	}
	check("전체 재계산 뒤")
}

// 다른 단위 이름의 단가 없는 기록은 짐작해 곱하지 않고 '확인 필요'로 드러낸다
func TestUnpricedOtherLabelIsFlagged(t *testing.T) {
	s := unitTestService(t, [][]any{{"600000030", "가나펜주", "2.4", "mL/펜", 30000}})
	importPurchases(t, s, "p.xlsx",
		[]any{"20260302", "가나펜주", "600000030", "가나약품", "사입", "개", 1, 2, 30000, 60000},
		[]any{"20260310", "가나펜주", "600000030", "가나약품", "반품", "mL/펜", 2.4, -2, 0, 0})
	b := s.productBriefs()["600000030"]
	if b.UnitStatus != "check" {
		t.Fatalf("단위가 다른 단가 없는 반품은 확인 필요: %+v", b)
	}
	// 같은 고시 단위 이름이면 그대로 (정상)
	s2 := unitTestService(t, [][]any{{"600000040", "마바정", "1", "정", 100}})
	importPurchases(t, s2, "p.xlsx",
		[]any{"20260302", "마바정", "600000040", "가나약품", "사입", "박스", 30, 1, 3000, 3000},
		[]any{"20260310", "마바정", "600000040", "가나약품", "반품", "정", 1, -10, 0, 0})
	if b := s2.productBriefs()["600000040"]; b.UnitStatus != "ok" {
		t.Fatalf("고시 단위 이름('정')인 반품은 정상: %+v", b)
	}
	if got := purchaseQtys(t, s2, "600000040"); got["return|정|1"] != domain.QtyFromFloat(-10) {
		t.Fatalf("반품 수량: %v", got)
	}
}

// 단가가 없는 초기재고 행은 같은 단위의 사입 판단(사용자 지정 포함)을 따른다
func TestInitialLineWithoutPrice(t *testing.T) {
	s := unitTestService(t, [][]any{{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000}})
	importPurchases(t, s, "p.xlsx", []any{"20260302", "가나팩정", "600000010", "가나약품", "사입", "정", 28, 1, 6000, 6000})
	mustImport(t, s, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-02-02", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"약품코드", "약품명", "재고수량", "제품규격", "재고단위"},
		{"600000010", "가나팩정", 2, "28T", "정"},
	}})})
	if q := anchorQty(t, s, "600000010", "initial"); q != domain.QtyFromFloat(2) {
		t.Fatalf("단가 없는 '정' 초기재고 2팩(56정) → %s", q)
	}
	if err := s.SetUnitFactor("600000010", 0.5, "", "시험"); err != nil {
		t.Fatal(err)
	}
	if q := anchorQty(t, s, "600000010", "initial"); q != domain.QtyFromFloat(28) {
		t.Fatalf("사용자 지정 배율 0.5 를 같은 단위 초기재고도 따름 (56×0.5=28): %s", q)
	}
	// 단위가 다르고 단가도 없으면 확인 필요
	s2 := unitTestService(t, [][]any{{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000}})
	importPurchases(t, s2, "p.xlsx", []any{"20260302", "가나팩정", "600000010", "가나약품", "사입", "정", 28, 1, 6000, 6000})
	mustImport(t, s2, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-02-02", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"약품코드", "약품명", "재고수량", "재고단위"},
		{"600000010", "가나팩정", 3, "EA"},
	}})})
	if b := s2.productBriefs()["600000010"]; b.UnitStatus != "check" {
		t.Fatalf("단위를 알 수 없는 단가 없는 초기재고는 확인 필요: %+v", b)
	}
}

// 기준일을 옮기면 그 시점 고시 가격으로 초기재고 배율을 다시 정한다
func TestBaselineMoveRecomputesInitialFactors(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ex := func(q string, a ...any) {
		t.Helper()
		if _, err := s.DB.Exec(q, a...); err != nil {
			t.Fatal(q, err)
		}
	}
	ex("INSERT INTO notice(id, source, notice_no, published_at, status, created_at, updated_at) VALUES(1,'upload','2026-1','2026-05-01','parsed','','')")
	ex("INSERT INTO price_change(notice_id, product_code, kind, detail, spec, unit, price_before, price_after, effective_date) VALUES(1,'600000050','change','price_down','1','정',1000,100,'2026-06-01')")
	mustImport(t, s, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-02-02", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"약품코드", "약품명", "재고수량", "단가", "재고단위"},
		{"600000050", "가나정", 5, 1000, "정"},
	}})})
	if q := anchorQty(t, s, "600000050", "initial"); q != domain.QtyFromFloat(5) {
		t.Fatalf("3월 기준(고시 1000원): %s", q)
	}
	if err := s.SetBaseline("2026-07-01", "start"); err != nil {
		t.Fatal(err)
	}
	var f float64
	_ = s.DB.QueryRow("SELECT factor FROM initial_line WHERE product_code = '600000050'").Scan(&f)
	if f != 10 {
		t.Fatalf("7월 기준(고시 100원) → 단가 1000원은 10단위 묶음: 배율 %v", f)
	}
}

// 가져오기를 지우거나 같은 기간을 새 파일로 바꾸면 남은 기록으로 다시 판단하고, 지운 것은 수정 기록에 남는다
func TestDeleteAndReplaceRecompute(t *testing.T) {
	s := unitTestService(t, [][]any{
		{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000},
		{"600000060", "사아정", "1", "정", 100},
	})
	importPurchases(t, s, "p1.xlsx",
		[]any{"20260103", "가나팩정", "600000010", "가나약품", "사입", "정", 28, 1, 6000, 6000},
		[]any{"20260103", "사아정", "600000060", "가나약품", "사입", "정", 1, 10, 100, 1000})
	if b := s.productBriefs()["600000010"]; b.UnitStatus != "auto_unit" {
		t.Fatalf("준비: %+v", b)
	}
	// 1월을 가나팩정이 빠진 파일로 교체 → 가나팩정은 사입 기록이 없어졌으니 자동 환산 표시도 사라져야 함
	importPurchases(t, s, "p1b.xlsx", []any{"20260103", "사아정", "600000060", "가나약품", "사입", "정", 1, 10, 100, 1000})
	if b := s.productBriefs()["600000010"]; b.UnitStatus == "auto_unit" {
		t.Fatalf("교체로 빠진 약도 다시 판단: %+v", b)
	}
	// 2월 반품(단가 없음)은 1월 '정' 사입 판단(1/28)을 따른다 → 1월 파일을 지우면 근거가 없어진다
	importPurchases(t, s, "p2.xlsx", []any{"20260102", "가나팩정", "600000010", "가나약품", "사입", "정", 28, 1, 6000, 6000})
	importPurchases(t, s, "p3.xlsx", []any{"20260210", "가나팩정", "600000010", "가나약품", "반품", "정", 1, -28, 0, 0})
	if got := purchaseQtys(t, s, "600000010"); got["return|정|1"] != domain.QtyFromFloat(-1) {
		t.Fatalf("준비: %v", got)
	}
	list, _ := s.ListImports(100)
	var p2 int64
	for _, b := range list {
		if b.FileName == "p2.xlsx" {
			p2 = b.ID
		}
	}
	if p2 == 0 {
		t.Fatalf("가져오기 목록: %+v", list)
	}
	if err := s.DeleteImport(p2); err != nil {
		t.Fatal(err)
	}
	if got := purchaseQtys(t, s, "600000010"); got["return|정|1"] != domain.QtyFromFloat(-28) {
		t.Fatalf("근거가 사라진 반품은 원본 그대로(다시 계산됨): %v", got)
	}
	log, _ := s.ChangeLog(ChangeFilter{Kind: "import_delete"})
	if len(log) != 1 {
		t.Fatalf("가져오기 삭제 기록: %+v", log)
	}
}

// 미확인 행을 나중에(재매칭·직접 지정) 약에 연결해도 원본 단위를 지켜, 가져온 순서와 상관없이 같은 수량이 된다
func TestLateResolvedInitialRowKeepsUnit(t *testing.T) {
	initSheet := map[string][][]any{"Sheet": {
		{"약품명", "재고수량", "재고단위"},
		{"GN팩 28T", 56, "정"}, // 코드·단가 없음, 56정 = 2팩
	}}
	purchase := []any{"20260302", "GN팩 28T", "600000010", "가나약품", "사입", "정", 28, 1, 6000, 6000}
	master := [][]any{{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000}}

	// 순서 1: 사입 먼저 → 초기재고를 올릴 때 바로 맞춰짐
	a := unitTestService(t, master)
	importPurchases(t, a, "p.xlsx", purchase)
	mustImport(t, a, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-02-02", DataBase64: xlsxB64(t, initSheet)})
	// 순서 2: 초기재고 먼저(미확인으로 남음) → 사입을 올리면 재매칭으로 연결
	b := unitTestService(t, master)
	mustImport(t, b, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-02-02", DataBase64: xlsxB64(t, initSheet)})
	importPurchases(t, b, "p.xlsx", purchase)
	// 순서 3: 초기재고 먼저 → 사용자가 직접 지정
	c := unitTestService(t, master)
	mustImport(t, c, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-02-02", DataBase64: xlsxB64(t, initSheet)})
	if list, _ := c.ListUnresolved(); len(list) == 1 {
		if err := c.ResolveUnresolved(list[0].ID, "600000010"); err != nil {
			t.Fatal(err)
		}
	}
	importPurchases(t, c, "p.xlsx", purchase)
	for label, s := range map[string]*Service{"사입 먼저": a, "초기재고 먼저(재매칭)": b, "직접 지정": c} {
		if q := anchorQty(t, s, "600000010", "initial"); q != domain.QtyFromFloat(2) {
			t.Fatalf("%s: 초기재고 %s팩 (2팩이어야 함)", label, q)
		}
	}
}

// 바코드 기본 수량(포장 1개 = 고시 단위 몇 개)은 실제로 산 포장으로 정한다 — 반품만 있는 묶음이 정하면 안 됨
func TestCanonPackIgnoresReturnOnlyGroups(t *testing.T) {
	s := unitTestService(t, [][]any{{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000}})
	importPurchases(t, s, "p.xlsx",
		[]any{"20260305", "가나팩정", "600000010", "가나약품", "사입", "팩", 1, 1, 6000, 6000},
		[]any{"20260302", "가나팩정", "600000010", "가나약품", "사입", "정", 28, 1, 6000, 6000},
		[]any{"20260310", "가나팩정", "600000010", "가나약품", "반품", "정", 1, -28, 0, 0})
	var canon float64
	_ = s.DB.QueryRow("SELECT IFNULL(canon_pack, 0) FROM product WHERE code = '600000010'").Scan(&canon)
	if canon != 1 {
		t.Fatalf("포장 1개 = 1팩이어야 함: %v", canon)
	}
}
