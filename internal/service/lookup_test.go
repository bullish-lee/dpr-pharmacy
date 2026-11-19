package service

import "testing"

// 약품 조회: 제품코드 ↔ 이름 ↔ 바코드 ↔ 주성분코드를 서로 찾는다
func TestLookupDrugs(t *testing.T) {
	s := unitTestService(t, [][]any{
		{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000},
		{"600000020", "가나정10밀리그램", "1", "정", 300},
		{"655500020", "명인가나정10밀리그램", "1", "정", 250},
	})
	_, _ = s.DB.Exec("UPDATE drug_master SET ingredient_code = '470901ATB' WHERE code IN ('600000020', '655500020')")
	_, _ = s.DB.Exec("INSERT INTO gtin_map(gtin, product_code, created_at, source, pack_qty, name) VALUES('08806000000018', '600000010', '', 'official', 28, '가나팩정')")
	importPurchases(t, s, "p.xlsx", []any{"20260302", "가나팩정", "600000010", "가나약품", "사입", "정", 28, 2, 6000, 12000})
	check := func(q, wantFirst, how string) []DrugHit {
		t.Helper()
		hits, err := s.LookupDrugs(q)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) == 0 || hits[0].Code != wantFirst || hits[0].MatchedBy != how {
			t.Fatalf("%q → %+v (기대 %s/%s)", q, hits, wantFirst, how)
		}
		return hits
	}
	h := check("600000010", "600000010", "code")[0]
	if h.Name != "가나팩정_(28정)" || h.Price != 6000 || h.BaseLabel != "팩(28정)" || !h.Pharmacy || h.Stock.Float() != 2 {
		t.Fatalf("코드 → 이름·규격·재고: %+v", h)
	}
	check("8806000000018", "600000010", "barcode")                      // EAN-13
	check("]d2010880600000001817271231101AB12", "600000010", "barcode") // GS1 DataMatrix (스캐너)
	if hits := check("470901ATB", "655500020", "ingredient"); len(hits) != 2 {
		t.Fatalf("주성분코드 → 같은 성분 2개: %+v", hits)
	}
	if hits := check("가나", "600000010", "name"); len(hits) < 3 { // 약국 품목 먼저
		t.Fatalf("이름 부분 일치: %+v", hits)
	}
	check("6000000", "600000010", "code") // 코드 앞자리
	d, err := s.DrugInfo("600000020")
	if err != nil {
		t.Fatal(err)
	}
	if d.IngredientCode != "470901ATB" || len(d.SameIngredient) != 1 || d.SameIngredient[0].Code != "655500020" {
		t.Fatalf("같은 성분 제품: %+v", d)
	}
	d, _ = s.DrugInfo("600000010")
	if len(d.Barcodes) != 1 || d.Barcodes[0].PerBarcode != 1 || d.LastPurchase != "2026-03-02" {
		t.Fatalf("바코드 1개 = 1팩, 마지막 사입: %+v", d)
	}
	if hits, _ := s.LookupDrugs("없는약품이름"); len(hits) != 0 {
		t.Fatalf("없으면 빈 목록: %+v", hits)
	}
}
