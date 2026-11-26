package service

import (
	"fmt"
	"strings"
	"testing"
)

// 사입현황(엑셀)과 순서로 맞추기 — 가상 자료.
// 휴대폰으로 모니터를 찍은 사진은 화면 무늬 때문에 제조번호는 읽어도 사입일자·도매를 놓치는 줄이 많다.
// 그런 줄은 사입현황과 순서로 짝지어 날짜·도매를 채우는데, 화면에는 최근 9건만 보이고 엑셀에는 12건이 있다.

type alignRef struct {
	date string
	sup  string
	qty  int
}

var alignRefs = []alignRef{
	{"2026-10-16", "다솜도매", 210}, {"2026-09-20", "가람도매", 30}, {"2026-09-01", "나래도매", 60},
	{"2026-08-10", "가람도매", 30}, {"2026-07-05", "가람도매", 30}, {"2026-06-11", "나래도매", 60},
	{"2026-05-14", "다솜도매", 30}, {"2026-04-02", "가람도매", 30}, {"2026-03-25", "가람도매", 30},
	// 화면 아래 (사진에 없음)
	{"2026-02-11", "나래도매", 60}, {"2026-01-07", "가람도매", 30}, {"2025-12-03", "다솜도매", 30},
}

func alignTestService(t *testing.T) (*Service, map[string]int64) {
	t.Helper()
	s := lotTestService(t)
	hdr := []any{"일자", "약품명", "약품코드", "거래처", "구분", "", "거래처구분", "단위", "단위수량", "수량", "", "단가", "합계", "", "재고단위", "재고수량", "제약회사", "유효기간", "제조번호"}
	sheet := [][]any{{"사입현황 20251101 ~ 20261028"}, hdr}
	for _, p := range alignRefs {
		sheet = append(sheet, []any{strings.ReplaceAll(p.date, "-", ""), "누리정250마이크로그램(누리성분)_(0.25mg/1정)", "900000010", p.sup, "사입", "", "도매상", "정", 30, p.qty / 30, "", 19200, 19200 * p.qty / 30, "", "정", 0, "", "", ""})
	}
	mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "사입현황.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": sheet})})
	ids := map[string]int64{}
	for _, n := range []string{"가람도매", "나래도매", "다솜도매"} {
		ids[n] = supplierID(t, s, n)
	}
	return s, ids
}

// 화면의 줄(위에서 i번째)을 제조번호만 읽은 채로 만든다
func alignBlankRow(i int) LotPhotoRow {
	return LotPhotoRow{Lot: fmt.Sprintf("LOT-%d", 1001+i), Unsure: []string{"date", "supplier", "qty"}}
}

// 첫 줄만 날짜를 읽고 나머지 8줄은 날짜·도매를 못 읽음: 화면 위에서부터(최근 사입부터) 차례로 짝짓고,
// 순서로만 채운 도매는 '확인 필요'로 둔다. (전에는 엑셀의 가장 옛 8건과 짝지어 도매 4개가 틀렸고 확실한 칸으로 보였다.)
func TestAlignOrderGuessPrefersRecentAndFlagsSupplier(t *testing.T) {
	s, ids := alignTestService(t)
	rows := make([]LotPhotoRow, 9)
	for i := range rows {
		rows[i] = alignBlankRow(i)
	}
	rows[0].Date, rows[0].Unsure = "2026-10-16", []string{"supplier", "qty"}
	rows[4].SupplierRaw, rows[4].Alt = "가랑으메", map[string]string{"supplier": "기람도애"} // 두 엔진 모두 알아보지 못한 도매 글자
	s.alignWithPurchases("900000010", rows)
	if rows[4].Alt["supplier"] != "" {
		t.Errorf("알아보지 못한 글자는 짐작한 도매의 다른 후보로 남기지 않음: %v", rows[4].Alt)
	}
	for i, r := range rows {
		want := alignRefs[i]
		if r.Date != want.date || r.SupplierID != ids[want.sup] {
			t.Errorf("%d번째 줄: %s %s, 기대 %s %s", i+1, r.Date, r.SupplierName, want.date, want.sup)
		}
		if i == 0 {
			if r.Matched != "date" || isUnsure(r, "supplier") || isUnsure(r, "date") {
				t.Errorf("날짜가 같은 줄은 사입현황 값으로 확정: %+v", r)
			}
			continue
		}
		if r.Matched != "order" || !isUnsure(r, "supplier") || !isUnsure(r, "date") {
			t.Errorf("%d번째 줄: 순서로만 채운 날짜·도매는 '확인 필요'여야 함: matched=%q unsure=%v", i+1, r.Matched, r.Unsure)
		}
	}
}

