package service

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// 재고 = 최신 기준점(초기재고 또는 실계수) + 이후 사입(부호 포함) − 이후 조제
//
// 기준점 시점(timing):
//   start(영업 전) : 기준일 당일 거래부터 반영
//   end  (영업 후) : 기준일 당일 거래는 이미 반영된 수량 → 다음날부터 반영
// 초기재고 파일에 없는 품목은 '초기재고 기준일에 0개' 로 본다(기준일 시점 전체 재고 목록이므로).

type StockInfo struct {
	Code         string     `json:"code"`
	Qty          domain.Qty `json:"qty"`
	AnchorID     int64      `json:"anchorId"`
	AnchorQty    domain.Qty `json:"anchorQty"`
	AnchorDate   string     `json:"anchorDate"`
	AnchorTiming string     `json:"anchorTiming"`
	Basis        string     `json:"basis"` // count | initial | baseline | none
	Inflow       domain.Qty `json:"inflow"`
	Outflow      domain.Qty `json:"outflow"`
}

// stockSQL 은 품목마다 두 기준점을 함께 구한다.
//
//	최종(f): 실계수 포함 최신 기준점   추정(e): 실계수를 뺀 최신 기준점 (보통 같은 기준점 → 합계를 다시 세지 않는다)
//
// 기준점 이후 사입·조제 합계는 (product_code, date, qty) 색인만으로 계산된다 (0004_perf.sql).
const stockSQL = `
WITH r AS (
  SELECT id, product_code, as_of, timing, qty, source,
    ROW_NUMBER() OVER (PARTITION BY product_code ORDER BY as_of DESC, CASE timing WHEN 'end' THEN 1 ELSE 0 END DESC,
                       CASE source WHEN 'count' THEN 1 ELSE 0 END DESC, id DESC) AS rn_all,
    ROW_NUMBER() OVER (PARTITION BY product_code, (source = 'count') ORDER BY as_of DESC, CASE timing WHEN 'end' THEN 1 ELSE 0 END DESC, id DESC) AS rn_src
  FROM stock_anchor WHERE as_of <= ?1
),
fa AS (SELECT * FROM r WHERE rn_all = 1),
ea AS (SELECT * FROM r WHERE source <> 'count' AND rn_src = 1),
codes AS (
  SELECT code AS product_code FROM product p
  WHERE EXISTS (SELECT 1 FROM purchase_line x WHERE x.product_code = p.code)
     OR EXISTS (SELECT 1 FROM dispense_line x WHERE x.product_code = p.code)
     OR EXISTS (SELECT 1 FROM stock_anchor x WHERE x.product_code = p.code)
  UNION SELECT DISTINCT product_code FROM stock_anchor
),
base AS (
  SELECT c.product_code AS code,
    IFNULL(fa.id, 0) AS fid, IFNULL(fa.as_of, ?2) AS fdate, IFNULL(fa.timing, ?3) AS ftim, IFNULL(fa.qty, 0) AS fqty,
    IFNULL(fa.source, CASE WHEN ?2 = '' THEN 'none' ELSE 'baseline' END) AS fbasis,
    IFNULL(ea.id, 0) AS eid, IFNULL(ea.as_of, ?2) AS edate, IFNULL(ea.timing, ?3) AS etim, IFNULL(ea.qty, 0) AS eqty,
    IFNULL(ea.source, CASE WHEN ?2 = '' THEN 'none' ELSE 'baseline' END) AS ebasis
  FROM codes c LEFT JOIN fa ON fa.product_code = c.product_code LEFT JOIN ea ON ea.product_code = c.product_code
),
flows AS (
  SELECT b.*,
    (SELECT IFNULL(SUM(p.qty), 0) FROM purchase_line p WHERE p.product_code = b.code AND p.date <= ?1
       AND (b.fdate = '' OR p.date > b.fdate OR (p.date = b.fdate AND b.ftim = 'start'))) AS fin,
    (SELECT IFNULL(SUM(d.qty), 0) FROM dispense_line d WHERE d.product_code = b.code AND d.date <= ?1
       AND (b.fdate = '' OR d.date > b.fdate OR (d.date = b.fdate AND b.ftim = 'start'))) AS fout
  FROM base b
)
SELECT code, fid, fdate, ftim, fqty, fbasis, fin, fout, eid, edate, etim, eqty, ebasis,
  CASE WHEN eid = fid THEN fin ELSE (SELECT IFNULL(SUM(p.qty), 0) FROM purchase_line p WHERE p.product_code = code AND p.date <= ?1
       AND (edate = '' OR p.date > edate OR (p.date = edate AND etim = 'start'))) END,
  CASE WHEN eid = fid THEN fout ELSE (SELECT IFNULL(SUM(d.qty), 0) FROM dispense_line d WHERE d.product_code = code AND d.date <= ?1
       AND (edate = '' OR d.date > edate OR (d.date = edate AND etim = 'start'))) END
FROM flows`

