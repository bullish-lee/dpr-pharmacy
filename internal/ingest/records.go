package ingest

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"dpr/internal/domain"
)

// RowIssue 는 행 단위 경고 (가져오기 결과 화면에 표시).
type RowIssue struct {
	Sheet   string `json:"sheet"`
	Row     int    `json:"row"` // 엑셀 행 번호(1부터)
	Message string `json:"message"`
}

var leadingNumber = regexp.MustCompile(`^[-+]?[\d,]*\.?\d+`)

// ParseNumber 는 '1,166', '25770', '-4', '1,860(별도합의…)' 등에서 앞쪽 숫자를 읽는다.
func ParseNumber(s string) (float64, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "원", ""))
	if s == "" {
		return 0, false
	}
	if f, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64); err == nil {
		return f, !math.IsNaN(f)
	}
	m := leadingNumber.FindString(s)
	if m == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(m, ",", ""), 64)
	return f, err == nil
}

func excelRow(t *Table, i int) int { return t.HeaderRow + 2 + i } // 데이터 i번째 → 엑셀 행 번호

// ── 사입 ──────────────────────────────────────────────────────────

type PurchaseRec struct {
	Row       int        `json:"row"`
	Date      string     `json:"date"`
	Code      string     `json:"code"`
	Name      string     `json:"name"`
	Supplier  string     `json:"supplier"` // 원본 거래처명
	Kind      string     `json:"kind"`
	Unit      string     `json:"unit"`
	PackSize  float64    `json:"packSize"`
	PackQty   float64    `json:"packQty"`
	Qty       domain.Qty `json:"qty"` // 낱개 기준, 부호 포함
	UnitPrice float64    `json:"unitPrice"`
	Amount    float64    `json:"amount"`
	Lot       string     `json:"lot"`
	Expiry    string     `json:"expiry"`
}

func signFor(kind string, q domain.Qty) domain.Qty {
	abs := q
	if abs < 0 {
		abs = -abs
	}
	switch kind {
	case "return", "disposal", "borrow_out":
		return -abs
	case "purchase", "borrow_in":
		return abs
	}
	return q
}

