package domain

import (
	"fmt"
	"sort"
	"time"
)

// 서류보상 / 실물보상 판정 엔진.
//
//   - 서류보상: 도매가 '대상기간 사입량 × 비율' 만큼 원장에서 자동 반품·재사입 처리.
//     보상액 = 서류보상수량 × 1단위 인하폭. 실제 보유량과 무관하다.
//   - 실물보상: 보유 실물의 바코드를 도매 앱에 등록 → 등록 수량만큼 보상.
//   - 두 방식은 (품목, 도매) 단위 택일. 더 많이 회수되는 쪽을 고른다.
//     실물 유리 ⇔ 해당 도매에서 온 보유수량 > 해당 도매 서류보상수량
//
// 도매별 기준은 언제든 바뀔 수 있으므로 코드가 아닌 CompRule 데이터로 표현한다.

type CompRule struct {
	RateBP RateBP `json:"rateBp"` // 3000 = 30%
	// WindowMode : 대상기간 계산 방식
	//   cycle         : 매월 마감일(CutoffDay) 기준 N개 주기. CutoffDay=0(말일)이면 '달력 월'과 같다.
	//                   예) 마감일 15일·2주기·시행일 11/1 → 8/16 ~ 10/15
	//   rolling_month : 기준일 직전 N개월 (예: 11/1 → 8/1 ~ 10/31, 11/20 → 8/20 ~ 11/19)
	//   rolling_day   : 기준일 직전 N일
	WindowMode     string `json:"windowMode"`
	Lookback       int    `json:"lookback"`       // 주기 수 / 개월 수 / 일 수
	CutoffDay      int    `json:"cutoffDay"`      // cycle 전용: 1~31, 0 = 말일
	ExtendToAnchor bool   `json:"extendToAnchor"` // cycle 전용: 마지막 마감일 이후 ~ 기준일 전날까지 포함
	Anchor         string `json:"anchor"`         // effective_date(시행일) | notice_date(고시일)
	Rounding       string `json:"rounding"`       // floor | round | ceil
	NetReturns     bool   `json:"netReturns"`     // 대상기간 반품 수량 차감
	SettlementDay  int    `json:"settlementDay"`  // 결제(정산)일 0 = 말일, 1~31 — 정산 예정일 안내용
}

// DefaultCompRule : 흔한 기준 "대부분 30%, 2달 또는 3달".
// [의사결정 필요] 도매별 실제 기준을 받기 전까지의 기본값이며, 화면에서 도매처별로 수정한다.
func DefaultCompRule() CompRule {
	return CompRule{RateBP: 3000, WindowMode: "cycle", Lookback: 3, CutoffDay: 0,
		Anchor: "effective_date", Rounding: "floor", NetReturns: true, SettlementDay: 0}
}

func (r CompRule) Validate() error {
	switch {
	case r.RateBP < 0 || r.RateBP > 10000:
		return fmt.Errorf("보상 비율은 0~100%% 사이여야 합니다")
	case r.Lookback <= 0 || r.Lookback > 400:
		return fmt.Errorf("기간은 1 이상이어야 합니다")
	case r.WindowMode != "cycle" && r.WindowMode != "rolling_month" && r.WindowMode != "rolling_day":
		return fmt.Errorf("기간 방식은 cycle|rolling_month|rolling_day")
	case r.CutoffDay < 0 || r.CutoffDay > 31:
		return fmt.Errorf("마감일은 0(말일) 또는 1~31")
	case r.SettlementDay < 0 || r.SettlementDay > 31:
		return fmt.Errorf("결제일은 0(말일) 또는 1~31")
	case r.Anchor != "effective_date" && r.Anchor != "notice_date":
		return fmt.Errorf("기준일은 effective_date|notice_date")
	case r.Rounding != "floor" && r.Rounding != "round" && r.Rounding != "ceil":
		return fmt.Errorf("반올림은 floor|round|ceil")
	}
	return nil
}

func dayLabel(d int) string {
	if d <= 0 || d >= 31 {
		return "말일"
	}
	return fmt.Sprintf("%d일", d)
}

