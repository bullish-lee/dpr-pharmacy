package service

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"dpr/internal/domain"
)

// 데이터 점검 — 재고·단위가 이상해 보이는 곳을 자동으로 찾아 대시보드와 가져오기 결과에 알린다.
//
//	음수 재고      : 초기재고 누락·사입 자료 누락·단위 오류 신호
//	단위 확인      : 사입 단가가 고시 가격과 맞지 않거나, 조제 단위수량이 고시 규격과 다름
//	과다 재고      : 최근 6개월 사용량으로 3년 넘게 쓸 만큼 쌓인 재고 (단위·포장 수량 오류 의심)
//	자동 환산      : 사입 기록을 고시 단위로 바꾼 약 (맞는지 한 번 확인 권장)
//	급여목록 없음/오래됨, 고시 수집 실패

type HealthItem struct {
	Key      string   `json:"key"`   // negative | unit_check | overstock | unit_auto | no_master | master_old | notice_failed
	Level    string   `json:"level"` // warn | info
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Count    int      `json:"count"`
	Examples []string `json:"examples"`
	Link     string   `json:"link"` // 화면 경로
}

// 재고 화면 거르기 이름 (Link 의 ?f= 값)
const (
	FlagNegative  = "negative"
	FlagUnitCheck = "unit_check"
	FlagOverstock = "overstock"
	FlagUnitAuto  = "unit_auto"
)

const overstockMonths = 36

// monthlyUse : 최근 6개월(오늘 기준) 품목별 월평균 조제량 (캐시)
func (s *Service) monthlyUse() map[string]float64 {
	m, err := cached(s, "monthlyUse|"+s.Today(), func() (map[string]float64, error) {
		out := map[string]float64{}
		now := s.NowKST()
		from := now.AddDate(0, -6, 0).Format("2006-01-02")
		// 조제 기록이 6개월보다 짧으면 그 기간으로 나눈다 (예: 3개월 치만 올렸는데 6으로 나누면 사용량이 절반으로 보인다)
		months := 6.0
		var first, last sql.NullString
		_ = s.DB.QueryRow("SELECT MIN(date), MAX(date) FROM dispense_line WHERE date > ?", from).Scan(&first, &last)
		if f, err1 := domain.ParseDate(first.String); err1 == nil {
			if l, err2 := domain.ParseDate(last.String); err2 == nil {
				if m := l.Sub(f).Hours()/24/30.44 + 1.0/30.44; m < months {
					months = math.Max(m, 1)
				}
			}
		}
		rows, err := s.DB.Query("SELECT product_code, SUM(qty) FROM dispense_line WHERE date > ? GROUP BY product_code", from)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var c string
			var q int64
			if err := rows.Scan(&c, &q); err != nil {
				return nil, err
			}
			if q > 0 {
				out[c] = domain.Qty(q).Float() / months
			}
		}
		return out, rows.Err()
	}, cloneMap[string, float64])
	if err != nil {
		s.Log.Warn("월 사용량 조회 실패", "err", err)
		return map[string]float64{}
	}
	return m
}

// productFlags : 품목별 점검 표시 (재고 화면 거르기와 대시보드가 같은 기준을 쓴다)
func productFlags(si StockInfo, b productBrief, monthly float64) []string {
	var f []string
	if si.Qty < 0 {
		f = append(f, FlagNegative)
	}
	switch b.UnitStatus {
	case "check":
		f = append(f, FlagUnitCheck)
	case "auto_unit", "auto_pack":
		f = append(f, FlagUnitAuto)
	}
	if monthly > 0 && si.Qty.Float() > monthly*overstockMonths && si.Qty.Float() >= 30 {
		f = append(f, FlagOverstock)
	}
	return f
}