// ParsePurchases 는 사입현황 표를 레코드로 바꾼다. 합계·꼬리말 행은 건너뛴다.
func ParsePurchases(t *Table) ([]PurchaseRec, []RowIssue) {
	var out []PurchaseRec
	var issues []RowIssue
	unknownKinds := map[string]bool{}
	for i, row := range t.Rows {
		codeRaw := t.Get(row, "code")
		date, okDate := domain.ParseFlexibleDate(t.Get(row, "date"))
		code := domain.NormalizeProductCode(codeRaw)
		if !okDate || code == "" {
			if codeRaw != "" || (okDate && code == "") {
				issues = append(issues, RowIssue{t.Sheet, excelRow(t, i), "일자 또는 약품코드를 해석할 수 없어 건너뜀"})
			}
			continue
		}
		r := PurchaseRec{Row: excelRow(t, i), Date: date, Code: code, Name: t.Get(row, "name"),
			Supplier: t.Get(row, "supplier"), Unit: t.Get(row, "unit"), Lot: strings.TrimSpace(t.Get(row, "lot"))}
		if e, ok := domain.ParseFlexibleDate(t.Get(row, "expiry")); ok {
			r.Expiry = e
		}
		packSize, hasPackSize := ParseNumber(t.Get(row, "pack_size"))
		packQty, hasPackQty := ParseNumber(t.Get(row, "pack_qty"))
		total, hasTotal := ParseNumber(t.Get(row, "total_qty"))
		if !hasPackSize || packSize == 0 {
			packSize, hasPackSize = 1, false
		}
		switch {
		case hasTotal:
			r.Qty = domain.QtyFromFloat(total)
			if !hasPackSize && hasPackQty && packQty != 0 {
				packSize = math.Abs(total / packQty)
			}
		case hasPackQty:
			r.Qty = domain.QtyFromFloat(packSize * packQty)
		default:
			issues = append(issues, RowIssue{t.Sheet, r.Row, "수량이 없어 건너뜀"})
			continue
		}
		r.PackSize, r.PackQty = packSize, packQty
		if t.Has("kind") {
			raw := strings.TrimSpace(t.Get(row, "kind"))
			r.Kind = domain.PurchaseKind(raw)
			if r.Kind == "other" {
				// 알 수 없는 구분(빈칸, 다른 프로그램의 '전문/일반' 등) → 수량 부호로 사입·반품을 정한다
				// ('기타'로 두면 단가·포장·보상 계산에서 모두 빠진다)
				r.Kind = "purchase"
				if r.Qty < 0 {
					r.Kind = "return"
				}
				if !unknownKinds[raw] && len(unknownKinds) < 5 {
					unknownKinds[raw] = true
					issues = append(issues, RowIssue{t.Sheet, r.Row, fmt.Sprintf("구분 '%s' 을(를) 알 수 없어 수량 부호로 사입·반품을 정했습니다 (같은 값의 다른 행도 같음)", raw)})
				}
			}
		} else if r.Qty < 0 {
			r.Kind = "return"
		} else {
			r.Kind = "purchase"
		}
		r.Qty = signFor(r.Kind, r.Qty)
		price, hasPrice := ParseNumber(t.Get(row, "price"))
		r.Amount, _ = ParseNumber(t.Get(row, "amount"))
		switch {
		case hasPrice && price > 0 && packSize > 0:
			r.UnitPrice = price / packSize
		case r.Amount != 0 && r.Qty != 0:
			r.UnitPrice = math.Abs(r.Amount / r.Qty.Float())
		}
		if r.Supplier == "" {
			issues = append(issues, RowIssue{t.Sheet, r.Row, "거래처가 비어 있음"})
		}
		out = append(out, r)
	}
	return out, issues
}

// ── 조제 ──────────────────────────────────────────────────────────

type DispenseRec struct {
	Row      int        `json:"row"`
	Date     string     `json:"date"`
	Code     string     `json:"code"`
	Name     string     `json:"name"`
	Qty      domain.Qty `json:"qty"`      // 청구(고시) 단위 조제량 (총수량)
	Unit     string     `json:"unit"`     // 이팜 조제 단위 (예: '정/팩')
	UnitSize float64    `json:"unitSize"` // 이팜 조제 단위수량 (예: 28)
}

func ParseDispenses(t *Table) ([]DispenseRec, []RowIssue) {
	var out []DispenseRec
	var issues []RowIssue
	for i, row := range t.Rows {
		codeRaw := t.Get(row, "code")
		date, okDate := domain.ParseFlexibleDate(t.Get(row, "date"))
		code := domain.NormalizeProductCode(codeRaw)
		if !okDate || code == "" {
			if codeRaw != "" {
				issues = append(issues, RowIssue{t.Sheet, excelRow(t, i), "일자 또는 약품코드를 해석할 수 없어 건너뜀"})
			}
			continue
		}
		q, ok := ParseNumber(t.Get(row, "total_qty"))
		if !ok {
			issues = append(issues, RowIssue{t.Sheet, excelRow(t, i), "총수량이 없어 건너뜀"})
			continue
		}
		rec := DispenseRec{Row: excelRow(t, i), Date: date, Code: code, Name: t.Get(row, "name"), Qty: domain.QtyFromFloat(q), Unit: strings.TrimSpace(t.Get(row, "unit"))}
		if us, ok := ParseNumber(t.Get(row, "pack_size")); ok {
			rec.UnitSize = us
		}
		out = append(out, rec)
	}
	return out, issues
}

// ── 재고 (초기재고 / 실사) ─────────────────────────────────────────