// Describe 는 사람이 읽는 규칙 설명.
func (r CompRule) Describe() string {
	base := "시행일"
	if r.Anchor == "notice_date" {
		base = "고시일"
	}
	var period string
	switch r.WindowMode {
	case "rolling_day":
		period = fmt.Sprintf("직전 %d일", r.Lookback)
	case "rolling_month":
		period = fmt.Sprintf("직전 %d개월", r.Lookback)
	default:
		if r.CutoffDay <= 0 || r.CutoffDay >= 31 {
			period = fmt.Sprintf("직전 %d개월(달력 월)", r.Lookback)
		} else {
			period = fmt.Sprintf("직전 %d주기(매월 %d일 마감, %d일~다음달 %d일)", r.Lookback, r.CutoffDay, r.CutoffDay+1, r.CutoffDay)
		}
		if r.ExtendToAnchor {
			period += "+마감 후~기준일 전날"
		}
	}
	s := fmt.Sprintf("%s 기준 %s 사입량의 %s", base, period, r.RateBP)
	if r.NetReturns {
		s += " (반품 차감)"
	}
	return s + ", 결제 " + dayLabel(r.SettlementDay)
}

func daysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// cutoffIn 은 y년 m월의 마감일 (0 또는 말일 초과 → 말일).
func cutoffIn(y int, m time.Month, cutoffDay int) time.Time {
	first := time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) // 정규화 (m 이 0 이하/13 이상이어도 안전)
	last := daysIn(first.Year(), first.Month())
	d := cutoffDay
	if d <= 0 || d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, time.UTC)
}

// CompWindow 는 서류보상 대상기간 [from, to] (양끝 포함).
func CompWindow(r CompRule, effective time.Time, notice *time.Time) (time.Time, time.Time) {
	base := effective
	if r.Anchor == "notice_date" && notice != nil && !notice.IsZero() {
		base = *notice
	}
	switch r.WindowMode {
	case "rolling_day":
		return base.AddDate(0, 0, -r.Lookback), base.AddDate(0, 0, -1)
	case "rolling_month":
		return AddMonths(base, -r.Lookback), base.AddDate(0, 0, -1)
	}
	// cycle: 기준일 '이전'에 끝난 마지막 마감일부터 N개 주기
	end := cutoffIn(base.Year(), base.Month(), r.CutoffDay)
	if !end.Before(base) {
		end = cutoffIn(base.Year(), base.Month()-1, r.CutoffDay)
	}
	startCut := cutoffIn(end.Year(), end.Month()-time.Month(r.Lookback), r.CutoffDay)
	from := startCut.AddDate(0, 0, 1)
	if r.ExtendToAnchor {
		end = base.AddDate(0, 0, -1)
	}
	return from, end
}

// NextSettlementDate 는 t 이후(포함) 첫 결제일. day 0 = 말일.
func NextSettlementDate(t time.Time, day int) time.Time {
	c := cutoffIn(t.Year(), t.Month(), day)
	if c.Before(t) {
		c = cutoffIn(t.Year(), t.Month()+1, day)
	}
	return c
}

// DocumentQty 는 서류보상 수량. purchased/returned 는 양수.
// 정수 연산으로 반올림 오차 없이 계산한다 (90 × 30% = 정확히 27).
func DocumentQty(r CompRule, purchased, returned Qty) Qty {
	base := purchased
	if r.NetReturns {
		base -= returned
	}
	if base <= 0 || r.RateBP <= 0 {
		return 0
	}
	num := int64(base) * int64(r.RateBP) // milli × bp
	const den = int64(QtyScale) * 10000  // → 정수 단위
	q, rem := num/den, num%den
	switch r.Rounding {
	case "ceil":
		if rem > 0 {
			q++
		}
	case "round":
		if 2*rem >= den {
			q++
		}
	}
	return Units(q)
}

// PurchaseLot 은 FIFO 추정용 사입 1건. Qty < 0 이면 그 도매로의 반품.
type PurchaseLot struct {
	Date       string
	SupplierID int64
	Qty        Qty
}

