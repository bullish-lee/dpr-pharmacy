package domain

import "testing"

// 같은 약이 두 번 인하될 때 첫 인하 전에 산 약의 두 번째 인하폭은 (첫 인하 후 가격 − 두 번째 인하 후 가격) — 두 회차 합이 실제 손실과 같다
func TestPriceDeltaCappedAtCeilingBefore(t *testing.T) {
	first := ComputePriceDelta(1000, 0, 1000, 900, 1)
	second := ComputePriceDelta(1000, 0, 900, 850, 1) // 마지막 사입은 1000원 (첫 인하 전)
	if first.Delta != 100 || second.Delta != 50 || second.PriceSource != "ceiling" || second.CurrentUnitPrice != 900 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if first.Delta+second.Delta != 1000-850 {
		t.Fatal("두 회차 합이 실제 손실과 다름")
	}
	// 매입가가 변경 전 가격보다 낮으면 그대로
	if d := ComputePriceDelta(870, 0, 900, 850, 1); d.Delta != 20 || d.PriceSource != "purchase" {
		t.Fatalf("%+v", d)
	}
	// 변경 전 가격을 모르면 자르지 않는다
	if d := ComputePriceDelta(1000, 0, 0, 850, 1); d.Delta != 150 || d.PriceSource != "purchase" {
		t.Fatalf("%+v", d)
	}
}

// 반품한 약은 그 도매 몫이 아니다 — 예전에는 반품을 무시해 다 반품한 도매에 보유량을 매겼다
func TestFIFOAttributionNetsReturns(t *testing.T) {
	const A, B = 1, 2
	lots := []PurchaseLot{
		{Date: "2026-10-01", SupplierID: B, Qty: Units(60)},
		{Date: "2026-10-20", SupplierID: A, Qty: Units(100)},
		{Date: "2026-10-25", SupplierID: A, Qty: Units(-100)}, // A 에 모두 반품
	}
	got := FIFOAttribution(Units(60), lots)
	if got[A] != 0 || got[B] != Units(60) || got[0] != 0 {
		t.Fatalf("다 반품한 A 에는 보유량이 없어야 함: %v", got)
	}
	// 일부 반품: 최근 사입분부터 뺀다
	lots = []PurchaseLot{
		{Date: "2026-09-01", SupplierID: A, Qty: Units(50)},
		{Date: "2026-10-01", SupplierID: B, Qty: Units(40)},
		{Date: "2026-10-20", SupplierID: A, Qty: Units(30)},
		{Date: "2026-10-25", SupplierID: A, Qty: Units(-30)}, // 10-20 분 반품
	}
	got = FIFOAttribution(Units(60), lots)
	if got[B] != Units(40) || got[A] != Units(20) {
		t.Fatalf("일부 반품: %v", got)
	}
	// 반품은 그 날짜까지의 사입분에서만 뺀다 (뒤에 산 것은 그대로)
	lots = []PurchaseLot{
		{Date: "2026-10-01", SupplierID: A, Qty: Units(20)},
		{Date: "2026-10-05", SupplierID: A, Qty: Units(-20)},
		{Date: "2026-10-10", SupplierID: A, Qty: Units(30)},
	}
	if got = FIFOAttribution(Units(30), lots); got[A] != Units(30) {
		t.Fatalf("반품 뒤 사입: %v", got)
	}
	// 같은 날 사입·반품 → 그날 사입분에서 뺀다
	lots = []PurchaseLot{
		{Date: "2026-10-05", SupplierID: A, Qty: Units(-10)},
		{Date: "2026-10-05", SupplierID: A, Qty: Units(10)},
		{Date: "2026-10-01", SupplierID: B, Qty: Units(10)},
	}
	if got = FIFOAttribution(Units(10), lots); got[A] != 0 || got[B] != Units(10) {
		t.Fatalf("같은 날 반품: %v", got)
	}
}

// 찍는 도중에는 안 찍은 도매의 몫을 FIFO 추정으로 채운다 — 하나만 찍어도 판정이 서류로 뒤집히던 문제
func TestDecideScannedPartial(t *testing.T) {
	rule := DefaultCompRule()
	u := Units
	zero, thirty := u(0), u(30)
	pos := func(scanned *Qty) []SupplierPosition {
		return []SupplierPosition{{SupplierID: 1, Rule: &rule, Purchased: u(300), EstimatedHeld: u(240), ScannedHeld: scanned}}
	}
	before := Decide(u(240), 50, pos(nil))
	if before.Positions[0].Method != "physical" || before.Positions[0].DocQty != u(90) {
		t.Fatalf("찍기 전: %+v", before.Positions[0])
	}
	mid := DecideScanned(u(240), 50, pos(&thirty), u(30)) // 8통 중 1통 찍음
	if mid.Positions[0].Method != "physical" || mid.Positions[0].HeldUsed != u(240) || mid.Recovery != before.Recovery {
		t.Fatalf("찍는 중에는 판정·보상액이 그대로여야 함: %+v (전 %d, 중 %d)", mid.Positions[0], before.Recovery, mid.Recovery)
	}
	// 다 찍었는데 그 도매 것은 30뿐 (나머지는 도매를 못 정함) → 찍은 수량 그대로
	done := DecideScanned(u(240), 50, pos(&thirty), u(240))
	if done.Positions[0].HeldUsed != u(30) || done.Positions[0].Method != "document" {
		t.Fatalf("다 찍은 뒤: %+v", done.Positions[0])
	}
	// 두 도매: A 만 찍는 중이면 B 는 추정 보유량 유지
	ps := []SupplierPosition{
		{SupplierID: 1, Rule: &rule, Purchased: u(100), EstimatedHeld: u(40), ScannedHeld: &thirty},
		{SupplierID: 2, Rule: &rule, Purchased: u(100), EstimatedHeld: u(60), ScannedHeld: &zero},
	}
	d := DecideScanned(u(100), 10, ps, u(30))
	if d.Positions[0].HeldUsed != u(40) || d.Positions[1].HeldUsed != u(60) {
		t.Fatalf("찍는 중 두 도매: %+v", d.Positions)
	}
	d = DecideScanned(u(100), 10, ps, u(100)) // 다 찍음 (B 것은 하나도 없음)
	if d.Positions[0].HeldUsed != u(30) || d.Positions[1].HeldUsed != 0 {
		t.Fatalf("다 찍은 두 도매: %+v", d.Positions)
	}
}

func TestMatchLotExpiryByMonth(t *testing.T) {
	recs := []LotRecord{{SupplierID: 7, Lot: "AB123", Expiry: "2028-12-01"}}
	// 바코드의 유효기간 일 '00' 은 말일로 읽힌다 → 같은 달이면 같은 유효기간
	if m := MatchLot("AB123", "2028-12-31", recs); m.Status == "none" || len(m.SupplierIDs) != 1 {
		t.Fatalf("같은 달 유효기간: %+v", m)
	}
	if m := MatchLot("AB123", "2029-01-31", recs); len(m.SupplierIDs) != 0 {
		t.Fatalf("다른 달은 다른 유효기간: %+v", m)
	}
}
