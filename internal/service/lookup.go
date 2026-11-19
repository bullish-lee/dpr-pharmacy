package service

// 약품 조회 — 제품코드·약품명·바코드·주성분코드로 서로 찾는다 (약품 조회 탭).
//
//	9자리 숫자        → 제품코드 (보험코드)
//	13·14자리 / GS1   → 바코드(표준코드 매핑)로 제품코드
//	6자리 숫자+영문 3 → 주성분(일반명)코드 — 같은 성분 제품 모두
//	그 밖의 글자      → 약품명 (부분 일치 → 비슷한 이름)

import (
	"database/sql"
	"regexp"
	"sort"
	"strings"

	"dpr/internal/domain"
)

type DrugHit struct {
	Code      string     `json:"code"`
	Name      string     `json:"name"`
	Company   string     `json:"company"`
	Spec      string     `json:"spec"`      // 급여목록 규격·단위 원문 (예: '28 정/팩')
	BaseLabel string     `json:"baseLabel"` // 고시 단위 (예: '팩(28정)')
	Price     float64    `json:"price"`     // 상한금액 (급여목록, 없으면 최근 고시)
	InMaster  bool       `json:"inMaster"`  // 지금 급여목록에 있음
	Pharmacy  bool       `json:"pharmacy"`  // 이 약국의 사입·조제·재고 기록이 있음
	Stock     domain.Qty `json:"stock"`     // 지금 재고 (고시 단위)
	Unit      string     `json:"unit"`      // 재고 단위 표시
	MatchedBy string     `json:"matchedBy"` // code | barcode | ingredient | name | similar
}

var reIngredient = regexp.MustCompile(`^[0-9]{6}[A-Za-z]{3}$`)

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// nameIndexCached : 이름 사전 (데이터가 바뀌기 전까지 재사용 — 조회는 글자를 칠 때마다 부른다)
func (s *Service) nameIndexCached() *nameIndex {
	ix, _ := cached(s, "nameIndex", func() (*nameIndex, error) { return s.buildNameIndex(), nil }, func(v *nameIndex) *nameIndex { return v })
	return ix
}

// LookupDrugs : 조회어로 약을 찾는다 (최대 60개)
func (s *Service) LookupDrugs(q string) ([]DrugHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	var codes []string
	by := map[string]string{}
	add := func(code, how string) {
		if code == "" || by[code] != "" || len(codes) >= 60 {
			return
		}
		by[code] = how
		codes = append(codes, code)
	}
	queryCodes := func(how, query string, args ...any) error {
		rows, err := s.DB.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				add(c, how)
			}
		}
		return rows.Err()
	}
	digits := onlyDigits(q)
	plainDigits := digits != "" && len(digits) == len(strings.ReplaceAll(strings.ReplaceAll(q, " ", ""), "-", ""))
	switch {
	case plainDigits && len(digits) == 9: // 제품코드
		add(domain.NormalizeProductCode(digits), "code")
	case plainDigits && (len(digits) == 13 || len(digits) == 14), strings.HasPrefix(q, "]"), strings.Contains(q, "(01)"), len(q) > 20 && strings.HasPrefix(digits, "01"):
		gtin := ""
		if g, ok := domain.ParseGS1(q); ok {
			gtin = g.GTIN
		} else if len(digits) == 13 {
			gtin = "0" + digits
		} else if len(digits) == 14 {
			gtin = digits
		}
		if gtin != "" {
			if err := queryCodes("barcode", "SELECT product_code FROM gtin_map WHERE gtin = ? OR gtin = ?", gtin, strings.TrimPrefix(gtin, "0")); err != nil {
				return nil, err
			}
		}
	case reIngredient.MatchString(q): // 주성분코드
		if err := queryCodes("ingredient", "SELECT code FROM drug_master WHERE upper(ingredient_code) = upper(?) ORDER BY IFNULL(price, 0), name", q); err != nil {
			return nil, err
		}
	case plainDigits && len(digits) >= 3: // 제품코드 앞자리
		if err := queryCodes("code", `SELECT code FROM (SELECT code FROM product WHERE code LIKE ?1 UNION SELECT code FROM drug_master WHERE code LIKE ?1)
			ORDER BY code LIMIT 60`, digits+"%"); err != nil {
			return nil, err
		}
	}
	if len(codes) == 0 && !plainDigits {
		// 이름 부분 일치 — 약국 품목 먼저, 짧은 이름 먼저
		like := "%" + strings.ReplaceAll(strings.ReplaceAll(q, "%", ""), "_", "") + "%"
		if err := queryCodes("name", `SELECT code FROM (
				SELECT p.code, p.name, 0 AS src FROM product p WHERE p.name LIKE ?1
				  AND (EXISTS(SELECT 1 FROM purchase_line WHERE product_code = p.code) OR EXISTS(SELECT 1 FROM dispense_line WHERE product_code = p.code)
				       OR EXISTS(SELECT 1 FROM stock_anchor WHERE product_code = p.code))
				UNION ALL SELECT code, name, 1 FROM drug_master WHERE name LIKE ?1
				UNION ALL SELECT code, name, 2 FROM product WHERE name LIKE ?1)
			ORDER BY src, length(name), name LIMIT 120`, like); err != nil {
			return nil, err
		}
		// 비슷한 이름 (띄어쓰기·업체 접두·용량 표기가 달라도)
		if len(codes) < 8 && len([]rune(q)) >= 2 {
			code, cands := s.nameIndexCached().match(q, false, 0)
			add(code, "similar")
			for _, c := range cands {
				add(c.Code, "similar")
			}
		}
	}
	return s.drugHits(codes, by)
}