type stockPair struct{ final, est map[string]StockInfo }

// stockMap 은 asOf 시점의 품목별 재고. includeCounts=false 면 실계수를 무시한 '추정재고'.
// 두 가지를 한 번에 계산해 데이터가 바뀌기 전까지 캐시한다.
func (s *Service) stockMap(asOf string, includeCounts bool) (map[string]StockInfo, error) {
	pair, err := cached(s, "stock|"+asOf, func() (stockPair, error) { return s.stockMapQuery(asOf) },
		func(p stockPair) stockPair { return stockPair{cloneMap(p.final), cloneMap(p.est)} })
	if err != nil {
		return nil, err
	}
	if includeCounts {
		return pair.final, nil
	}
	return pair.est, nil
}

func (s *Service) stockMapQuery(asOf string) (stockPair, error) {
	baseline := s.DB.GetSetting(SetBaselineDate, "")
	timing := s.DB.GetSetting(SetBaselineTiming, "start")
	rows, err := s.DB.Query(stockSQL, asOf, baseline, timing)
	if err != nil {
		return stockPair{}, err
	}
	defer rows.Close()
	out := stockPair{final: map[string]StockInfo{}, est: map[string]StockInfo{}}
	for rows.Next() {
		var f, e StockInfo
		var fq, fin, fout, eq, ein, eout int64
		if err := rows.Scan(&f.Code, &f.AnchorID, &f.AnchorDate, &f.AnchorTiming, &fq, &f.Basis, &fin, &fout,
			&e.AnchorID, &e.AnchorDate, &e.AnchorTiming, &eq, &e.Basis, &ein, &eout); err != nil {
			return stockPair{}, err
		}
		e.Code = f.Code
		f.AnchorQty, f.Inflow, f.Outflow = domain.Qty(fq), domain.Qty(fin), domain.Qty(fout)
		f.Qty = f.AnchorQty + f.Inflow - f.Outflow
		e.AnchorQty, e.Inflow, e.Outflow = domain.Qty(eq), domain.Qty(ein), domain.Qty(eout)
		e.Qty = e.AnchorQty + e.Inflow - e.Outflow
		out.final[f.Code], out.est[e.Code] = f, e
	}
	return out, rows.Err()
}

// latestPurchasePrices : before 이전 마지막 사입의 1단위 단가 (캐시)
func (s *Service) latestPurchasePrices(before string) map[string]float64 {
	m, err := cached(s, "prices|"+before, func() (map[string]float64, error) { return s.latestPurchasePricesQuery(before) }, cloneMap[string, float64])
	if err != nil {
		s.Log.Warn("사입 단가 조회 실패", "err", err)
		return map[string]float64{}
	}
	return m
}

