package service

import (
	"strings"
	"testing"

	"dpr/internal/domain"
)

// 합성 데이터: 고시 단위 환산 (팩 단위 고시·g 단위 고시·이팜 단위수량 오기) + 가져오기 순서 무관
func TestUnitCanonicalization(t *testing.T) {
	master := map[string][][]any{"2026년11월01일_(3)_공개용": {
		{"연번", "투여", "제품코드", "제품명", "업체명", "규격", "단위", "상한금액표 금액", "전일", "비고"},
		{1, "내복", "600000010", "가나팩정_(28정)", "가나제약", "28", "정/팩", 6000, "전문", ""},
		{2, "내복", "600000020", "다라정75마이크로그램_(75μg/1정)", "다라제약", "1", "정", 33, "전문", ""},
		{3, "내복", "600000030", "마바세립_(10g/100g)", "마바제약", "100(1)", "g/통", 768, "전문", ""},
	}}
	purchases := map[string][][]any{"Sheet": {
		{"사입현황 20260202 ~ 20260331"},
		{"일자", "약품명", "약품코드", "거래처", "구분", "단위", "단위수량", "수량", "단가", "합계"},
		{"20260302", "가나팩정_(28정)", "600000010", "가나약품", "사입", "정", 28, 3, 6000, 18000}, // 28정 × 3 = 3팩
		{"20260302", "다라정75마이크로그램", "600000020", "가나약품", "사입", "정", 1, 2, 3300, 6600},  // 100정 1통을 '1'로 기록 → 200정
		{"20260303", "마바세립", "600000030", "다라약품", "사입", "g/통", 1, 2, 76800, 153600},    // 100g 2통 = 200g
	}}
	dispenses := map[string][][]any{"Sheet": {
		{"조제현황 20260202 ~ 20260331"},
		{"일자", "약품명", "약품코드", "단위", "단위수량", "투약량", "일수", "횟수", "총수량"},
		{"20260310", "가나팩정_(28정)", "600000010", "정/팩", 28, 1, 56, 1, 2},
		{"20260310", "다라정75마이크로그램", "600000020", "정", 1, 1, 150, 1, 150},
		{"20260311", "마바세립", "600000030", "g/통", 1, 0.5, 7, 3, 10.5},
	}}
	want := map[string]struct {
		stock  float64
		unit   string
		status string
	}{
		"600000010": {1, "팩", "auto_unit"},     // 3 − 2
		"600000020": {50, "정", "auto_pack"},    // 200 − 150
		"600000030": {189.5, "g", "auto_unit"}, // 200 − 10.5
	}
	check := func(s *Service, label string) {
		t.Helper()
		inv, err := s.Inventory("", false)
		if err != nil {
			t.Fatal(err)
		}
		briefs := s.productBriefs()
		for _, r := range inv.Rows {
			w, ok := want[r.Code]
			if !ok {
				continue
			}
			b := briefs[r.Code]
			if r.Qty != domain.QtyFromFloat(w.stock) || b.Unit != w.unit || b.UnitStatus != w.status {
				t.Errorf("[%s] %s: 재고 %s %s (%s: %s), 기대 %v %s %s", label, r.Code, r.Qty, b.Unit, b.UnitStatus, b.UnitNote, w.stock, w.unit, w.status)
			}
		}
	}
	for _, masterFirst := range []bool{true, false} {
		s, err := NewTemp(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		reqs := []ImportRequest{
			{Kind: "purchase", FileName: "purchase.xlsx", DataBase64: xlsxB64(t, purchases)},
			{Kind: "dispense", FileName: "dispense.xlsx", DataBase64: xlsxB64(t, dispenses)},
		}
		m := ImportRequest{Kind: "drug_master", FileName: "약제급여목록표(2026.11.1.).xlsx", DataBase64: xlsxB64(t, master)}
		if masterFirst {
			reqs = append([]ImportRequest{m}, reqs...)
		} else {
			reqs = append(reqs, m)
		}
		for _, r := range reqs {
			mustImport(t, s, r)
		}
		label := map[bool]string{true: "급여목록 먼저", false: "급여목록 나중"}[masterFirst]
		check(s, label)
		// 바코드 1개 = 고시 단위: 28정 PTP → 1팩, 100g 1병 → 100g
		if _, err := s.DB.Exec("INSERT INTO gtin_map(gtin, product_code, created_at, source, pack_qty) VALUES('08800000000101', '600000010', '', 'official', 28), ('08800000000301', '600000030', '', 'official', 1)"); err != nil {
			t.Fatal(err)
		}
		if n := s.barcodeUnits("600000010", "08800000000101"); n != 1 {
			t.Errorf("[%s] 팩 바코드 = %v팩, 기대 1", label, n)
		}
		if n := s.barcodeUnits("600000030", "08800000000301"); n != 100 {
			t.Errorf("[%s] 통 바코드 = %vg, 기대 100", label, n)
		}
		// 사용자 지정 배율 → 자동으로 되돌리기
		if err := s.SetUnitFactor("600000020", 1, "", "test"); err != nil {
			t.Fatal(err)
		}
		if inv, _ := s.Inventory("다라", false); len(inv.Rows) != 1 || inv.Rows[0].Qty != domain.Units(-148) {
			t.Errorf("[%s] 사용자 배율 1: %+v", label, inv.Rows)
		}
		if err := s.SetUnitFactor("600000020", 0, "", "test"); err != nil {
			t.Fatal(err)
		}
		check(s, label+"·자동 복귀")
		s.Close()
	}
}

// 일괄 저장 때 내렸다 다시 만드는 색인이 마이그레이션 정의와 같아야 한다 (어긋나면 색인이 사라지거나 바뀜)
func TestBulkIndexDefinitionsMatchSchema(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for table, defs := range tableIndexes {
		rows, err := s.DB.Query("SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND sql IS NOT NULL", table)
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]bool{}
		for rows.Next() {
			var q string
			_ = rows.Scan(&q)
			have[strings.Join(strings.Fields(q), " ")] = true
		}
		rows.Close()
		if len(have) != len(defs) {
			t.Fatalf("%s: 스키마 색인 %d개, 일괄 저장 정의 %d개", table, len(have), len(defs))
		}
		for _, d := range defs {
			if !have[strings.Join(strings.Fields(d), " ")] {
				t.Fatalf("%s: 정의가 스키마와 다름: %s (스키마: %v)", table, d, have)
			}
		}
	}
}