// drugHits : 코드 목록에 이름·규격·가격·재고를 붙인다 (순서 유지)
func (s *Service) drugHits(codes []string, by map[string]string) ([]DrugHit, error) {
	if len(codes) == 0 {
		return []DrugHit{}, nil
	}
	in, args := inClause(codes)
	hits := map[string]*DrugHit{}
	for _, c := range codes {
		hits[c] = &DrugHit{Code: c, MatchedBy: by[c]}
	}
	// 목록에서 빠졌지만 약국 기록이 있어 남겨 둔 행은 정보는 보여 주되 '지금 급여목록에 있음'은 아니다
	rows, err := s.DB.Query("SELECT code, name, company, spec, unit, IFNULL(price, 0), "+sqlCurrentMaster+" FROM drug_master WHERE code IN ("+in+")", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c, name, company, spec, unit string
		var price float64
		var current bool
		if rows.Scan(&c, &name, &company, &spec, &unit, &price, &current) == nil {
			h := hits[c]
			h.Name, h.Company, h.Spec, h.Price, h.InMaster = name, company, strings.TrimSpace(spec+" "+unit), price, current
			h.BaseLabel = domain.ParsePriceUnit(spec, unit).Label
		}
	}
	rows.Close()
	// 급여목록에 없는 약: 최근 고시의 이름·규격·가격
	rows, err = s.DB.Query(`SELECT product_code, product_name, company, spec, unit, IFNULL(NULLIF(price_after, 0), price_before) FROM price_change
		WHERE product_code IN (`+in+`) ORDER BY effective_date DESC, id DESC`, args...)
	if err == nil {
		seen := map[string]bool{}
		for rows.Next() {
			var c, name, company, spec, unit string
			var price sql.NullFloat64
			if rows.Scan(&c, &name, &company, &spec, &unit, &price) != nil || seen[c] {
				continue
			}
			seen[c] = true
			if h := hits[c]; !h.InMaster {
				if h.Name == "" {
					h.Name, h.Company = name, company
				}
				if h.Spec == "" {
					h.Spec = strings.TrimSpace(spec + " " + unit)
					h.BaseLabel = domain.ParsePriceUnit(spec, unit).Label
				}
				if h.Price == 0 && price.Valid {
					h.Price = price.Float64
				}
			}
		}
		rows.Close()
	}
	briefs := s.productBriefs()
	stock, _ := s.stockMap(s.Today(), true)
	held := map[string]bool{}
	if prow, err := s.DB.Query(`SELECT DISTINCT product_code FROM purchase_line WHERE product_code IN (`+in+`)
		UNION SELECT DISTINCT product_code FROM dispense_line WHERE product_code IN (`+in+`)
		UNION SELECT DISTINCT product_code FROM stock_anchor WHERE product_code IN (`+in+`)`, append(append(append([]any{}, args...), args...), args...)...); err == nil {
		for prow.Next() {
			var c string
			if prow.Scan(&c) == nil {
				held[c] = true
			}
		}
		prow.Close()
	}
	out := make([]DrugHit, 0, len(codes))
	for _, c := range codes {
		h := hits[c]
		if b, ok := briefs[c]; ok {
			if h.Name == "" {
				h.Name, h.Company = b.Name, b.Company
			}
			if b.BaseLabel != "" {
				h.BaseLabel = b.BaseLabel
			}
			h.Unit = b.Unit
		}
		h.Pharmacy = held[c]
		if h.Pharmacy {
			h.Stock = stock[c].Qty
		}
		if h.Name == "" && !h.Pharmacy && !h.InMaster {
			continue // 아무 정보도 없는 코드
		}
		out = append(out, *h)
	}
	return out, nil
}

// ── 약 한 개 자세히 ──────────────────────────────────────────────

type BarcodeInfo struct {
	GTIN       string  `json:"gtin"`
	PackQty    float64 `json:"packQty"`    // 표준코드 제품총수량 (낱개)
	PerBarcode float64 `json:"perBarcode"` // 바코드 1개 = 고시 단위 몇 개
	Name       string  `json:"name"`
	Source     string  `json:"source"` // official(표준코드) | user(직접 확인) | heuristic
}

type PriceHistory struct {
	EffectiveDate string  `json:"effectiveDate"`
	Detail        string  `json:"detail"`
	Before        float64 `json:"before"`
	After         float64 `json:"after"`
	NoticeNo      string  `json:"noticeNo"`
	Published     string  `json:"published"`
}