type StockRec struct {
	Sheet     string     `json:"sheet"`
	Row       int        `json:"row"`
	Code      string     `json:"code"`
	Name      string     `json:"name"`
	Qty       domain.Qty `json:"qty"`       // 낱개 기준 (포장 규격 적용 후)
	RawQty    float64    `json:"rawQty"`    // 원본 숫자
	UnitPrice float64    `json:"unitPrice"` // 낱개 단가 (알 수 있으면)
	PackSize  float64    `json:"packSize"`  // 포장 단위로 센 경우 포장당 수량
	Unit      string     `json:"unit"`
	Spec      string     `json:"spec"`
	Location  string     `json:"location"`
}

// boxUnit : '60관 1상자', '30T 1BOX' 처럼 재고단위가 포장인 경우 포장당 수량
var boxUnit = regexp.MustCompile(`(?i)(\d+)\s*(관|정|t|c|캡슐|포|병|ea|개)\s*\d*\s*(상자|박스|box|bx|통|갑|팩)`)

func ParseStock(t *Table) ([]StockRec, []RowIssue) {
	var out []StockRec
	var issues []RowIssue
	for i, row := range t.Rows {
		name := strings.TrimSpace(t.Get(row, "name"))
		codeRaw := t.Get(row, "code")
		code := domain.NormalizeProductCode(codeRaw)
		if name == "" && code == "" {
			continue
		}
		r := StockRec{Sheet: t.Sheet, Row: excelRow(t, i), Code: code, Name: name, Unit: t.Get(row, "unit"),
			Spec: t.Get(row, "spec"), Location: t.Get(row, "location")}
		raw, ok := ParseNumber(t.Get(row, "qty"))
		if !ok {
			raw = 0 // 수량 칸이 비어 있으면 0 (재고 없음으로 정리된 행)
		}
		r.RawQty = raw
		price, _ := ParseNumber(t.Get(row, "unit_price"))
		pack := 0.0
		if n, ok := domain.ParsePackSize(r.Spec); ok && t.Has("spec") {
			pack = n
		} else if n, err := strconv.ParseFloat(strings.TrimSpace(r.Spec), 64); err == nil && n > 1 && t.Has("spec") {
			pack = n // 제품규격 칸에 숫자만 있는 경우 (예: '500' = 500정 1통)
		} else if m := boxUnit.FindStringSubmatch(r.Unit); m != nil {
			pack, _ = strconv.ParseFloat(m[1], 64)
		}
		if pack > 1 {
			r.PackSize = pack
			r.Qty = domain.QtyFromFloat(raw * pack)
			if price > 0 {
				r.UnitPrice = price / pack
			}
		} else {
			r.Qty = domain.QtyFromFloat(raw)
			r.UnitPrice = price
		}
		if codeRaw != "" && code == "" {
			issues = append(issues, RowIssue{t.Sheet, r.Row, fmt.Sprintf("약품코드 %q 를 해석할 수 없어 약품명으로 매칭", codeRaw)})
		}
		out = append(out, r)
	}
	return out, issues
}

// ── 사입 근거 (제조번호·유효기간) ─────────────────────────────────

type LotRec struct {
	Row      int        `json:"row"`
	Date     string     `json:"date"`
	Code     string     `json:"code"`
	Name     string     `json:"name"`
	Supplier string     `json:"supplier"`
	Qty      domain.Qty `json:"qty"`
	Lot      string     `json:"lot"`
	Expiry   string     `json:"expiry"`
}

func ParseLots(t *Table) ([]LotRec, []RowIssue) {
	var out []LotRec
	var issues []RowIssue
	for i, row := range t.Rows {
		lot := strings.TrimSpace(t.Get(row, "lot"))
		sup := strings.TrimSpace(t.Get(row, "supplier"))
		if lot == "" || sup == "" {
			continue
		}
		r := LotRec{Row: excelRow(t, i), Code: domain.NormalizeProductCode(t.Get(row, "code")), Name: t.Get(row, "name"), Supplier: sup, Lot: lot}
		r.Date, _ = domain.ParseFlexibleDate(t.Get(row, "date"))
		r.Expiry, _ = domain.ParseFlexibleDate(t.Get(row, "expiry"))
		if q, ok := ParseNumber(t.Get(row, "total_qty")); ok {
			r.Qty = domain.QtyFromFloat(q)
		} else if q, ok := ParseNumber(t.Get(row, "pack_qty")); ok {
			r.Qty = domain.QtyFromFloat(q)
		}
		out = append(out, r)
	}
	return out, issues
}