// 합성: 팩 단위 고시 약을 낱알(정)·포장으로 세어 넣어도 고시 단위(팩)로 저장된다
func TestCountInOtherUnits(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	master := map[string][][]any{"목록": {
		{"제품코드", "제품명", "규격", "단위", "상한금액"},
		{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000},
		{"600000030", "마바세립_(10g/100g)", "100(1)", "g/통", 768},
	}}
	mustImport(t, s, ImportRequest{Kind: "drug_master", FileName: "약제급여목록표.xlsx", DataBase64: xlsxB64(t, master)})
	_, _ = s.DB.Exec("INSERT INTO product(code, name, updated_at) VALUES('600000010','가나팩정',''),('600000030','마바세립','')")
	opts := s.UnitOptions("600000010")
	if len(opts) < 2 || opts[1].Key != "measure" || opts[1].Label != "정" {
		t.Fatalf("단위 선택지: %+v", opts)
	}
	if _, err := s.SaveCount(CountInput{Code: "600000010", Qty: 56, Unit: "measure"}); err != nil { // 56정 = 2팩
		t.Fatal(err)
	}
	if _, err := s.SaveCount(CountInput{Code: "600000030", Qty: 1.5, Unit: "container"}); err != nil { // 1.5통 = 150g
		t.Fatal(err)
	}
	anchors := map[string]domain.Qty{}
	rows, _ := s.DB.Query("SELECT product_code, qty FROM stock_anchor WHERE source = 'count'")
	for rows.Next() {
		var c string
		var q int64
		_ = rows.Scan(&c, &q)
		anchors[c] = domain.Qty(q)
	}
	rows.Close()
	if anchors["600000010"] != domain.Units(2) || anchors["600000030"] != domain.Units(150) {
		t.Fatalf("저장 수량: %v", anchors)
	}
	if _, err := s.SaveCount(CountInput{Code: "600000010", Qty: 1, Unit: "container"}); err == nil {
		t.Fatal("없는 단위는 거부")
	}
}

// 합성: 조제 기록의 단위수량이 고시 규격과 다르면 '단위 확인'
func TestDispenseUnitMismatch(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustImport(t, s, ImportRequest{Kind: "drug_master", FileName: "약제급여목록표.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"목록": {
		{"제품코드", "제품명", "규격", "단위", "상한금액"},
		{"600000050", "사아정_(10mg/1정)", "1", "정", 100},
	}})})
	mustImport(t, s, ImportRequest{Kind: "dispense", FileName: "d.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"일자", "약품명", "약품코드", "단위", "단위수량", "총수량"},
		{"20260310", "사아정", "600000050", "정/통", 30, 2},
	}})})
	b := s.productBriefs()["600000050"]
	if b.UnitStatus != "check" || !strings.Contains(b.UnitNote, "조제 기록의 단위수량 30") {
		t.Fatalf("조제 단위 불일치를 잡아야 함: %+v", b)
	}
}

// 합성: 포장 수량 오기 보정(×100)은 같은 기록 방식(단위수량 1)에만 — 단위수량 100 으로 적힌 반품에는 곱하지 않는다
func TestPackFixNotInheritedByOtherPackGroups(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustImport(t, s, ImportRequest{Kind: "drug_master", FileName: "약제급여목록표.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"목록": {
		{"제품코드", "제품명", "규격", "단위", "상한금액"},
		{"600000070", "자차정_(10mg/1정)", "1", "정", 33},
	}})})
	mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "p.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"일자", "약품명", "약품코드", "거래처", "구분", "단위", "단위수량", "수량", "단가", "합계"},
		{"20260302", "자차정", "600000070", "가나약품", "사입", "정", 1, 2, 3300, 6600}, // 100정 1통을 1로 → 200정
		{"20260305", "자차정", "600000070", "가나약품", "반품", "정", 100, -1, 0, 0},    // 100정 반품 → -100정 (×100 하면 안 됨)
	}})})
	inv, _ := s.Inventory("자차", false)
	if len(inv.Rows) != 1 || inv.Rows[0].Qty != domain.Units(100) {
		t.Fatalf("재고 100정이어야 함: %+v", inv.Rows)
	}
}