type DrugDetail struct {
	DrugHit
	Route          string         `json:"route"`
	IngredientCode string         `json:"ingredientCode"`
	OTC            string         `json:"otc"`
	PayNote        string         `json:"payNote"`  // 급여구분 등 비고
	ListDate       string         `json:"listDate"` // 급여목록 적용일
	Barcodes       []BarcodeInfo  `json:"barcodes"`
	Prices         []PriceHistory `json:"prices"`
	MonthlyUse     float64        `json:"monthlyUse"` // 최근 6개월 월평균 조제량
	LastPurchase   string         `json:"lastPurchase"`
	LastPriceUnit  float64        `json:"lastPriceUnit"` // 마지막 사입 단가 (고시 단위)
	LastSupplier   string         `json:"lastSupplier"`
	LastDispense   string         `json:"lastDispense"`
	UnitStatus     string         `json:"unitStatus"`
	UnitNote       string         `json:"unitNote"`
	SameIngredient []DrugHit      `json:"sameIngredient"` // 같은 주성분코드의 다른 제품 (상한금액 순)
}

// DrugInfo : 약 한 개의 급여목록·바코드·가격 변화·약국 기록·같은 성분 제품
func (s *Service) DrugInfo(code string) (*DrugDetail, error) {
	code = domain.NormalizeProductCode(code)
	hits, err := s.drugHits([]string{code}, map[string]string{code: "code"})
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, ErrNotFound
	}
	d := &DrugDetail{DrugHit: hits[0]}
	_ = s.DB.QueryRow("SELECT route, ingredient_code, otc, note, IFNULL(list_date, '') FROM drug_master WHERE code = ?", code).
		Scan(&d.Route, &d.IngredientCode, &d.OTC, &d.PayNote, &d.ListDate)
	pu, hasUnit := priceUnitFor(s.DB, code)
	if rows, err := s.DB.Query("SELECT gtin, IFNULL(pack_qty, 0), name, source FROM gtin_map WHERE product_code = ? ORDER BY source = 'official' DESC, gtin", code); err == nil {
		for rows.Next() {
			var b BarcodeInfo
			if rows.Scan(&b.GTIN, &b.PackQty, &b.Name, &b.Source) == nil {
				if hasUnit {
					b.PerBarcode = pu.PerBarcode(b.PackQty)
				}
				d.Barcodes = append(d.Barcodes, b)
			}
		}
		rows.Close()
	}
	if rows, err := s.DB.Query(`SELECT c.effective_date, c.detail, IFNULL(c.price_before, 0), IFNULL(c.price_after, 0), n.notice_no, n.published_at
		FROM price_change c JOIN notice n ON n.id = c.notice_id WHERE c.product_code = ? ORDER BY c.effective_date DESC, n.published_at DESC LIMIT 50`, code); err == nil {
		for rows.Next() {
			var p PriceHistory
			if rows.Scan(&p.EffectiveDate, &p.Detail, &p.Before, &p.After, &p.NoticeNo, &p.Published) == nil {
				d.Prices = append(d.Prices, p)
			}
		}
		rows.Close()
	}
	if d.Pharmacy {
		d.MonthlyUse = s.monthlyUse()[code]
		_ = s.DB.QueryRow(`SELECT p.date, IFNULL(p.unit_price, 0), IFNULL(sp.name, '') FROM purchase_line p LEFT JOIN supplier sp ON sp.id = p.supplier_id
			WHERE p.product_code = ? AND p.kind = 'purchase' ORDER BY p.date DESC, p.id DESC LIMIT 1`, code).Scan(&d.LastPurchase, &d.LastPriceUnit, &d.LastSupplier)
		_ = s.DB.QueryRow("SELECT IFNULL(MAX(date), '') FROM dispense_line WHERE product_code = ?", code).Scan(&d.LastDispense)
		b := s.productBriefs()[code]
		d.UnitStatus, d.UnitNote = b.UnitStatus, b.UnitNote
	}
	if d.IngredientCode != "" {
		var codes []string
		if rows, err := s.DB.Query("SELECT code FROM drug_master WHERE ingredient_code = ? AND code <> ? ORDER BY IFNULL(price, 0), name LIMIT 40", d.IngredientCode, code); err == nil {
			for rows.Next() {
				var c string
				if rows.Scan(&c) == nil {
					codes = append(codes, c)
				}
			}
			rows.Close()
		}
		by := map[string]string{}
		for _, c := range codes {
			by[c] = "ingredient"
		}
		if same, err := s.drugHits(codes, by); err == nil {
			sort.SliceStable(same, func(i, j int) bool { // 약국 품목 먼저, 그다음 상한금액 순
				if same[i].Pharmacy != same[j].Pharmacy {
					return same[i].Pharmacy
				}
				return same[i].Price < same[j].Price
			})
			d.SameIngredient = same
		}
	}
	return d, nil
}