// FIFOAttribution 은 보유 재고가 '가장 최근 사입분'으로 구성됐다는 가정의 도매별 추정 보유량.
// 바코드 촬영 전에 어느 도매가 실물보상 후보인지 미리 보여주는 용도다.
// 반품(Qty < 0)은 같은 도매의 그 날짜까지 사입분 중 가장 최근 것부터 지운다 — 반품한 약은 그 도매 몫이 아니다
// (예전에는 반품을 무시해, 다 반품한 도매에 보유량을 매기고 실물보상을 권했다).
// 기록보다 오래된 재고(초기재고 등)는 키 0 에 담긴다.
func FIFOAttribution(stock Qty, lots []PurchaseLot) map[int64]Qty {
	sorted := netReturns(lots)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date > sorted[j].Date })
	remaining := stock
	out := map[int64]Qty{}
	for _, l := range sorted {
		if remaining <= 0 {
			break
		}
		if l.Qty <= 0 {
			continue
		}
		take := MinQty(l.Qty, remaining)
		out[l.SupplierID] += take
		remaining -= take
	}
	if remaining > 0 {
		out[0] += remaining
	}
	return out
}

// netReturns : 날짜순(같은 날은 사입 먼저)으로 반품을 같은 도매의 남은 사입분(최근 것부터)에서 뺀 뒤 남은 사입분
func netReturns(lots []PurchaseLot) []PurchaseLot {
	ev := append([]PurchaseLot(nil), lots...)
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].Date != ev[j].Date {
			return ev[i].Date < ev[j].Date
		}
		return ev[i].Qty > ev[j].Qty
	})
	open := map[int64][]PurchaseLot{} // 도매별 남은 사입분 (날짜순)
	for _, l := range ev {
		switch {
		case l.Qty > 0:
			open[l.SupplierID] = append(open[l.SupplierID], l)
		case l.Qty < 0:
			back := -l.Qty
			st := open[l.SupplierID]
			for i := len(st) - 1; i >= 0 && back > 0; i-- {
				take := MinQty(st[i].Qty, back)
				st[i].Qty -= take
				back -= take
			}
			open[l.SupplierID] = st
		}
	}
	var out []PurchaseLot
	for _, st := range open {
		for _, l := range st {
			if l.Qty > 0 {
				out = append(out, l)
			}
		}
	}
	return out
}

// SupplierPosition 은 한 품목에 대한 도매 1곳의 보상 계산.
type SupplierPosition struct {
	SupplierID    int64     `json:"supplierId"`
	SupplierName  string    `json:"supplierName"`
	Rule          *CompRule `json:"rule"` // nil = 보상 기준 없음(보상 불가)
	RuleText      string    `json:"ruleText"`
	WindowFrom    string    `json:"windowFrom"`
	WindowTo      string    `json:"windowTo"`
	Purchased     Qty       `json:"purchased"`     // 대상기간 사입
	Returned      Qty       `json:"returned"`      // 대상기간 반품
	EstimatedHeld Qty       `json:"estimatedHeld"` // FIFO 추정 보유
	ScannedHeld   *Qty      `json:"scannedHeld"`   // 바코드로 확인된 보유 (nil = 미촬영)
	// Decide 가 채우는 값
	DocQty     Qty    `json:"docQty"`
	HeldUsed   Qty    `json:"heldUsed"`
	Method     string `json:"method"` // document | physical | none
	RecoverQty Qty    `json:"recoverQty"`
	Recovery   int64  `json:"recovery"` // 예상 보상액(원)
}

// ProductDecision 은 품목 1개의 판정 결과.
type ProductDecision struct {
	Stock          Qty                `json:"stock"`
	Delta          float64            `json:"delta"`
	GrossLoss      int64              `json:"grossLoss"` // 보상 전 손실 = 재고 × 인하폭
	TotalDocQty    Qty                `json:"totalDocQty"`
	Recommendation string             `json:"recommendation"` // document | physical_review | no_loss | no_stock
	Positions      []SupplierPosition `json:"positions"`
	RecoverQty     Qty                `json:"recoverQty"`
	Recovery       int64              `json:"recovery"` // 예상 보상액
	NetLoss        int64              `json:"netLoss"`  // 보상 후 순손실 (음수 = 이득)
	Scanned        bool               `json:"scanned"`
	// 품목 판정이 '서류보상'이어도, 도매별로 보면 실물이 더 유리한 도매가 있을 수 있다 (추가 회수 가능분)
	PhysicalUpside Qty   `json:"physicalUpside"`
	UpsideAmount   int64 `json:"upsideAmount"`
}

