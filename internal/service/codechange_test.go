package service

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"dpr/internal/crawler"
	"dpr/internal/ingest"
)

// 약가기준정보의 '삭제' 품목: 변경이후코드가 없으면 급여 중단(목록에서 뺌), 있으면 코드 변경(새 코드로 이어 감)
func TestMasterFromDgamtCodeChanges(t *testing.T) {
	it := func(code, pay, eff, after, before, spec, unit string) crawler.DgamtItem {
		return crawler.DgamtItem{MdsCd: code, ItmNm: "약" + code, NomNm: spec, Unit: unit, MxCprc: "100", AdtStaDd: eff, PayTpNm: pay, ChgAfMdsCd: after, ChgBfMdsCd: before}
	}
	items := []crawler.DgamtItem{
		it("600000001", "급여", "20260101", "", "600000001", "1", "정"),          // 그대로
		it("600000002", "급여", "20200101", "", "", "1", "정"),                   // 예전엔 급여였지만
		it("600000002", "삭제", "20260101", "", "", "1", "정"),                   // 지금은 급여 중단
		it("600000003", "삭제", "20260101", "600000004", "600000003", "1", "정"), // 코드 변경 3 → 4
		it("600000004", "급여", "20260101", "", "600000003", "1", "정"),          //
		it("600000005", "삭제", "20250101", "600000006", "", "1", "정"),          // 두 번 변경 5 → 6 → 7
		it("600000006", "삭제", "20260101", "600000007", "", "1", "정"),          //
		it("600000007", "급여", "20260101", "", "", "1", "정"),                   //
		it("600000008", "삭제", "20260101", "600000009", "", "1", "정"),          // 단위가 다른 변경 → 확인 필요
		it("600000009", "급여", "20260101", "", "", "30", "정/병"),                //
		it("600000010", "삭제", "20260101", "", "", "1", "정"),                   // 새 코드 행에만 변경이전코드가 적힘 10 → 11
		it("600000011", "급여", "20260101", "", "600000010", "1", "정"),          //
		it("600000012", "삭제", "20260101", "600000013", "", "1", "정"),          // 새 코드도 끝내 삭제 → 급여 중단
		it("600000013", "삭제", "20260101", "", "", "1", "정"),                   //
		it("600000014", "급여", "20260101", "", "", "1", "정"),                   //
		it("600000014", "삭제", "20991231", "600000001", "", "1", "정"),          // 아직 시행 전 삭제 → 지금은 급여
	}
	dm := masterFromDgamt(items, "2026-11-18")
	inList := map[string]bool{}
	for _, r := range dm.Recs {
		inList[r.Code] = true
	}
	for _, c := range []string{"600000001", "600000004", "600000007", "600000009", "600000011", "600000014"} {
		if !inList[c] {
			t.Errorf("급여 품목 %s 가 목록에 없음", c)
		}
	}
	if len(dm.Recs) != 6 {
		t.Errorf("삭제 품목은 목록에서 빼야 함: %d품목", len(dm.Recs))
	}
	got := map[string]string{}
	for _, c := range dm.Changes {
		got[c.Old] = fmt.Sprintf("%s %v", c.New, c.Compatible)
	}
	want := map[string]string{
		"600000003": "600000004 true",
		"600000005": "600000007 true",
		"600000006": "600000007 true",
		"600000008": "600000009 false",
		"600000010": "600000011 true",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("코드 변경:\n got  %v\n want %v", got, want)
	}
	if dm.Removed != 3 { // 2, 12, 13
		t.Errorf("급여 중단 %d품목", dm.Removed)
	}
}