func (s *Service) latestPurchasePricesQuery(before string) (map[string]float64, error) {
	out := map[string]float64{}
	// SQLite: max() 하나만 있는 집계에서 나머지 열은 그 최댓값 행의 값이다 → 날짜·id 가 가장 큰 행의 단가 (부분 색인 ix_purchase_price 사용)
	rows, err := s.DB.Query(`SELECT product_code, unit_price, MAX(date || '|' || printf('%010d', id)) FROM purchase_line
		WHERE kind = 'purchase' AND qty > 0 AND unit_price > 0 AND date < ? GROUP BY product_code`, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c, k string
		var p float64
		if err := rows.Scan(&c, &p, &k); err != nil {
			return nil, err
		}
		out[c] = p
	}
	return out, rows.Err() // 오류가 나면 캐시에 남기지 않는다 (cached)
}

func (s *Service) initialPrices() map[string]float64 {
	m, err := cached(s, "initPrices", s.initialPricesQuery, cloneMap[string, float64])
	if err != nil {
		s.Log.Warn("초기재고 단가 조회 실패", "err", err)
		return map[string]float64{}
	}
	return m
}

func (s *Service) initialPricesQuery() (map[string]float64, error) {
	out := map[string]float64{}
	rows, err := s.DB.Query("SELECT product_code, unit_price FROM stock_anchor WHERE source = 'initial' AND unit_price > 0")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		var p float64
		if err := rows.Scan(&c, &p); err != nil {
			return nil, err
		}
		out[c] = p
	}
	return out, rows.Err()
}

type productBrief struct {
	Name, Company, Unit  string // Unit: 고시 단위 짧은 표시 (없으면 이팜 재고단위)
	BaseLabel            string // 고시 단위 설명 (예: '팩(28정)')
	UnitStatus, UnitNote string
	PackSize, Factor     float64 // PackSize: 바코드 1개 = 고시 단위 N개 (사용자 지정 > 사입 포장 환산)
}

func (s *Service) productBriefs() map[string]productBrief {
	m, err := cached(s, "briefs", s.productBriefsQuery, cloneMap[string, productBrief])
	if err != nil {
		s.Log.Warn("품목 정보 조회 실패", "err", err)
		return map[string]productBrief{}
	}
	return m
}

func (s *Service) productBriefsQuery() (map[string]productBrief, error) {
	out := map[string]productBrief{}
	rows, err := s.DB.Query(`SELECT code, name, company, CASE WHEN base_unit <> '' THEN base_unit ELSE stock_unit END, base_label, unit_status, unit_note,
		IFNULL(pack_manual, IFNULL(canon_pack, IFNULL(pack_size, 0))), IFNULL(unit_factor, 0) FROM product`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		var b productBrief
		if err := rows.Scan(&c, &b.Name, &b.Company, &b.Unit, &b.BaseLabel, &b.UnitStatus, &b.UnitNote, &b.PackSize, &b.Factor); err != nil {
			return nil, err
		}
		out[c] = b
	}
	return out, rows.Err()
}

// ── 재고 화면 ─────────────────────────────────────────────────────

type InventoryRow struct {
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	Unit         string      `json:"unit"`
	Qty          domain.Qty  `json:"qty"`      // 최종(실계수 우선)
	Estimate     domain.Qty  `json:"estimate"` // 실계수 무시 추정
	Basis        string      `json:"basis"`
	AnchorDate   string      `json:"anchorDate"`
	AnchorTiming string      `json:"anchorTiming"`
	AnchorQty    domain.Qty  `json:"anchorQty"`
	Inflow       domain.Qty  `json:"inflow"`
	Outflow      domain.Qty  `json:"outflow"`
	UnitPrice    float64     `json:"unitPrice"`
	Value        int64       `json:"value"`
	CountQty     *domain.Qty `json:"countQty"`
	CountDate    string      `json:"countDate"`
	UnitStatus   string      `json:"unitStatus"`
	UnitNote     string      `json:"unitNote"`
	MonthlyUse   float64     `json:"monthlyUse"` // 최근 6개월 월평균 조제량
	Flags        []string    `json:"flags"`      // negative | unit_check | overstock | unit_auto
	Hint         string      `json:"hint"`       // 음수 재고 원인 추정
	Acked        []string    `json:"acked"`      // '확인함'으로 표시한 점검 항목 (상태가 그대로일 때만)
}