// ── 표준코드(GTIN) ↔ 보험코드 ─────────────────────────────────────

type GTINRec struct {
	GTIN string  `json:"gtin"` // GTIN-14 ('0' + 13자리 표준코드)
	Code string  `json:"code"`
	Name string  `json:"name"`
	Pack float64 `json:"pack"`
}

func NormalizeGTIN(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	switch len(d) {
	case 13:
		return "0" + d
	case 14:
		return d
	}
	return ""
}

func ParseGTINs(t *Table) ([]GTINRec, []RowIssue) {
	var out []GTINRec
	var issues []RowIssue
	for i, row := range t.Rows {
		g := NormalizeGTIN(t.Get(row, "gtin"))
		code := domain.NormalizeProductCode(t.Get(row, "code"))
		if g == "" || !domain.IsEDICode(code) {
			continue
		}
		r := GTINRec{GTIN: g, Code: code, Name: strings.TrimSpace(t.Get(row, "name"))}
		r.Pack, _ = ParseNumber(t.Get(row, "pack"))
		out = append(out, r)
		_ = i
	}
	return out, issues
}

// ── 약제급여목록표 (심평원) ───────────────────────────────────────

type MasterRec struct {
	Code           string  `json:"code"`
	Name           string  `json:"name"`
	Company        string  `json:"company"`
	Spec           string  `json:"spec"`
	Unit           string  `json:"unit"`
	Price          float64 `json:"price"`
	Route          string  `json:"route"`
	IngredientCode string  `json:"ingredientCode"`
	OTC            string  `json:"otc"`
	Note           string  `json:"note"`
}

func ParseDrugMaster(t *Table) ([]MasterRec, []RowIssue) {
	var out []MasterRec
	var issues []RowIssue
	for i, row := range t.Rows {
		code := domain.NormalizeProductCode(t.Get(row, "code"))
		if !domain.IsEDICode(code) {
			continue
		}
		r := MasterRec{Code: code, Name: strings.TrimSpace(t.Get(row, "name")), Company: strings.TrimSpace(t.Get(row, "company")),
			Spec: strings.TrimSpace(t.Get(row, "spec")), Unit: strings.TrimSpace(t.Get(row, "unit")), Route: strings.TrimSpace(t.Get(row, "route")),
			IngredientCode: strings.TrimSpace(t.Get(row, "ingredient_code")), OTC: strings.TrimSpace(t.Get(row, "otc")), Note: strings.TrimSpace(t.Get(row, "note"))}
		if p, ok := ParseNumber(t.Get(row, "price")); ok {
			r.Price = p
		} else {
			issues = append(issues, RowIssue{t.Sheet, excelRow(t, i), "상한금액이 없음: " + r.Name})
		}
		out = append(out, r)
	}
	return out, issues
}

var listDateRe = []*regexp.Regexp{
	regexp.MustCompile(`(20\d{2})\s*년\s*(\d{1,2})\s*월\s*(\d{1,2})\s*일`),
	regexp.MustCompile(`(20\d{2})[.\-](\d{1,2})[.\-](\d{1,2})`),
}

// ListDate 는 시트명·파일명에서 목록 적용일을 찾는다 ('2026년11월01일_(21,434)…', '(2026.11.1.)').
func ListDate(names ...string) string {
	for _, n := range names {
		for _, re := range listDateRe {
			if m := re.FindStringSubmatch(n); m != nil {
				y, _ := strconv.Atoi(m[1])
				mo, _ := strconv.Atoi(m[2])
				d, _ := strconv.Atoi(m[3])
				if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
					return fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
				}
			}
		}
	}
	return ""
}
