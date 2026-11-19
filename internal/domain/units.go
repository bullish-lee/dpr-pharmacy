package domain

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// 단위 체계 — 모든 수량은 '고시 단위'로 센다.
//
// 고시 단위 = 약제급여목록의 상한금액이 매겨진 단위 (규격 + 단위 열).
// 이팜 조제현황 '총수량'(청구 수량)이 이 단위다. '단위·단위수량'은 급여목록 규격과 같다.
//
//	규격 '1'      + 단위 '정'     → 1정당 가격              → 고시 단위 '정'
//	규격 '28'     + 단위 '정/팩'  → 28정 든 1팩당 가격      → 고시 단위 '팩(28정)'
//	규격 '0.4'    + 단위 'mL/관'  → 0.4mL 든 1관당 가격     → 고시 단위 '관(0.4mL)'
//	규격 '100(1)' + 단위 'g/통'   → 100g 든 통, 1g당 가격   → 고시 단위 'g' (1통 = 100)
//
// 사입현황은 이팜 약품 설정에 따라 원본 단위가 정·통·박스로 섞여 있다(예: 28정 1팩 약을 '정'으로, 100g 통을 '통'으로).
// 그래서 사입·초기재고는 '배율'(원본 1단위 = 고시 단위 몇 개)로 환산해 저장한다. 배율은 단가 비율로 정한다:
// 원본 1단위 단가 ÷ 고시 상한금액 ≈ 배율 (약국 매입가는 대부분 상한금액 근처).

// PriceUnit 은 급여목록 '규격·단위'를 해석한 고시 단위.
type PriceUnit struct {
	Spec      string  `json:"spec"`
	Unit      string  `json:"unit"`
	Measure   string  `json:"measure"`   // 'X/Y' 의 X (정·캡슐·mL·g …). 단순 단위면 그 단위
	Container string  `json:"container"` // 'X/Y' 의 Y (팩·병·통·관 …). 없으면 ''
	Size      float64 `json:"size"`      // 고시 1단위에 든 X 양 (28정/팩 → 28). 'N(1)' 이면 1
	PerCont   float64 `json:"perCont"`   // 용기 1개 = 고시 단위 몇 개 ('N(1)' → N, 그 외 1)
	Label     string  `json:"label"`     // 화면 표시: '정', '팩(28정)', 'g', '관(0.4mL)'
	Short     string  `json:"short"`     // 수량 뒤 표시: '정', '팩', 'g', '관'
	Known     bool    `json:"known"`
}

var specPerBase = regexp.MustCompile(`^\s*([\d.]+)\s*\(\s*([\d.]+)\s*\)\s*$`)

// 셀 수 있는 낱개 단위 (용기 안에 이 단위가 N개 들었으면 포장 기준으로 세는 경우가 있다)
var countMeasures = map[string]bool{"정": true, "캡슐": true, "매": true, "포": true, "개": true, "장": true, "관": true, "병": true, "앰플": true, "바이알": true, "펜": true, "키트": true}

func trimNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// ParsePriceUnit 은 급여목록·고시의 규격·단위를 해석한다. 해석할 수 없으면 Known=false.
func ParsePriceUnit(spec, unit string) PriceUnit {
	pu := PriceUnit{Spec: strings.TrimSpace(spec), Unit: strings.TrimSpace(unit), Size: 1, PerCont: 1}
	u := pu.Unit
	if u == "" {
		return pu
	}
	if i := strings.Index(u, "/"); i > 0 {
		pu.Measure, pu.Container = strings.TrimSpace(u[:i]), strings.TrimSpace(u[i+1:])
		if j := strings.IndexAny(pu.Container, "(（"); j > 0 { // 'mL/병(PP)' → 병
			pu.Container = strings.TrimSpace(pu.Container[:j])
		}
	} else {
		pu.Measure = u
	}
	if m := specPerBase.FindStringSubmatch(pu.Spec); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		per, _ := strconv.ParseFloat(m[2], 64)
		if n > 0 && per > 0 {
			pu.Size, pu.PerCont, pu.Known = per, n/per, true
		}
	} else if f, err := strconv.ParseFloat(pu.Spec, 64); err == nil && f > 0 {
		pu.Size, pu.Known = f, true
	} else if pu.Spec == "" {
		pu.Known = true
	}
	pu.Short = pu.Measure
	if pu.Container != "" && pu.PerCont <= 1 {
		pu.Short = pu.Container
	}
	switch {
	case pu.Container == "":
		pu.Label = pu.Measure
		if pu.Size != 1 {
			pu.Label = trimNum(pu.Size) + pu.Measure
		}
	case pu.PerCont > 1: // 100(1) g/통 → g
		pu.Label = pu.Measure
		if pu.Size != 1 {
			pu.Label = trimNum(pu.Size) + pu.Measure
		}
	default: // 28 정/팩 → 팩(28정)
		pu.Label = pu.Container + "(" + trimNum(pu.Size) + pu.Measure + ")"
	}
	return pu
}