type InventoryView struct {
	AsOf           string         `json:"asOf"`
	Baseline       string         `json:"baseline"`
	BaselineTiming string         `json:"baselineTiming"`
	LastPurchase   string         `json:"lastPurchase"`
	LastDispense   string         `json:"lastDispense"`
	Rows           []InventoryRow `json:"rows"`
	TotalValue     int64          `json:"totalValue"`
	Products       int            `json:"products"`
	Negative       int            `json:"negative"` // 음수 추정재고 (데이터 누락 신호)
}

func (s *Service) DataFreshness() (lastPurchase, lastDispense string) {
	var p, d sql.NullString
	_ = s.DB.QueryRow("SELECT MAX(date) FROM purchase_line").Scan(&p)
	_ = s.DB.QueryRow("SELECT MAX(date) FROM dispense_line").Scan(&d)
	return nullStr(p), nullStr(d)
}

func (s *Service) Inventory(query string, onlyNonZero bool) (*InventoryView, error) {
	asOf := s.Today()
	final, err := s.stockMap(asOf, true)
	if err != nil {
		return nil, err
	}
	est, err := s.stockMap(asOf, false)
	if err != nil {
		return nil, err
	}
	briefs := s.productBriefs()
	use := s.monthlyUse()
	acked := s.acks()
	prices := s.latestPurchasePrices("9999-12-31")
	for c, p := range s.initialPrices() {
		if prices[c] == 0 {
			prices[c] = p
		}
	}
	v := &InventoryView{AsOf: asOf, Baseline: s.DB.GetSetting(SetBaselineDate, ""), BaselineTiming: s.DB.GetSetting(SetBaselineTiming, "start")}
	v.LastPurchase, v.LastDispense = s.DataFreshness()
	q := strings.ToLower(strings.TrimSpace(query))
	for code, si := range final {
		b := briefs[code]
		if q != "" && !strings.Contains(strings.ToLower(b.Name), q) && !strings.HasPrefix(code, q) {
			continue
		}
		if onlyNonZero && si.Qty == 0 {
			continue
		}
		row := InventoryRow{Code: code, Name: b.Name, Unit: b.Unit, Qty: si.Qty, Estimate: est[code].Qty, Basis: si.Basis,
			AnchorDate: si.AnchorDate, AnchorTiming: si.AnchorTiming, AnchorQty: si.AnchorQty, Inflow: si.Inflow, Outflow: si.Outflow,
			UnitPrice: prices[code], UnitStatus: b.UnitStatus, UnitNote: b.UnitNote, MonthlyUse: use[code]}
		row.Flags = productFlags(si, b, use[code])
		row.Hint = negativeReason(si)
		for _, f := range row.Flags {
			if st, ok := acked[code+"|"+f]; ok && ackStillValid(f, st, si, b) {
				row.Acked = append(row.Acked, f)
			}
		}
		if si.Basis == "count" {
			cq := si.AnchorQty
			row.CountQty, row.CountDate = &cq, si.AnchorDate
		}
		if si.Qty > 0 {
			row.Value = domain.RoundWon(si.Qty.Float() * row.UnitPrice)
			v.TotalValue += row.Value
		}
		if si.Qty < 0 {
			v.Negative++
		}
		v.Rows = append(v.Rows, row)
	}
	sort.Slice(v.Rows, func(i, j int) bool { return v.Rows[i].Name < v.Rows[j].Name })
	v.Products = len(v.Rows)
	return v, nil
}

// ── 실계수 · 기준일 ───────────────────────────────────────────────