// 코드 변경: 옛 코드의 사입·재고 기록이 새 코드로 옮겨지고, 뒤에 옛 코드로 가져온 자료도 새 코드로 간다
func TestCodeChangeMergesPharmacyRecords(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mk := func(extra ...ingest.MasterRec) []ingest.MasterRec {
		recs := extra
		for i := 100; i < 1300; i++ {
			recs = append(recs, ingest.MasterRec{Code: fmt.Sprintf("6%08d", i), Name: fmt.Sprintf("약%d정", i), Spec: "1", Unit: "정", Price: 100})
		}
		return recs
	}
	oldRec := ingest.MasterRec{Code: "600000003", Name: "옛코드정", Spec: "1", Unit: "정", Price: 100}
	newRec := ingest.MasterRec{Code: "600000004", Name: "새코드정", Spec: "1", Unit: "정", Price: 100}
	diffOld := ingest.MasterRec{Code: "600000008", Name: "단위다른정", Spec: "1", Unit: "정", Price: 100}
	if _, _, _, err := s.storeDrugMaster("full", "a", "2026-10-01", mk(oldRec, diffOld), 0); err != nil {
		t.Fatal(err)
	}
	importPurchases(t, s, "p.xlsx",
		[]any{"20261002", "옛코드정", "600000003", "가나약품", "사입", "정", 1, 30, 100, 3000},
		[]any{"20261002", "단위다른정", "600000008", "가나약품", "사입", "정", 1, 10, 100, 1000})
	_, _ = s.DB.Exec("UPDATE product SET unit_factor = 1, memo = '옛 메모' WHERE code = '600000003'")

	changes := []codeChange{
		{Old: "600000003", New: "600000004", ChangedOn: "2026-11-01", Compatible: true, OldUnit: "정", NewUnit: "정"},
		{Old: "600000008", New: "600000009", ChangedOn: "2026-11-01", Compatible: false, OldUnit: "정", NewUnit: "병(30정)"},
	}
	newDiff := ingest.MasterRec{Code: "600000009", Name: "단위다른병", Spec: "30", Unit: "정/병", Price: 3000}
	_, _, _, cr, err := s.storeDrugMasterWith("next", "b", "2026-11-01", mk(newRec, newDiff), 0, changes)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Merged != 1 || len(cr.Review) != 1 || cr.Review[0].Old != "600000008" {
		t.Fatalf("옮긴 수·확인 필요: %+v", cr)
	}
	count := func(q string, args ...any) int {
		var n int
		if err := s.DB.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("SELECT COUNT(*) FROM purchase_line WHERE product_code = '600000004'"); n != 1 {
		t.Fatalf("사입 기록이 새 코드로 옮겨지지 않음: %d", n)
	}
	if n := count("SELECT COUNT(*) FROM purchase_line WHERE product_code = '600000003'") + count("SELECT COUNT(*) FROM product WHERE code = '600000003'") +
		count("SELECT COUNT(*) FROM drug_master WHERE code = '600000003'"); n != 0 {
		t.Fatalf("옛 코드가 남음: %d", n)
	}
	var memo string
	var factor float64
	_ = s.DB.QueryRow("SELECT memo, IFNULL(unit_factor, 0) FROM product WHERE code = '600000004'").Scan(&memo, &factor)
	if memo != "옛 메모" || factor != 1 {
		t.Fatalf("사용자 지정값을 이어받지 않음: memo=%q factor=%v", memo, factor)
	}
	// 단위가 다르면 옮기지 않고, 옛 코드는 '목록에서 빠진 약국 품목'으로 남는다
	if n := count("SELECT COUNT(*) FROM purchase_line WHERE product_code = '600000008'"); n != 1 {
		t.Fatalf("단위가 다른 변경을 옮김: %d", n)
	}
	if n := count("SELECT COUNT(*) FROM drug_master WHERE code = '600000008' AND in_list = 0"); n != 1 {
		t.Fatal("단위가 다른 옛 코드의 규격·단위를 남겨야 함")
	}
	if n := count("SELECT COUNT(*) FROM code_change WHERE status = 'review' AND old_code = '600000008'"); n != 1 {
		t.Fatal("확인 필요 기록 없음")
	}
	if n := count("SELECT COUNT(*) FROM change_log WHERE kind = 'code_change' AND old_value = '600000003' AND new_value = '600000004'"); n != 1 {
		t.Fatal("수정 기록 없음")
	}
	if note := codeChangeNote(cr); !strings.Contains(note, "600000008→600000009") {
		t.Fatalf("안내: %s", note)
	}
	// 옛 코드로 된 이팜 자료를 다시 가져와도 새 코드로
	importPurchases(t, s, "p2.xlsx", []any{"20261005", "옛코드정", "600000003", "가나약품", "사입", "정", 1, 10, 100, 1000})
	if n := count("SELECT COUNT(*) FROM purchase_line WHERE product_code = '600000003'"); n != 0 {
		t.Fatalf("다시 가져온 옛 코드 자료가 남음: %d", n)
	}
	if n := count("SELECT COUNT(*) FROM purchase_line WHERE product_code = '600000004'"); n != 2 {
		t.Fatalf("새 코드 사입: %d", n)
	}
}

// 두 코드에 같은 날 실사 기준점이 있으면 하나로 합친다 (최신 기준점 하나만 쓰므로 둘이면 한쪽 수량이 사라진다)
func TestMergeProductCodeAnchors(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := "2026-11-01T00:00:00Z"
	for _, a := range []struct {
		code string
		qty  int64
	}{{"600000003", 5000}, {"600000004", 7000}} {
		if _, err := s.DB.Exec("INSERT INTO stock_anchor(product_code, as_of, timing, qty, source, created_at) VALUES(?, '2026-10-31', 'end', ?, 'count', ?)", a.code, a.qty, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.Exec("INSERT INTO stock_anchor(product_code, as_of, timing, qty, source, created_at) VALUES('600000003', '2026-09-30', 'end', 1000, 'count', ?)", now); err != nil {
		t.Fatal(err)
	}
	var both bool
	if err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var err error
		both, err = mergeProductCode(tx, "600000003", "600000004", "2026-11-01")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !both {
		t.Fatal("두 코드 모두 기준점이 있었음")
	}
	var n int
	var qty int64
	_ = s.DB.QueryRow("SELECT COUNT(*), IFNULL(SUM(qty), 0) FROM stock_anchor WHERE product_code = '600000004' AND as_of = '2026-10-31'").Scan(&n, &qty)
	if n != 1 || qty != 12000 {
		t.Fatalf("같은 날 기준점 합치기: %d개 %d", n, qty)
	}
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM stock_anchor WHERE product_code = '600000004'").Scan(&n)
	if n != 2 {
		t.Fatalf("다른 날 기준점은 그대로 옮김: %d", n)
	}
}
