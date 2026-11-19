package domain

import "math"

// 단가·인하폭 계산.
//
//   - 현재가(현재 매입 단가) = 사입현황의 최신 '단가 ÷ 단위수량' (포장 단가 → 1단위 단가).
//     예: 포장 단가 25,770 ÷ 30 = 1단위 859원 (= 보험약가).
//   - 고시 상한금액은 고시 '단위'(정·캡슐·관·병 …) 1개당 가격이다.
//   - 재고 단위와 고시 단위가 다르면(예: 재고가 '60관 1상자') 배율(factor)로 맞춘다.

// UnitCheck 는 재고 1단위가 고시 단위 몇 개인지 추정한 결과.
type UnitCheck struct {
	Factor float64 `json:"factor"` // 재고 1단위 = 고시 단위 Factor 개
	Status string  `json:"status"` // ok | auto_pack | check | no_reference
	Ratio  float64 `json:"ratio"`
}

// 매입가 ÷ 고시 변경전가가 이 범위면 같은 단위 (할인 매입·반올림 허용)
const sameUnitLow, sameUnitHigh = 0.70, 1.30

// DetectUnitFactor 는 현재 매입 단가와 고시 '변경 전' 가격을 비교해 단위 배율을 추정한다.
func DetectUnitFactor(currentUnitPrice, noticeBefore float64) UnitCheck {
	if currentUnitPrice <= 0 || noticeBefore <= 0 {
		return UnitCheck{Factor: 1, Status: "no_reference"}
	}
	ratio := currentUnitPrice / noticeBefore
	if ratio >= sameUnitLow && ratio <= sameUnitHigh {
		return UnitCheck{Factor: 1, Status: "ok", Ratio: ratio}
	}
	nearest := math.Round(ratio)
	if nearest >= 2 && math.Abs(ratio-nearest)/ratio < 0.03 {
		return UnitCheck{Factor: nearest, Status: "auto_pack", Ratio: ratio}
	}
	return UnitCheck{Factor: 1, Status: "check", Ratio: ratio}
}

// PriceDelta 는 재고 1단위 기준 손실 계산 결과.
type PriceDelta struct {
	CurrentUnitPrice float64 `json:"currentUnitPrice"` // 재고 1단위 현재 매입가
	NewUnitPrice     float64 `json:"newUnitPrice"`     // 재고 1단위 인하 후 상한금액
	Delta            float64 `json:"delta"`            // 1단위 손실 (음수면 0)
	Rate             float64 `json:"rate"`             // 고시 기준 인하율 (0.1 = 10%)
	PriceSource      string  `json:"priceSource"`      // purchase | initial | notice | ceiling(매입가가 변경 전 가격보다 높아 변경 전 가격으로 자름)
}

// ComputePriceDelta : 현재가 우선순위 = 최근 사입단가 → 초기재고 사입단가 → 고시 변경전가.
// 현재가는 이번 고시의 변경 전 가격을 넘지 않는다 — 마지막 사입이 이전 인하보다 앞서면(예: 730원에 산 뒤 657원, 다시 600원으로 인하)
// 이전 인하분(730→657)은 그 회차의 손실이므로, 이번 회차 인하폭은 657−600 = 57원이다 (두 회차 합 = 130원).
func ComputePriceDelta(purchaseUnitPrice, initialUnitPrice, noticeBefore, noticeAfter, factor float64) PriceDelta {
	if factor <= 0 {
		factor = 1
	}
	var cur float64
	var src string
	switch {
	case purchaseUnitPrice > 0:
		cur, src = purchaseUnitPrice, "purchase"
	case initialUnitPrice > 0:
		cur, src = initialUnitPrice, "initial"
	default:
		cur, src = noticeBefore*factor, "notice"
	}
	if ceiling := noticeBefore * factor; noticeBefore > 0 && cur > ceiling {
		cur, src = ceiling, "ceiling"
	}
	newPrice := noticeAfter * factor
	delta := cur - newPrice
	if delta < 0 {
		delta = 0
	}
	rate := 0.0
	if noticeBefore > 0 {
		rate = (noticeBefore - noticeAfter) / noticeBefore
	}
	return PriceDelta{CurrentUnitPrice: cur, NewUnitPrice: newPrice, Delta: delta, Rate: rate, PriceSource: src}
}
