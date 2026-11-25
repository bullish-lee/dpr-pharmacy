package service

import (
	"math"
	"sort"
	"strings"

	"dpr/internal/domain"
)

// 보상 정산 쌍 찾기
//
// 도매는 약가인하 보상(서류보상·실물보상 모두)을 장부에서 이렇게 처리한다 (가상 예):
//
//	09.03  가나정  -60  1,000   ← 옛 가격으로 반품
//	09.03  가나정  +60    900   ← 같은 날 새 가격으로 다시 사입
//
// 약이 실제로 오간 것이 아니므로 이 두 줄을
//   - 다음 회차 서류보상의 '대상기간 사입량'에 넣으면 안 되고 (반품을 빼지 않는 도매면 사입량이 부풀려진다)
//   - 재고의 도매 귀속(FIFO) 추정에 넣으면 안 된다 (보상받은 약이 그날 새로 산 약처럼 보여 그 도매로 쏠린다).
//
// 알아보는 조건: 같은 날·같은 약·같은 도매, 반품 수량 = 사입 수량, 반품 단가 > 사입 단가,
// 그리고 단가 비율(반품/사입)이 그 날짜 전 180일 안에 시행된 인하 고시의 비율(인하 전/인하 후)과 0.5% 안에서 맞음.
// 단가 비율로 보므로 사입 단위(통·정)와 고시 단위가 달라도 된다. 한 줄은 한 쌍에만 쓴다.
// 같은 가격 반품·재입고(교환)는 쌍이 아니다.
func markSettlePairs(q querier, codes []string) error {
	w, args := codeFilter("product_code", codes)
	if _, err := q.Exec("UPDATE purchase_line SET settle_pair = 0 WHERE settle_pair <> 0 AND "+w, args...); err != nil {
		return err
	}
	type line struct {
		id, sup    int64
		date, code string
		qty        int64
		price      float64
	}
	rows, err := q.Query(`SELECT id, IFNULL(supplier_id, 0), date, product_code, qty, unit_price FROM purchase_line
		WHERE unit_price > 0 AND ((kind = 'return' AND qty < 0) OR (kind = 'purchase' AND qty > 0)) AND `+w+`
		AND (product_code, date) IN (SELECT product_code, date FROM purchase_line WHERE kind = 'return' AND qty < 0 AND `+w+`)
		ORDER BY id`, append(append([]any{}, args...), args...)...)
	if err != nil {
		return err
	}
	type key struct {
		code, date string
		sup        int64
	}
	rets, buys := map[key][]line{}, map[key][]line{}
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.id, &l.sup, &l.date, &l.code, &l.qty, &l.price); err != nil {
			rows.Close()
			return err
		}
		k := key{l.code, l.date, l.sup}
		if l.qty < 0 {
			rets[k] = append(rets[k], l)
		} else {
			buys[k] = append(buys[k], l)
		}
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	if len(rets) == 0 {
		return nil
	}
	// 약품별 인하 고시 (시행일, 인하 전/후 비율)
	type cut struct {
		eff   string
		ratio float64
	}
	cuts := map[string][]cut{}
	crow, err := q.Query(`SELECT product_code, effective_date, price_before, price_after FROM price_change
		WHERE detail = 'price_down' AND price_before > 0 AND price_after > 0 AND effective_date <> '' AND `+w, args...)
	if err != nil {
		return err
	}
	for crow.Next() {
		var c, eff string
		var b, a float64
		if err := crow.Scan(&c, &eff, &b, &a); err != nil {
			crow.Close()
			return err
		}
		cuts[c] = append(cuts[c], cut{eff, b / a})
	}
	if err := closeRows(crow); err != nil {
		return err
	}
	matchesCut := func(code, date string, ratio float64) bool {
		from := date
		if t, err := domain.ParseDate(date); err == nil {
			from = domain.FormatDate(t.AddDate(0, 0, -180))
		}
		for _, c := range cuts[code] {
			if c.eff <= date && c.eff >= from && math.Abs(ratio/c.ratio-1) <= 0.005 {
				return true
			}
		}
		return false
	}
	var ids []int64
	for k, rs := range rets {
		bs := buys[k]
		used := make([]bool, len(bs))
		for _, r := range rs {
			for i, b := range bs {
				if used[i] || b.qty != -r.qty || r.price <= b.price || !matchesCut(k.code, k.date, r.price/b.price) {
					continue
				}
				used[i] = true
				ids = append(ids, r.id, b.id)
				break
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		chunk := ids[start:end]
		iargs := make([]any, len(chunk))
		for i, id := range chunk {
			iargs[i] = id
		}
		in := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		if _, err := q.Exec("UPDATE purchase_line SET settle_pair = 1 WHERE id IN ("+in+")", iargs...); err != nil {
			return err
		}
	}
	return nil
}

const setSettlePairs = "settle_pair_v1"

// EnsureSettlePairs 는 업데이트 뒤 처음 켤 때 한 번, 이미 가져온 사입 기록에서 보상 정산 쌍을 찾아 표시한다.
func (s *Service) EnsureSettlePairs() error {
	if s.DB.GetSetting(setSettlePairs, "") == "1" {
		return nil
	}
	if err := markSettlePairs(s.DB, nil); err != nil {
		return err
	}
	return s.DB.SetSetting(setSettlePairs, "1")
}