// 날짜를 읽은 두 줄 사이에서 사진이 한 줄을 놓쳤다: 어느 사입이 빠졌는지 알 수 없으므로
// 순서로 채운 도매는 모두 '확인 필요' — 틀린 도매가 확실한 칸으로 보이면 제조번호가 다른 도매의 근거로 저장된다.
func TestAlignOrderGuessWithMissedRowStaysUnsure(t *testing.T) {
	s, _ := alignTestService(t)
	var rows []LotPhotoRow
	for i := 0; i < 9; i++ {
		if i == 3 {
			continue // 사진이 놓친 줄
		}
		rows = append(rows, alignBlankRow(i))
	}
	rows[0].Date, rows[0].Unsure = "2026-10-16", nil
	rows[7].Date, rows[7].Unsure = "2026-03-25", nil
	s.alignWithPurchases("900000010", rows)
	for i, r := range rows[1:7] {
		if r.SupplierID == 0 || r.Matched != "order" || !isUnsure(r, "supplier") {
			t.Errorf("사이 %d번째 줄: 짐작한 도매는 '확인 필요'여야 함: %+v", i+1, r)
		}
	}
	if rows[0].Matched != "date" || rows[7].Matched != "date" || isUnsure(rows[7], "supplier") {
		t.Errorf("날짜가 같은 줄: %+v / %+v", rows[0], rows[7])
	}
}

// 사진에서 도매를 읽었거나 날짜 숫자 일부(월·일)가 맞아 짝지은 줄의 도매는 짐작이 아니다 → 표시하지 않는다
func TestAlignSupplierWithEvidenceStaysSure(t *testing.T) {
	s, ids := alignTestService(t)
	// 도매는 읽고 날짜만 못 읽은 9줄 → 최근 9건, 도매는 확실
	rows := make([]LotPhotoRow, 9)
	for i := range rows {
		rows[i] = alignBlankRow(i)
		rows[i].SupplierID, rows[i].SupplierName, rows[i].Unsure = ids[alignRefs[i].sup], alignRefs[i].sup, []string{"date", "qty"}
	}
	s.alignWithPurchases("900000010", rows)
	for i, r := range rows {
		if r.Date != alignRefs[i].date || isUnsure(r, "supplier") || !isUnsure(r, "date") {
			t.Errorf("%d번째 줄: %s unsure=%v, 기대 %s (도매 확실·날짜 확인 필요)", i+1, r.Date, r.Unsure, alignRefs[i].date)
		}
	}
	// 날짜의 뒤 다섯 자리(6-09-01)만 읽은 줄 → 그 사입으로, 도매는 사입현황 값 그대로 확실
	one := []LotPhotoRow{alignBlankRow(2)}
	one[0].DateRaw = "60901"
	s.alignWithPurchases("900000010", one)
	if r := one[0]; r.Date != "2026-09-01" || r.SupplierID != ids["나래도매"] || isUnsure(r, "supplier") {
		t.Errorf("날짜 일부로 맞춘 줄: %+v", r)
	}
}

// 두 글자 인식이 한 줄의 도매를 서로 다른 거래처로 읽었고 날짜는 못 읽었다: 바탕 엔진이 읽은 도매로 순서를 맞춘 것만으로는
// 그 도매가 확인된 것이 아니다 → '확인 필요'와 다른 후보를 그대로 둔다. 같은 날짜로 맞춘 줄은 사입현황 값으로 확정한다.
func TestAlignKeepsEngineDisputedSupplier(t *testing.T) {
	s, ids := alignTestService(t)
	rows := make([]LotPhotoRow, 9)
	for i := range rows {
		rows[i] = alignBlankRow(i)
		rows[i].SupplierID, rows[i].SupplierName, rows[i].Unsure = ids[alignRefs[i].sup], alignRefs[i].sup, []string{"date", "qty"}
	}
	// 셋째 줄(실제로는 나래도매): 바탕 엔진은 가람도매로, 다른 엔진은 나래도매로 읽음
	dispute := func(r *LotPhotoRow) {
		r.SupplierID, r.SupplierName, r.Unsure = ids["가람도매"], "가람도매", []string{"date", "supplier", "qty"}
		r.Alt = map[string]string{"supplier": "나래도매", "supplierId": fmt.Sprint(ids["나래도매"])}
	}
	dispute(&rows[2])
	s.alignWithPurchases("900000010", rows)
	if r := rows[2]; !isUnsure(r, "supplier") || r.Alt["supplierId"] == "" {
		t.Errorf("갈린 도매로 순서만 맞춘 줄: 확인 필요·다른 후보가 남아야 함: %+v", r)
	}
	for i, r := range rows {
		if i != 2 && isUnsure(r, "supplier") {
			t.Errorf("%d번째 줄: 두 엔진이 같게 읽은 도매는 확실: %+v", i+1, r)
		}
	}
	// 같은 날짜로 맞춘 줄은 사입현황의 도매로 확정하고 다른 후보를 지운다
	one := []LotPhotoRow{alignBlankRow(2)}
	dispute(&one[0])
	one[0].Date, one[0].SupplierID, one[0].SupplierName = "2026-09-20", ids["가람도매"], "가람도매"
	s.alignWithPurchases("900000010", one)
	if r := one[0]; r.Matched != "date" || isUnsure(r, "supplier") || len(r.Alt) != 0 {
		t.Errorf("같은 날짜로 맞춘 줄: %+v", r)
	}
}