type CountInput struct {
	Code   string  `json:"code"`
	Qty    float64 `json:"qty"`
	Unit   string  `json:"unit"`   // 센 단위 (UnitOptions 의 key, 비우면 고시 단위)
	AsOf   string  `json:"asOf"`   // 기본 오늘
	Timing string  `json:"timing"` // 기본 end(영업 후)
	Note   string  `json:"note"`
	Source string  `json:"source"` // 입력한 화면 (수정 기록용)
}

// UnitOption 은 실계수를 넣을 수 있는 단위 하나. 넣은 수 × Factor = 고시 단위 수량.
type UnitOption struct {
	Key    string  `json:"key"`    // base | measure | container | pack
	Label  string  `json:"label"`  // 예: '팩', '정', '통', '포장'
	Factor float64 `json:"factor"` // 이 단위 1 = 고시 단위 Factor 개
	Hint   string  `json:"hint"`   // 예: '1팩 = 28정'
}

// UnitOptions : 그 약을 셀 때 쓸 수 있는 단위들 (소분·개봉한 통은 낱알·mL·g 로, 미개봉은 포장으로 세도 된다).
func (s *Service) UnitOptions(code string) []UnitOption {
	code = domain.NormalizeProductCode(code)
	b := s.productBriefs()[code]
	pu, ok := priceUnitFor(s.DB, code)
	base := orDefaultStr(b.Unit, "개")
	if ok && pu.Short != "" {
		base = pu.Short
	}
	out := []UnitOption{{Key: "base", Label: base, Factor: 1, Hint: "고시 단위 (청구 단위)"}}
	if ok && pu.Known && pu.Container != "" && pu.PerCont <= 1 && pu.Size > 0 && pu.Size != 1 {
		out = append(out, UnitOption{Key: "measure", Label: pu.Measure, Factor: 1 / pu.Size,
			Hint: fmt.Sprintf("1%s = %s%s — 개봉한 통·소분은 이 단위로", pu.Container, trimF(pu.Size), pu.Measure)})
	}
	if ok && pu.Known && pu.PerCont > 1 {
		out = append(out, UnitOption{Key: "container", Label: pu.Container, Factor: pu.PerCont,
			Hint: fmt.Sprintf("1%s = %s%s", pu.Container, trimF(pu.PerCont*pu.Size), pu.Measure)})
	}
	if n := b.PackSize; n > 1 && (!ok || math.Abs(n-pu.PerCont) > 1e-9) {
		out = append(out, UnitOption{Key: "pack", Label: "포장", Factor: n, Hint: fmt.Sprintf("미개봉 포장 1개 = %s%s", trimF(n), base)})
	}
	return out
}

// SaveCount 는 실계수를 저장한다. 같은 날·같은 시점의 이전 실계수는 교체된다.
func (s *Service) SaveCount(in CountInput) (int64, error) {
	code := domain.NormalizeProductCode(in.Code)
	if code == "" {
		return 0, errors.New("약품코드가 올바르지 않습니다")
	}
	if in.Qty < 0 {
		return 0, errors.New("수량은 0 이상이어야 합니다")
	}
	if in.AsOf == "" {
		in.AsOf = s.Today()
	}
	if _, err := domain.ParseDate(in.AsOf); err != nil {
		return 0, fmt.Errorf("실사일 형식 오류: %s", in.AsOf)
	}
	if in.Timing != "start" {
		in.Timing = "end"
	}
	if in.Unit != "" && in.Unit != "base" { // 낱알·용기·포장으로 센 수량 → 고시 단위
		found := false
		for _, o := range s.UnitOptions(code) {
			if o.Key == in.Unit {
				in.Qty, found = in.Qty*o.Factor, true
				in.Note = strings.TrimSpace(in.Note + fmt.Sprintf(" (%s 단위로 입력, ×%s)", o.Label, trimF(o.Factor)))
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("이 약에 없는 단위입니다: %s", in.Unit)
		}
	}
	var id int64
	unit := s.productBriefs()[code].Unit
	err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		old := ""
		var oq int64
		if tx.QueryRow("SELECT qty FROM stock_anchor WHERE source = 'count' AND product_code = ? AND as_of = ? AND timing = ? ORDER BY id DESC LIMIT 1", code, in.AsOf, in.Timing).Scan(&oq) == nil {
			old = fmtQtyUnit(domain.Qty(oq), unit)
		}
		if _, err := tx.Exec("DELETE FROM stock_anchor WHERE source = 'count' AND product_code = ? AND as_of = ? AND timing = ?", code, in.AsOf, in.Timing); err != nil {
			return err
		}
		q := domain.QtyFromFloat(in.Qty)
		res, err := tx.Exec("INSERT INTO stock_anchor(product_code, as_of, timing, qty, source, note, created_at) VALUES(?, ?, ?, ?, 'count', ?, ?)",
			code, in.AsOf, in.Timing, int64(q), in.Note, store.Now())
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		return logChange(tx, ChangeEntry{Kind: "count", ProductCode: code, OldValue: old, NewValue: fmtQtyUnit(q, unit),
			Note: fmt.Sprintf("실계수 %s %s %s", in.AsOf, map[string]string{"start": "영업 전", "end": "영업 후"}[in.Timing], in.Note), Source: orDefaultStr(in.Source, "재고")})
	})
	return id, err
}