// ContainerNote : 'N(1)' 형식이면 '1통 = 100g' 같은 보조 설명
func (pu PriceUnit) ContainerNote() string {
	if pu.Container != "" && pu.PerCont > 1 {
		return fmt.Sprintf("1%s = %s%s", pu.Container, trimNum(pu.PerCont*pu.Size), pu.Measure)
	}
	return ""
}

// PerBarcode : 표준코드 '제품총수량'(포장 1개에 든 낱개 수, 단위는 제형구분) → 고시 단위 수.
//
//	28 정/팩 + 제품총수량 28(정)   → 1팩
//	100(1) g/통 + 제품총수량 1(개) → 100g
//	0.4 mL/관 + 제품총수량 60(관)  → 60관
func (pu PriceUnit) PerBarcode(packQty float64) float64 {
	if packQty <= 0 {
		return 0
	}
	if !pu.Known || pu.Container == "" {
		return packQty
	}
	if pu.PerCont > 1 { // 용기 단위로 센 수량 × 용기당 고시 단위
		return packQty * pu.PerCont
	}
	if countMeasures[pu.Measure] && pu.Size > 1 { // 정·캡슐로 센 수량 ÷ 팩당 정
		return packQty / pu.Size
	}
	return packQty // mL/g 이 든 용기(병·관·펜)를 센 수량
}

// UnitDecision 은 사입·재고 원본 1단위가 고시 단위 몇 개인지(배율) 판단한 결과.
type UnitDecision struct {
	Factor float64 `json:"factor"`
	Status string  `json:"status"` // ok | auto_unit | auto_pack | check | no_reference | manual
	Ratio  float64 `json:"ratio"`  // 원본 1단위 단가 ÷ 고시 상한금액
	Note   string  `json:"note"`
}

// 같은 단위로 보는 단가 비율 범위. 매입 할인·인하 직전 가격 차이를 허용한다.
// 포장 보정(정수배 k ≥ 2)과 겹치지 않는다.
const unitOKLow, unitOKHigh = 0.65, 1.55

func within(ratio, target float64) bool {
	if target <= 0 || ratio <= 0 {
		return false
	}
	q := ratio / target
	return q >= unitOKLow && q <= unitOKHigh
}

// DecideFactor 는 단가 비율과 고시 단위 구조로 배율을 정한다.
//
//  1. 비율이 1 근처(0.65~1.55)                         → 1 (같은 단위)
//  2. 고시 구조가 설명하는 배율 근처 (N(1) → N, N개들이 → 1/N) → 그 배율 (auto_unit)
//  3. 비율이 정수 k(≥2)에 3% 이내이고, k가 알려진 포장 수량이거나 10 이상 → k (auto_pack: 이팜 단위수량 오기 보정)
//  4. 그 외 → 1, 확인 필요 (약가유연계약·환급·일반약 매입가 등 단위 외 원인 가능)
func DecideFactor(ratio float64, pu PriceUnit, knownPacks []float64, unitLabel string) UnitDecision {
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return UnitDecision{Factor: 1, Status: "no_reference"}
	}
	d := UnitDecision{Factor: 1, Ratio: ratio}
	type cand struct {
		f    float64
		note string
	}
	var cands []cand
	if pu.Known && pu.Container != "" {
		if pu.PerCont > 1 {
			cands = append(cands, cand{pu.PerCont, fmt.Sprintf("사입 1%s = 고시 %s%s", orDefault(unitLabel, pu.Container), trimNum(pu.PerCont*pu.Size), pu.Measure)})
		} else if pu.Size > 0 && pu.Size != 1 {
			cands = append(cands, cand{1 / pu.Size, fmt.Sprintf("사입 %s%s = 고시 1%s", trimNum(pu.Size), pu.Measure, pu.Container)})
		}
	}
	best, bestDist := 1.0, math.Abs(math.Log(ratio))
	note := ""
	for _, c := range cands {
		if dist := math.Abs(math.Log(ratio / c.f)); dist < bestDist {
			best, bestDist, note = c.f, dist, c.note
		}
	}
	if within(ratio, best) {
		d.Factor = best
		if best == 1 {
			d.Status = "ok"
		} else {
			d.Status, d.Note = "auto_unit", note
		}
		return d
	}
	if ratio >= 2 {
		k := math.Round(ratio)
		if math.Abs(ratio-k)/ratio < 0.03 && (k >= 10 || containsFloat(knownPacks, k)) {
			d.Factor, d.Status = k, "auto_pack"
			d.Note = fmt.Sprintf("사입 단가가 상한금액의 %s배 → 사입 1단위 = %s%s (이팜 단위수량 보정)", trimNum(k), trimNum(k), orDefault(pu.Label, "개"))
			return d
		}
	}
	d.Status = "check"
	d.Note = fmt.Sprintf("사입 단가가 상한금액의 %.2f배 — 단위·가격 확인 필요", ratio)
	return d
}

func containsFloat(xs []float64, v float64) bool {
	for _, x := range xs {
		if math.Abs(x-v) < 1e-9 {
			return true
		}
	}
	return false
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// Median 은 표본의 중앙값 (빈 표본은 0).
func Median(xs []float64) float64 {
	n := len(xs)
	if n == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}