// negativeReason : 음수 재고의 가장 그럴듯한 원인 (화면에 함께 보여준다)
func negativeReason(si StockInfo) string {
	switch {
	case si.Qty >= 0:
		return ""
	case si.Inflow < 0:
		return "반품·차용출고가 사입보다 많음"
	case si.Inflow == 0 && si.AnchorQty == 0 && si.Basis != "count":
		return "사입·초기재고 기록 없음 (다른 코드로 산 약일 수 있음)"
	case si.Inflow == 0:
		return "기간 중 사입 기록 없음"
	case si.AnchorQty == 0 && si.Basis != "count":
		return "초기재고 없음"
	}
	return "조제가 사입+초기재고보다 많음 (자료 누락 의심)"
}

func hasFlag(fs []string, f string) bool {
	for _, x := range fs {
		if x == f {
			return true
		}
	}
	return false
}

// DataHealth 는 지금 데이터에서 확인할 것을 찾는다 (없으면 빈 목록). 데이터가 바뀌기 전까지 캐시.
func (s *Service) DataHealth() ([]HealthItem, error) {
	return cached(s, "health|"+s.Today(), s.dataHealthQuery, func(v []HealthItem) []HealthItem { return append([]HealthItem(nil), v...) })
}

func (s *Service) dataHealthQuery() ([]HealthItem, error) {
	stock, err := s.stockMap(s.Today(), true)
	if err != nil {
		return nil, err
	}
	briefs := s.productBriefs()
	use := s.monthlyUse()
	acked := s.acks()
	ackedN := map[string]int{}
	type ex struct {
		key  float64
		text string
	}
	buckets := map[string][]ex{}
	add := func(flag string, key float64, text string) { buckets[flag] = append(buckets[flag], ex{key, text}) }
	for code, si := range stock {
		b := briefs[code]
		name := b.Name
		if r := []rune(name); len(r) > 18 {
			name = string(r[:18]) + "…"
		}
		for _, f := range productFlags(si, b, use[code]) {
			if st, ok := acked[code+"|"+f]; ok && ackStillValid(f, st, si, b) {
				ackedN[f]++ // '확인함'으로 표시했고 그 뒤로 바뀌지 않음 → 제외
				continue
			}
			switch f {
			case FlagNegative:
				add(f, si.Qty.Float(), fmt.Sprintf("%s (%s%s — %s)", name, si.Qty, b.Unit, negativeReason(si)))
			case FlagUnitCheck:
				w := 0.0
				if si.Qty > 0 {
					w = -1 // 보유 중인 약을 먼저
				}
				add(f, w, name)
			case FlagOverstock:
				add(f, -si.Qty.Float()/use[code], fmt.Sprintf("%s (%s%s, %.0f개월분)", name, si.Qty, b.Unit, si.Qty.Float()/use[code]))
			case FlagUnitAuto:
				add(f, 0, name)
			}
		}
	}
	examples := func(flag string) []string {
		list := buckets[flag]
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].key != list[j].key {
				return list[i].key < list[j].key
			}
			return list[i].text < list[j].text
		})
		var out []string
		for i := 0; i < len(list) && i < 3; i++ {
			out = append(out, list[i].text)
		}
		return out
	}
	var items []HealthItem
	if n := len(buckets[FlagNegative]); n > 0 {
		items = append(items, HealthItem{Key: FlagNegative, Level: "warn", Count: n, Title: fmt.Sprintf("재고가 마이너스인 약 %d개", n),
			Detail:   "초기재고가 빠졌거나, 사입 자료가 빠졌거나, 단위가 맞지 않을 때 생깁니다. 실계수를 넣거나 자료를 확인하세요.",
			Examples: examples(FlagNegative), Link: "/inventory?f=" + FlagNegative})
	}
	if n := len(buckets[FlagUnitCheck]); n > 0 {
		items = append(items, HealthItem{Key: FlagUnitCheck, Level: "warn", Count: n, Title: fmt.Sprintf("단위 확인이 필요한 약 %d개", n),
			Detail:   "사입 단가가 고시 가격과 맞지 않거나(약가유연계약·일반약 매입가 포함), 조제 단위수량이 고시 규격과 다릅니다. 이 약의 손실은 고시 가격 차이로 계산합니다.",
			Examples: examples(FlagUnitCheck), Link: "/inventory?f=" + FlagUnitCheck})
	}
	if n := len(buckets[FlagOverstock]); n > 0 {
		items = append(items, HealthItem{Key: FlagOverstock, Level: "info", Count: n, Title: fmt.Sprintf("사용량에 비해 재고가 많은 약 %d개", n),
			Detail:   fmt.Sprintf("최근 6개월 사용량으로 %d개월 넘게 쓸 재고입니다. 포장 수량·단위 오류이거나 실제 재고일 수 있으니 한 번 세어 보세요.", overstockMonths),
			Examples: examples(FlagOverstock), Link: "/inventory?f=" + FlagOverstock})
	}
	if n := len(buckets[FlagUnitAuto]); n > 0 {
		items = append(items, HealthItem{Key: FlagUnitAuto, Level: "info", Count: n, Title: fmt.Sprintf("사입 단위를 자동 환산한 약 %d개", n),
			Detail:   "예: 28정 1팩 약을 '정'으로 산 기록, 100정 1통을 '1'로 적은 기록을 고시 단위로 바꿨습니다. 맞는지 한 번 확인하세요.",
			Examples: examples(FlagUnitAuto), Link: "/inventory?f=" + FlagUnitAuto})
	}
	for i := range items {
		if n := ackedN[items[i].Key]; n > 0 {
			items[i].Detail += fmt.Sprintf(" ('확인함' %d개 제외)", n)
		}
	}
	var masterDate string
	var masterN int
	_ = s.DB.QueryRow("SELECT COUNT(*), IFNULL(MAX(list_date), '') FROM drug_master WHERE "+sqlCurrentMaster).Scan(&masterN, &masterDate)
	switch {
	case masterN == 0:
		items = append(items, HealthItem{Key: "no_master", Level: "info", Title: "약제급여목록표를 아직 올리지 않았습니다",
			Detail: "심평원 월별 전체 목록을 올리면 모든 약의 고시 단위를 알아 단위 환산·이름 매칭이 정확해집니다.", Link: "/import"})
	case masterDate != "":
		if t, err := time.ParseInLocation("2006-01-02", masterDate, s.loc); err == nil && s.NowKST().Sub(t) > 40*24*time.Hour {
			items = append(items, HealthItem{Key: "master_old", Level: "info", Title: "약제급여목록표가 오래되었습니다 (" + strings.ReplaceAll(masterDate, "-", ".") + ")",
				Detail: "새 달 목록을 올리면 새로 등재된 약까지 단위를 확인합니다.", Link: "/import"})
		}
	}
	var failed int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notice WHERE status IN ('failed', 'no_attachment')").Scan(&failed)
	if failed > 0 {
		items = append(items, HealthItem{Key: "notice_failed", Level: "warn", Count: failed, Title: fmt.Sprintf("받지 못한 고시 %d건", failed),
			Detail: "자동으로 간격을 늘려 다시 시도합니다. 급하면 '고시' 화면에서 목록표 엑셀을 직접 올리세요.", Link: "/notices"})
	}
	return items, nil
}

// healthNote : 가져오기 결과에 덧붙일 한 줄 (확인할 것만).
// 처음 자료를 올리는 중(초기재고·사입·조제가 다 모이기 전)에는 재고가 당연히 틀리므로 알리지 않는다.
func (s *Service) healthNote() string {
	var hasInit, hasPurchase, hasDispense int
	_ = s.DB.QueryRow("SELECT EXISTS(SELECT 1 FROM stock_anchor WHERE source = 'initial'), EXISTS(SELECT 1 FROM purchase_line), EXISTS(SELECT 1 FROM dispense_line)").
		Scan(&hasInit, &hasPurchase, &hasDispense)
	if hasInit == 0 || hasPurchase == 0 || hasDispense == 0 {
		return ""
	}
	items, err := s.DataHealth()
	if err != nil {
		return ""
	}
	var parts []string
	for _, it := range items {
		if it.Level == "warn" && it.Count > 0 {
			parts = append(parts, it.Title)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " 확인할 것: " + strings.Join(parts, " · ") + " (대시보드 → 데이터 점검)"
}