func (s *Service) DeleteAnchor(id int64) error {
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var code, asOf, source string
		var q int64
		if err := tx.QueryRow("SELECT product_code, as_of, source, qty FROM stock_anchor WHERE id = ?", id).Scan(&code, &asOf, &source, &q); err != nil {
			return ErrNotFound
		}
		if _, err := tx.Exec("DELETE FROM stock_anchor WHERE id = ?", id); err != nil {
			return err
		}
		unit := ""
		_ = tx.QueryRow("SELECT CASE WHEN base_unit <> '' THEN base_unit ELSE stock_unit END FROM product WHERE code = ?", code).Scan(&unit)
		return logChange(tx, ChangeEntry{Kind: "count_delete", ProductCode: code, OldValue: fmtQtyUnit(domain.Qty(q), unit), NewValue: "삭제",
			Note: map[string]string{"count": "실계수", "initial": "초기재고"}[source] + " " + asOf, Source: "재고"})
	})
}

type AnchorView struct {
	ID        int64      `json:"id"`
	AsOf      string     `json:"asOf"`
	Timing    string     `json:"timing"`
	Qty       domain.Qty `json:"qty"`
	Source    string     `json:"source"`
	Note      string     `json:"note"`
	CreatedAt string     `json:"createdAt"`
}

func (s *Service) ProductAnchors(code string) ([]AnchorView, error) {
	rows, err := s.DB.Query("SELECT id, as_of, timing, qty, source, note, created_at FROM stock_anchor WHERE product_code = ? ORDER BY as_of DESC, id DESC", code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AnchorView
	for rows.Next() {
		var a AnchorView
		var q int64
		if err := rows.Scan(&a.ID, &a.AsOf, &a.Timing, &q, &a.Source, &a.Note, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Qty = domain.Qty(q)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetBaseline 은 초기재고 기준일·시점을 바꾼다 (언제든 수정 가능).
func (s *Service) SetBaseline(date, timing string) error {
	if _, err := domain.ParseDate(date); err != nil {
		return fmt.Errorf("기준일 형식 오류: %s", date)
	}
	if timing != "end" {
		timing = "start"
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var od, ot string
		_ = tx.QueryRow("SELECT value FROM app_setting WHERE key = ?", SetBaselineDate).Scan(&od)
		_ = tx.QueryRow("SELECT value FROM app_setting WHERE key = ?", SetBaselineTiming).Scan(&ot)
		tl := map[string]string{"start": "영업 전", "end": "영업 후"}
		if err := logChange(tx, ChangeEntry{Kind: "baseline", OldValue: strings.TrimSpace(od + " " + tl[ot]), NewValue: date + " " + tl[timing], Note: "초기재고 기준일", Source: "재고"}); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE stock_anchor SET as_of = ?, timing = ? WHERE source = 'initial'", date, timing); err != nil {
			return err
		}
		for k, v := range map[string]string{SetBaselineDate: date, SetBaselineTiming: timing} {
			if _, err := tx.Exec("INSERT INTO app_setting(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", k, v); err != nil {
				return err
			}
		}
		// 초기재고 배율은 기준일의 고시 가격으로 정하므로, 기준일이 가격 변경을 넘어가면 다시 판단한다 (설정을 먼저 저장한 뒤)
		rows, err := tx.Query("SELECT DISTINCT product_code FROM initial_line")
		if err != nil {
			return err
		}
		var codes []string
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				codes = append(codes, c)
			}
		}
		rows.Close()
		if len(codes) == 0 {
			return nil
		}
		return recomputeUnits(tx, codes)
	})
}

