package service

import (
	"fmt"
	"strings"
	"testing"
)

func purchaseRows(supplier string, from, n int, date string) [][]any {
	var out [][]any
	for i := 0; i < n; i++ {
		code := fmt.Sprintf("6%08d", from+i)
		out = append(out, []any{date, "약" + code, code, supplier, "사입", "정", 1, 10, 100, 1000})
	}
	return out
}

// 도매처를 골라 내보낸 사입현황으로 같은 기간을 교체하면 다른 도매처 기록이 지워진다 → 먼저 확인
func TestSupplierFilteredExportIsGuarded(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	full := append(purchaseRows("가나약품", 1, 10, "20260105"), purchaseRows("다라약품", 11, 10, "20260106")...)
	importPurchases(t, s, "full.xlsx", full...)
	onlyA := append([][]any{purchaseHdr}, purchaseRows("가나약품", 1, 10, "20260105")...)
	onlyA = append(onlyA, []any{"20260106", "약600000001", "600000001", "가나약품", "사입", "정", 1, 5, 100, 500})
	res := mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "onlyA.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": onlyA})})
	if !res.NeedsConfirm || !strings.Contains(res.Message, "다라약품") {
		t.Fatalf("빠진 도매처가 있으면 확인해야 함: %+v", res)
	}
	var n int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM purchase_line").Scan(&n)
	if n != 20 {
		t.Fatalf("확인 전에는 아무것도 바꾸지 않음: %d행", n)
	}
	res = mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "onlyA.xlsx", Force: true, DataBase64: xlsxB64(t, map[string][][]any{"Sheet": onlyA})})
	if res.NeedsConfirm {
		t.Fatalf("'그래도 교체'면 진행: %+v", res)
	}
}

// 1월·3월 시트만 든 파일은 사이의 2월 기록을 지우지 않는다
func TestMultiSheetReplaceKeepsGap(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var all [][]any
	for _, d := range []string{"20260110", "20260210", "20260310"} {
		all = append(all, purchaseRows("가나약품", 1, 12, d)...)
	}
	importPurchases(t, s, "q1.xlsx", all...)
	book := map[string][][]any{ // 이팜 내보내기처럼 시트마다 기간 제목
		"1월": append([][]any{{"사입현황 20260101 ~ 20260131"}, purchaseHdr}, purchaseRows("가나약품", 1, 12, "20260111")...),
		"3월": append([][]any{{"사입현황 20260301 ~ 20260331"}, purchaseHdr}, purchaseRows("가나약품", 1, 12, "20260311")...),
	}
	mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "jan_mar.xlsx", DataBase64: xlsxB64(t, book)})
	var feb, total int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM purchase_line WHERE date LIKE '2026-02-%'").Scan(&feb)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM purchase_line").Scan(&total)
	if feb != 12 || total != 36 {
		t.Fatalf("2월 기록은 남아야 함: 2월 %d행, 전체 %d행", feb, total)
	}
}

// 알 수 없는 구분 값(다른 프로그램의 '전문/일반' 등)은 수량 부호로 사입·반품을 정한다 — '기타'로 두면 단가·보상 계산에서 빠진다
func TestUnknownPurchaseKindUsesSign(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res := mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "other.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		purchaseHdr,
		{"20260105", "가나정", "600000010", "가나약품", "전문", "정", 1, 10, 100, 1000},
		{"20260106", "가나정", "600000010", "가나약품", "전문", "정", 1, 12, 100, 1200},
		{"20260107", "가나정", "600000010", "가나약품", "전문", "정", 1, -2, 100, -200},
	}})})
	kinds := map[string]int{}
	rows, _ := s.DB.Query("SELECT kind FROM purchase_line")
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds[k]++
	}
	rows.Close()
	if kinds["purchase"] != 2 || kinds["return"] != 1 {
		t.Fatalf("구분: %v", kinds)
	}
	n := 0
	for _, is := range res.Issues {
		if strings.Contains(is.Message, "구분 '전문'") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("알 수 없는 구분은 값마다 한 번 알림: %d (%+v)", n, res.Issues)
	}
}

// 행을 하나도 못 읽은 시트는 제목에 기간이 있어도 그 기간 기록을 지우지 않는다
func TestUnreadSheetKeepsItsPeriod(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var all [][]any
	for _, d := range []string{"20260110", "20260210"} {
		all = append(all, purchaseRows("가나약품", 1, 12, d)...)
	}
	importPurchases(t, s, "q1.xlsx", all...)
	bad := [][]any{{"사입현황 20260201 ~ 20260228"}, purchaseHdr}
	for i := 0; i < 12; i++ {
		bad = append(bad, []any{"날짜아님", "약", "코드아님", "가나약품", "사입", "정", 1, 10, 100, 1000})
	}
	book := map[string][][]any{
		"1월": append([][]any{{"사입현황 20260101 ~ 20260131"}, purchaseHdr}, purchaseRows("가나약품", 1, 12, "20260111")...),
		"2월": bad,
	}
	res := mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "jan_badfeb.xlsx", DataBase64: xlsxB64(t, book)})
	var feb int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM purchase_line WHERE date LIKE '2026-02-%'").Scan(&feb)
	if feb != 12 {
		t.Fatalf("읽지 못한 2월 시트 때문에 2월 기록이 지워짐: %d행 남음", feb)
	}
	found := false
	for _, is := range res.Issues {
		if strings.Contains(is.Message, "2월") && strings.Contains(is.Message, "그대로") {
			found = true
		}
	}
	if !found || !strings.Contains(res.Message, "2026-01-01 ~ 2026-01-31") {
		t.Fatalf("건너뛴 시트 알림·실제 교체 기간 표시: %s / %+v", res.Message, res.Issues)
	}
}