// Decide 는 품목 1개의 서류/실물 판정을 한다.
//
//  1. 품목 단위: 재고 ≤ 서류보상 합계 → 'document'(서류보상, 바코드 작업 불필요)
//     재고 > 서류보상 합계 → 'physical_review'(바코드로 도매 귀속 확인 필요)
//  2. 도매 단위: 보유량(촬영 결과 우선, 없으면 FIFO 추정) > 서류보상수량 → 실물, 아니면 서류
func Decide(stock Qty, delta float64, positions []SupplierPosition) ProductDecision {
	return DecideScanned(stock, delta, positions, -1)
}

// DecideScanned 는 Decide 와 같고, scannedTotal 은 이 품목에서 찍은 전체 수량(도매를 못 정한 것 포함, 모르면 -1).
// 찍는 중(찍은 수량 < 재고)에는 아직 안 찍은 몫을 FIFO 추정으로 채운다:
//
//	보유량 = max(찍은 수량, min(FIFO 추정, 찍은 수량 + 안 찍은 재고))
//
// 예전에는 하나만 찍어도 안 찍은 도매의 보유량이 0 이 되어, 찍는 도중 판정이 서류로 뒤집히고 예상 보상액이 줄었다.
// 다 찍으면(안 찍은 재고 0) 찍은 수량 그대로다.
func DecideScanned(stock Qty, delta float64, positions []SupplierPosition, scannedTotal Qty) ProductDecision {
	var totalDoc, recover Qty
	scanned := false
	rest := Qty(0) // 아직 안 찍은 재고
	if scannedTotal >= 0 && stock > scannedTotal {
		rest = stock - scannedTotal
	}
	for i := range positions {
		p := &positions[i]
		if p.ScannedHeld != nil {
			scanned = true
			p.HeldUsed = *p.ScannedHeld
			if rest > 0 {
				p.HeldUsed = MaxQty(*p.ScannedHeld, MinQty(p.EstimatedHeld, *p.ScannedHeld+rest))
			}
		} else {
			p.HeldUsed = p.EstimatedHeld
		}
		if p.Rule == nil {
			p.DocQty, p.Method, p.RecoverQty = 0, "none", 0
		} else {
			p.DocQty = DocumentQty(*p.Rule, p.Purchased, p.Returned)
			if p.HeldUsed > p.DocQty {
				p.Method, p.RecoverQty = "physical", p.HeldUsed
			} else {
				p.Method, p.RecoverQty = "document", p.DocQty
			}
		}
		p.Recovery = RoundWon(p.RecoverQty.Float() * delta)
		totalDoc += p.DocQty
		recover += p.RecoverQty
	}
	d := ProductDecision{Stock: stock, Delta: delta, TotalDocQty: totalDoc, Positions: positions,
		RecoverQty: recover, Scanned: scanned}
	if stock > 0 {
		d.GrossLoss = RoundWon(stock.Float() * delta)
	}
	switch {
	case delta <= 0:
		d.Recommendation = "no_loss"
	case stock <= 0:
		d.Recommendation = "no_stock"
	case stock <= totalDoc:
		d.Recommendation = "document"
	default:
		d.Recommendation = "physical_review"
	}
	d.Recovery = RoundWon(recover.Float() * delta)
	d.NetLoss = d.GrossLoss - d.Recovery
	for _, p := range positions {
		if p.Method == "physical" {
			d.PhysicalUpside += p.HeldUsed - p.DocQty
		}
	}
	d.UpsideAmount = RoundWon(d.PhysicalUpside.Float() * delta)
	return d
}