// SetUnitFactor 는 품목의 '사입 기록 1단위 = 고시 단위 N개'를 사용자가 지정한다 (0 = 자동). 사입 수량을 다시 계산한다.
func (s *Service) SetUnitFactor(code string, factor float64, note, source string) error {
	code = domain.NormalizeProductCode(code)
	if factor < 0 || factor > 100000 {
		return errors.New("배율은 0(자동)~100000 사이여야 합니다")
	}
	var v any
	if factor > 0 {
		v = factor
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var old sql.NullFloat64
		var status string
		if err := tx.QueryRow("SELECT unit_factor, unit_status FROM product WHERE code = ?", code).Scan(&old, &status); err != nil {
			return ErrNotFound
		}
		if _, err := tx.Exec("UPDATE product SET unit_factor = ?, updated_at = ? WHERE code = ?", v, store.Now(), code); err != nil {
			return err
		}
		if factor <= 0 { // 자동으로 되돌리면 이전 지정 배율이 남지 않게 원본에서 다시 계산
			if _, err := tx.Exec("UPDATE purchase_line SET factor = -1 WHERE product_code = ?", code); err != nil {
				return err
			}
		}
		if err := recomputeUnits(tx, []string{code}); err != nil {
			return err
		}
		ov, nv := "자동", "자동"
		if old.Valid && old.Float64 > 0 {
			ov = "×" + trimF(old.Float64)
		}
		if factor > 0 {
			nv = "×" + trimF(factor)
		}
		return logChange(tx, ChangeEntry{Kind: "unit_factor", ProductCode: code, OldValue: ov + " (" + status + ")", NewValue: nv, Note: "사입 기록 1단위 = 고시 단위 N개 · " + note, Source: orDefaultStr(source, "품목 판정")})
	})
}

// SetPackSize 는 바코드 1개(포장)가 고시 단위 몇 개인지 지정한다 (0 = 자동: 표준코드 제품총수량 → 사입 포장 환산).
func (s *Service) SetPackSize(code string, size float64, note, source string) error {
	code = domain.NormalizeProductCode(code)
	if size < 0 {
		return errors.New("포장 수량은 0 이상이어야 합니다")
	}
	var v any
	if size > 0 {
		v = size
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var old sql.NullFloat64
		if err := tx.QueryRow("SELECT pack_manual FROM product WHERE code = ?", code).Scan(&old); err != nil {
			return ErrNotFound
		}
		if _, err := tx.Exec("UPDATE product SET pack_manual = ?, updated_at = ? WHERE code = ?", v, store.Now(), code); err != nil {
			return err
		}
		ov, nv := "자동", "자동"
		if old.Valid && old.Float64 > 0 {
			ov = trimF(old.Float64)
		}
		if size > 0 {
			nv = trimF(size)
		}
		return logChange(tx, ChangeEntry{Kind: "pack_size", ProductCode: code, OldValue: ov, NewValue: nv, Note: "바코드 1개 = 고시 단위 N개 · " + note, Source: orDefaultStr(source, "품목 판정")})
	})
}
