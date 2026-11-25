package service

import (
	"testing"

	"dpr/internal/domain"
)

// 도매가 보상을 장부에서 '옛 가격 반품 → 같은 날 새 가격 재사입'으로 처리한 줄은
// 서류보상 대상기간 사입량과 재고의 도매 귀속(FIFO) 추정에서 빠진다 (재고 수량은 그대로)
func TestSettlePairsExcluded(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)
	const eff, code = "2026-11-01", "900000010"
	// 그 전 인하 (9/1, 1,000 → 900원) — 보상 정산 쌍은 이 인하에 대한 것 (11/1 회차 대상기간 안에 있다)
	const before, after = 1000.0, 900.0
	var nid int64
	if err := s.DB.QueryRow("SELECT notice_id FROM price_change WHERE product_code = ? LIMIT 1", code).Scan(&nid); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO price_change(notice_id, product_code, kind, detail, price_before, price_after, effective_date)
		VALUES(?, ?, 'change', 'price_down', ?, ?, '2026-09-01')`, nid, code, before, after); err != nil {
		t.Fatal(err)
	}
	sup := supplierID(t, s, "가람도매")
	var batch int64
	if err := s.DB.QueryRow("SELECT batch_id FROM purchase_line WHERE product_code = ? LIMIT 1", code).Scan(&batch); err != nil {
		t.Fatal(err)
	}
	snapshot := func() (map[int64][2]domain.Qty, domain.Qty) {
		d, err := s.ProductDetail(eff, code)
		if err != nil {
			t.Fatal(err)
		}
		m := map[int64][2]domain.Qty{}
		for _, p := range d.Positions {
			m[p.SupplierID] = [2]domain.Qty{p.Purchased, p.EstimatedHeld}
		}
		return m, d.Row.Stock
	}
	pos0, stock0 := snapshot()

	// 보상 정산 쌍 (9/1 시행 이틀 뒤, 60정) + 같은 가격 반품·재입고(교환 — 쌍 아님)
	ins := func(date, kind string, qty int64, price float64) {
		if _, err := s.DB.Exec(`INSERT INTO purchase_line(batch_id, date, product_code, supplier_id, kind, qty, unit_price, amount, raw_qty, raw_unit_price, unit_label, factor)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '정', 1)`, batch, date, code, sup, kind, qty, price, float64(qty)/1000*price, qty, price); err != nil {
			t.Fatal(err)
		}
	}
	ins("2026-09-03", "return", -60000, before)
	ins("2026-09-03", "purchase", 60000, after)
	ins("2026-11-04", "return", -5000, after) // 교환 (회차 뒤라 이 회차 계산에는 영향 없음)
	ins("2026-11-04", "purchase", 5000, after)
	if err := markSettlePairs(s.DB, nil); err != nil {
		t.Fatal(err)
	}
	var pairs, exchange int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM purchase_line WHERE settle_pair = 1 AND date = '2026-09-03'").Scan(&pairs)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM purchase_line WHERE settle_pair = 0 AND date = '2026-11-04'").Scan(&exchange)
	if pairs != 2 || exchange != 2 {
		t.Fatalf("쌍 %d줄 (기대 2), 교환 %d줄 (기대 2)", pairs, exchange)
	}
	pos1, stock1 := snapshot()
	if stock1 != stock0 {
		t.Fatalf("재고가 바뀜: %v → %v", stock0, stock1)
	}
	if pos1[sup] != pos0[sup] {
		t.Fatalf("가람도매 사입량·보유 추정이 바뀜: %v → %v", pos0[sup], pos1[sup])
	}
	// 표시를 지우면 달라진다 (위 확인이 실제로 쌍을 빼서 같은 것임을 보인다)
	if _, err := s.DB.Exec("UPDATE purchase_line SET settle_pair = 0"); err != nil {
		t.Fatal(err)
	}
	if pos2, _ := snapshot(); pos2[sup] == pos0[sup] {
		t.Fatalf("쌍을 넣어도 같음 — 시험이 쌍의 영향을 보지 못함: %v", pos2[sup])
	}
	// 사입·고시를 다시 가져오면 (recomputeUnits) 다시 표시된다
	if err := recomputeUnits(s.DB, []string{code}); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM purchase_line WHERE settle_pair = 1").Scan(&pairs)
	if pairs != 2 {
		t.Fatalf("다시 계산한 뒤 쌍 %d줄", pairs)
	}
}