// 합성: 데이터 점검이 음수 재고(원인 추정 포함)·단위 확인을 찾는다
func TestDataHealth(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if items, _ := s.DataHealth(); len(items) != 1 || items[0].Key != "no_master" {
		t.Fatalf("빈 DB 는 급여목록 안내만: %+v", items)
	}
	mustImport(t, s, ImportRequest{Kind: "drug_master", FileName: "약제급여목록표(2026.11.1.).xlsx", DataBase64: xlsxB64(t, map[string][][]any{"목록": {
		{"제품코드", "제품명", "규격", "단위", "상한금액"},
		{"600000080", "카나정_(10mg/1정)", "1", "정", 100},
		{"600000090", "타파정_(5mg/1정)", "1", "정", 100},
	}})})
	mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "p.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"일자", "약품명", "약품코드", "거래처", "구분", "단위", "단위수량", "수량", "단가", "합계"},
		{"20260302", "타파정", "600000090", "가나약품", "사입", "정", 30, 1, 1080, 1080}, // 1정 36원 → 상한 100원의 0.36배 → 단위 확인
	}})})
	mustImport(t, s, ImportRequest{Kind: "dispense", FileName: "d.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"일자", "약품명", "약품코드", "총수량"},
		{"20260310", "카나정", "600000080", 10}, // 사입·초기재고 없이 조제 → 음수
	}})})
	items, err := s.DataHealth()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]HealthItem{}
	for _, it := range items {
		got[it.Key] = it
	}
	if n := got[FlagNegative]; n.Count != 1 || len(n.Examples) != 1 || !strings.Contains(n.Examples[0], "사입·초기재고 기록 없음") {
		t.Fatalf("음수 재고: %+v", n)
	}
	if u := got[FlagUnitCheck]; u.Count != 1 {
		t.Fatalf("단위 확인: %+v", u)
	}
	inv, _ := s.Inventory("", false)
	for _, r := range inv.Rows {
		if r.Code == "600000080" && (!hasFlag(r.Flags, FlagNegative) || r.Hint == "") {
			t.Fatalf("재고 화면 표시: %+v", r)
		}
	}
}

// 합성: 데이터 점검 '확인함' — 상태가 그대로면 빠지고, 바뀌면 다시 나타나며, 모든 수정이 기록된다
func TestAckAndChangeLog(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mustImport(t, s, ImportRequest{Kind: "drug_master", FileName: "약제급여목록표(2026.11.1.).xlsx", DataBase64: xlsxB64(t, map[string][][]any{"목록": {
		{"제품코드", "제품명", "규격", "단위", "상한금액"},
		{"600000080", "카나정_(10mg/1정)", "1", "정", 100},
	}})})
	mustImport(t, s, ImportRequest{Kind: "dispense", FileName: "d.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"일자", "약품명", "약품코드", "총수량"},
		{"20260310", "카나정", "600000080", 10},
	}})})
	neg := func() int {
		items, _ := s.DataHealth()
		for _, it := range items {
			if it.Key == FlagNegative {
				return it.Count
			}
		}
		return 0
	}
	if neg() != 1 {
		t.Fatal("음수 재고 1개")
	}
	if err := s.AckHealth("600000080", FlagNegative, "다른 코드로 산 약", "데이터 점검"); err != nil {
		t.Fatal(err)
	}
	if neg() != 0 {
		t.Fatal("확인함 표시 후 제외")
	}
	// 조금 바뀌면(−10 → −15, 10단위 안) 그대로 확인함
	mustImport(t, s, ImportRequest{Kind: "dispense", FileName: "d2.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"일자", "약품명", "약품코드", "총수량"},
		{"20260410", "카나정", "600000080", 5},
	}})})
	if neg() != 0 {
		t.Fatal("조금 바뀐 것은 다시 묻지 않음")
	}
	// 크게 바뀌면(−15 → −45) 다시 나타난다
	mustImport(t, s, ImportRequest{Kind: "dispense", FileName: "d3.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"일자", "약품명", "약품코드", "총수량"},
		{"20260510", "카나정", "600000080", 30},
	}})})
	if neg() != 1 {
		t.Fatal("재고가 크게 바뀌면 다시 표시")
	}
	if _, err := s.SaveCount(CountInput{Code: "600000080", Qty: 12, Source: "데이터 점검"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUnitFactor("600000080", 2, "테스트", "데이터 점검"); err != nil {
		t.Fatal(err)
	}
	log, err := s.ChangeLog(ChangeFilter{ProductCode: "600000080"})
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, e := range log {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, ",") != "unit_factor,count,ack" || log[1].NewValue != "12정" || log[1].ProductName == "" {
		t.Fatalf("수정 기록: %+v", log)
	}
}
