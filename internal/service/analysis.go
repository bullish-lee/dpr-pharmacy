package service

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// ── 분석 컨텍스트 (한 시행일 분석에 필요한 공통 데이터를 한 번만 읽는다) ─────────

type supplierInfo struct {
	ID     int64
	Name   string
	Kind   string
	Active bool
}

type analysisCtx struct {
	eff, asOf  string
	final, est map[string]StockInfo
	prices     map[string]float64 // 시행일 이전 최근 사입 단가
	initPrices map[string]float64
	briefs     map[string]productBrief
	suppliers  map[int64]supplierInfo
	overrides  map[int64][2]string // 회차별 기간 직접 지정
}

// asOfFor : 지난 시행일이면 시행 전날 재고, 다가오는 시행일이면 오늘 재고
func (s *Service) asOfFor(eff string) string {
	today := s.Today()
	t, err := domain.ParseDate(eff)
	if err != nil {
		return today
	}
	if prev := domain.FormatDate(t.AddDate(0, 0, -1)); prev < today {
		return prev
	}
	return today
}

func (s *Service) suppliersMap() map[int64]supplierInfo {
	out := map[int64]supplierInfo{}
	rows, err := s.DB.Query("SELECT id, name, kind, active FROM supplier")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var si supplierInfo
		var a int
		if rows.Scan(&si.ID, &si.Name, &si.Kind, &a) == nil {
			si.Active = a == 1
			out[si.ID] = si
		}
	}
	return out
}

func (s *Service) newAnalysis(eff string) (*analysisCtx, error) {
	ac := &analysisCtx{eff: eff, asOf: s.asOfFor(eff)}
	var err error
	if ac.final, err = s.stockMap(ac.asOf, true); err != nil {
		return nil, err
	}
	if ac.est, err = s.stockMap(ac.asOf, false); err != nil {
		return nil, err
	}
	ac.prices = s.latestPurchasePrices(eff)
	ac.initPrices = s.initialPrices()
	ac.briefs = s.productBriefs()
	ac.suppliers = s.suppliersMap()
	ac.overrides = map[int64][2]string{}
	if rows, err := s.DB.Query("SELECT supplier_id, window_from, window_to FROM window_override WHERE effective_date = ?", eff); err == nil {
		for rows.Next() {
			var id int64
			var f, t string
			if rows.Scan(&id, &f, &t) == nil {
				ac.overrides[id] = [2]string{f, t}
			}
		}
		rows.Close()
	}
	return ac, nil
}

// ── 도매별 포지션 계산 ────────────────────────────────────────────

type PosMeta struct {
	IsDefaultRule  bool   `json:"isDefaultRule"`
	NeedsReview    bool   `json:"needsReview"`
	Overridden     bool   `json:"overridden"`
	SettlementDate string `json:"settlementDate"`
	SupplierKind   string `json:"supplierKind"`
}

type ScanStat struct {
	Units        int        `json:"units"`
	Qty          domain.Qty `json:"qty"`
	Ambiguous    int        `json:"ambiguous"`
	Unmatched    int        `json:"unmatched"`
	Registered   int        `json:"registered"`
	regBySupp    map[int64]int
	unitsBySupp  map[int64]int
	scannedBySup map[int64]domain.Qty
}

type productCalc struct {
	positions    []domain.SupplierPosition
	meta         []PosMeta
	unattributed domain.Qty
	scan         ScanStat
	scanned      bool
}

func (s *Service) computePositions(ac *analysisCtx, codes []string, noticeDate string) (map[string]*productCalc, error) {
	out := map[string]*productCalc{}
	if len(codes) == 0 {
		return out, nil
	}
	for _, c := range codes {
		out[c] = &productCalc{scan: ScanStat{regBySupp: map[int64]int{}, unitsBySupp: map[int64]int{}, scannedBySup: map[int64]domain.Qty{}}}
	}
	in, args := inClause(codes)
	effT, err := domain.ParseDate(ac.eff)
	if err != nil {
		return nil, fmt.Errorf("시행일 형식 오류: %s", ac.eff)
	}
	var noticeT *time.Time
	if nt, err := domain.ParseDate(noticeDate); err == nil {
		noticeT = &nt
	}

	// 1) 품목별 관련 도매 (사입 이력 + 스캔 귀속)
	related := map[string]map[int64]bool{}
	add := func(code string, sid int64) {
		if related[code] == nil {
			related[code] = map[int64]bool{}
		}
		related[code][sid] = true
	}
	rows, err := s.DB.Query("SELECT DISTINCT product_code, supplier_id FROM purchase_line WHERE supplier_id IS NOT NULL AND product_code IN ("+in+")", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		var sid int64
		if rows.Scan(&c, &sid) == nil {
			add(c, sid)
		}
	}
	rows.Close()

	// 2) 스캔 결과
	rows, err = s.DB.Query("SELECT product_code, IFNULL(supplier_id, 0), match_status, registered, qty FROM scan_unit WHERE effective_date = ? AND product_code IN ("+in+")",
		append([]any{ac.eff}, args...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c, status string
		var sid, q int64
		var reg int
		if rows.Scan(&c, &sid, &status, &reg, &q) != nil {
			continue
		}
		pc := out[c]
		pc.scanned = true
		pc.scan.Units++
		pc.scan.Qty += domain.Qty(q)
		if sid > 0 {
			add(c, sid)
			pc.scan.scannedBySup[sid] += domain.Qty(q)
			pc.scan.unitsBySupp[sid]++
			if reg == 1 {
				pc.scan.regBySupp[sid]++
				pc.scan.Registered++
			}
		} else if status == "ambiguous" {
			pc.scan.Ambiguous++
		} else {
			pc.scan.Unmatched++
		}
	}
	rows.Close()

	// 3) 도매별 규칙·대상기간
	type win struct{ from, to string }
	rules := map[int64]*RuleView{}
	windows := map[int64]win{}
	meta := map[int64]PosMeta{}
	bySupplierSet := map[int64]bool{}
	for _, set := range related {
		for sid := range set {
			bySupplierSet[sid] = true
		}
	}
	for sid := range bySupplierSet {
		si := ac.suppliers[sid]
		rv := s.RuleFor(s.DB, sid, si.Kind, si.Active, ac.eff)
		m := PosMeta{SupplierKind: si.Kind}
		if rv != nil {
			rules[sid] = rv
			m.IsDefaultRule, m.NeedsReview = rv.IsDefault, rv.NeedsReview
			f, t := domain.CompWindow(rv.Rule, effT, noticeT)
			windows[sid] = win{domain.FormatDate(f), domain.FormatDate(t)}
			m.SettlementDate = domain.FormatDate(domain.NextSettlementDate(effT, rv.Rule.SettlementDay))
		}
		if ov, ok := ac.overrides[sid]; ok {
			windows[sid] = win{ov[0], ov[1]}
			m.Overridden = true
		}
		meta[sid] = m
	}

	// 4) 대상기간 사입·반품 합계 (같은 기간끼리 묶어 조회)
	type pr struct{ purchased, returned domain.Qty }
	sums := map[string]map[int64]pr{}
	groups := map[win][]int64{}
	for sid, w := range windows {
		groups[w] = append(groups[w], sid)
	}
	for w, sids := range groups {
		sIn := strings.TrimSuffix(strings.Repeat("?,", len(sids)), ",")
		qargs := []any{w.from, w.to}
		for _, id := range sids {
			qargs = append(qargs, id)
		}
		qargs = append(qargs, args...)
		rows, err := s.DB.Query(`SELECT supplier_id, product_code,
			SUM(CASE WHEN kind = 'purchase' AND qty > 0 THEN qty ELSE 0 END), SUM(CASE WHEN kind = 'return' THEN -qty ELSE 0 END)
			FROM purchase_line WHERE date BETWEEN ? AND ? AND supplier_id IN (`+sIn+`) AND product_code IN (`+in+`) GROUP BY supplier_id, product_code`, qargs...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var sid int64
			var c string
			var p, r int64
			if rows.Scan(&sid, &c, &p, &r) == nil {
				if sums[c] == nil {
					sums[c] = map[int64]pr{}
				}
				sums[c][sid] = pr{domain.Qty(p), domain.Qty(r)}
			}
		}
		rows.Close()
	}

	// 5) FIFO 추정용 사입 이력 — 도매로의 반품(음수)도 함께: 반품한 약은 그 도매 몫에서 뺀다
	lots := map[string][]domain.PurchaseLot{}
	rows, err = s.DB.Query("SELECT product_code, date, IFNULL(supplier_id, 0), qty FROM purchase_line WHERE ((kind = 'purchase' AND qty > 0) OR (kind = 'return' AND qty < 0)) AND date <= ? AND product_code IN ("+in+")",
		append([]any{ac.asOf}, args...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c, d string
		var sid, q int64
		if rows.Scan(&c, &d, &sid, &q) == nil {
			lots[c] = append(lots[c], domain.PurchaseLot{Date: d, SupplierID: sid, Qty: domain.Qty(q)})
		}
	}
	rows.Close()

	// 6) 품목별 포지션 조립
	for _, code := range codes {
		pc := out[code]
		stock := ac.final[code].Qty
		fifo := domain.FIFOAttribution(domain.MaxQty(stock, 0), lots[code])
		pc.unattributed = fifo[0]
		sids := make([]int64, 0, len(related[code]))
		for sid := range related[code] {
			sids = append(sids, sid)
		}
		sort.Slice(sids, func(i, j int) bool { return ac.suppliers[sids[i]].Name < ac.suppliers[sids[j]].Name })
		for _, sid := range sids {
			si := ac.suppliers[sid]
			p := domain.SupplierPosition{SupplierID: sid, SupplierName: si.Name, EstimatedHeld: fifo[sid]}
			if rv := rules[sid]; rv != nil {
				r := rv.Rule
				p.Rule = &r
				p.RuleText = rv.Description
			}
			if w, ok := windows[sid]; ok {
				p.WindowFrom, p.WindowTo = w.from, w.to
			}
			if v, ok := sums[code][sid]; ok {
				p.Purchased, p.Returned = v.purchased, v.returned
			}
			if pc.scanned {
				q := pc.scan.scannedBySup[sid]
				p.ScannedHeld = &q
			}
			// 관련성이 전혀 없는 도매는 생략 (기간 사입 0, 추정 보유 0, 스캔 0)
			if p.Purchased == 0 && p.Returned == 0 && p.EstimatedHeld == 0 && (p.ScannedHeld == nil || *p.ScannedHeld == 0) {
				continue
			}
			pc.positions = append(pc.positions, p)
			pc.meta = append(pc.meta, meta[sid])
		}
	}
	return out, nil
}

// ── 손실 순위 ─────────────────────────────────────────────────────

type RankRow struct {
	ChangeID         int64       `json:"changeId"`
	NoticeID         int64       `json:"noticeId"`
	NoticeNo         string      `json:"noticeNo"`
	Code             string      `json:"code"`
	Name             string      `json:"name"`
	NoticeName       string      `json:"noticeName"`
	Company          string      `json:"company"`
	Unit             string      `json:"unit"`
	NoticeUnit       string      `json:"noticeUnit"`
	Spec             string      `json:"spec"`
	Detail           string      `json:"detail"`
	Flexible         bool        `json:"flexible"`
	Note             string      `json:"note"`
	PriceBefore      float64     `json:"priceBefore"`
	PriceAfter       float64     `json:"priceAfter"`
	Rate             float64     `json:"rate"`
	CurrentUnitPrice float64     `json:"currentUnitPrice"`
	PriceSource      string      `json:"priceSource"`
	UnitFactor       float64     `json:"unitFactor"`
	UnitStatus       string      `json:"unitStatus"` // ok | auto_unit | auto_pack | check | no_reference | manual
	UnitNote         string      `json:"unitNote"`
	BaseLabel        string      `json:"baseLabel"` // 고시 단위 설명 (예: '팩(28정)')
	Delta            float64     `json:"delta"`
	Estimate         domain.Qty  `json:"estimate"`
	Stock            domain.Qty  `json:"stock"`
	Basis            string      `json:"basis"`
	CountQty         *domain.Qty `json:"countQty"`
	CountDate        string      `json:"countDate"`
	CountAnchorID    int64       `json:"countAnchorId"`
	// 재고 기준일(지난 회차는 시행 전날) 뒤에 넣은 실계수 — 이 회차 계산에는 쓰지 않고 보여만 준다 (어느 시점 재고로 판정할지는 정해지지 않음)
	LaterCountQty      *domain.Qty `json:"laterCountQty"`
	LaterCountDate     string      `json:"laterCountDate"`
	LaterCountAnchorID int64       `json:"laterCountAnchorId"`
	GrossLoss          int64       `json:"grossLoss"`
	TotalDocQty        domain.Qty  `json:"totalDocQty"`
	Recommendation     string      `json:"recommendation"`
	Recovery           int64       `json:"recovery"`
	NetLoss            int64       `json:"netLoss"`
	PhysicalCount      int         `json:"physicalCount"` // 실물 유리 도매 수
	PhysicalUpside     domain.Qty  `json:"physicalUpside"`
	UpsideAmount       int64       `json:"upsideAmount"`
	Scan               ScanStat    `json:"scan"`
	SettleStatus       string      `json:"settleStatus"` // '' | planned | done | partial
}

type RankTotals struct {
	Items     int   `json:"items"`
	Held      int   `json:"held"`
	GrossLoss int64 `json:"grossLoss"`
	Recovery  int64 `json:"recovery"`
	NetLoss   int64 `json:"netLoss"`
	Document  int   `json:"document"`
	Physical  int   `json:"physical"`
	Check     int   `json:"check"` // 실제가 확인 필요 (약가유연계약)
	Done      int   `json:"done"`
}

type Ranking struct {
	EffectiveDate string     `json:"effectiveDate"`
	AsOf          string     `json:"asOf"`
	IsPast        bool       `json:"isPast"`
	Notices       []string   `json:"notices"`
	Rows          []RankRow  `json:"rows"`
	Totals        RankTotals `json:"totals"`
	Warnings      []string   `json:"warnings"`
}

type changeRec struct {
	id, noticeID                                          int64
	noticeNo, published                                   string
	code, name, company, spec, unit, detail, note, effDay string
	before, after                                         float64
	flexible                                              bool
}

// priceDetails : 가격에 대해 말하는 고시 항목. 같은 품목·시행일에 고시가 여럿이면 이 중 가장 최근 것이 가격을 정한다.
// 예) 인하 고시 뒤에 '변경 없음(same)'으로 정정되면 인하는 취소된 것. 제품명 변경(name_change)·기타(other)·미확인(unknown)은
// 가격을 말하지 않으므로 앞선 인하를 취소하지 않는다.
var priceDetails = []string{"price_down", "price_up", "same", "agreed_price"}

// noticeSeq : 고시 번호 '2026-219' → 2026·100000 + 219 (번호가 없거나 형식이 다르면 0)
func noticeSeq(no string) int64 {
	y, n, ok := strings.Cut(strings.TrimSpace(no), "-")
	if !ok {
		return 0
	}
	yy, err1 := strconv.Atoi(strings.TrimSpace(y))
	nn, err2 := strconv.Atoi(strings.TrimSpace(n))
	if err1 != nil || err2 != nil || yy < 2000 || nn <= 0 {
		return 0
	}
	return int64(yy)*100000 + int64(nn)
}

// latestOf : 같은 품목·시행일 후보 중 최신 고시.
//   - 모두 고시 번호가 있으면 번호 순 — 공식 일련번호라 정정 고시가 더 크다 (직접 올린 고시는 게시일 대신 '올린 날'이 들어가므로 날짜로 비교하면 틀린다)
//   - 하나라도 번호가 없으면 게시일 순
//   - 같으면 나중에 저장된 고시, 같은 고시 안에서는 앞 행
//
// 번호와 날짜를 섞어 비교하면 순서가 돌고 돌 수 있어 묶음 전체에 한 기준을 쓴다.
func latestOf(cands []changeRec) changeRec {
	byNumber := true
	for _, c := range cands {
		if noticeSeq(c.noticeNo) == 0 {
			byNumber = false
			break
		}
	}
	newer := func(a, b changeRec) bool {
		if byNumber {
			if x, y := noticeSeq(a.noticeNo), noticeSeq(b.noticeNo); x != y {
				return x > y
			}
		}
		if a.published != b.published {
			return a.published > b.published
		}
		if a.noticeID != b.noticeID {
			return a.noticeID > b.noticeID
		}
		return a.id < b.id
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if newer(c, best) {
			best = c
		}
	}
	return best
}

// priceChangeGroups : 조건에 맞는 고시 항목을 (시행일, 품목)별로 모은다 (품목 순서 유지)
func (s *Service) priceChangeGroups(where string, args ...any) ([][]changeRec, error) {
	rows, err := s.DB.Query(`SELECT `+changeCols+` FROM price_change c JOIN notice n ON n.id = c.notice_id WHERE `+where+
		` ORDER BY c.effective_date, c.product_code, c.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups [][]changeRec
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		if n := len(groups); n > 0 && groups[n-1][0].code == c.code && groups[n-1][0].effDay == c.effDay {
			groups[n-1] = append(groups[n-1], c)
			continue
		}
		groups = append(groups, []changeRec{c})
	}
	return groups, rows.Err()
}

const changeCols = `c.id, c.notice_id, n.notice_no, n.published_at, c.product_code, c.product_name, c.company, c.spec, c.unit, c.detail, c.note,
		IFNULL(c.price_before, 0), IFNULL(c.price_after, 0), c.flexible, c.effective_date`

func scanChange(sc interface{ Scan(...any) error }) (changeRec, error) {
	var c changeRec
	var flex int
	err := sc.Scan(&c.id, &c.noticeID, &c.noticeNo, &c.published, &c.code, &c.name, &c.company, &c.spec, &c.unit, &c.detail, &c.note,
		&c.before, &c.after, &flex, &c.effDay)
	c.flexible = flex == 1
	return c, err
}

// changesForDate : 시행일의 품목별 '최신' 가격 고시 중 details 에 해당하는 것.
// 최신 고시를 먼저 고른 뒤 거른다 — 거른 뒤 고르면 정정 고시(same)가 앞선 인하를 취소하지 못한다.
func (s *Service) changesForDate(eff string, details []string) ([]changeRec, error) {
	dIn, dArgs := inClause(priceDetails)
	groups, err := s.priceChangeGroups(`c.effective_date = ? AND c.detail IN (`+dIn+`)`, append([]any{eff}, dArgs...)...)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, d := range details {
		want[d] = true
	}
	var out []changeRec
	for _, g := range groups { // 같은 시행일에 여러 고시가 있으면 최신 고시 우선 (latestOf)
		if c := latestOf(g); want[c.detail] {
			out = append(out, c)
		}
	}
	return out, nil
}

// unknownChangesForDate : 시행일의 '변경 전 가격 미상' 변경 중, 같은 품목·시행일에 가격 고시(priceDetails)가 따로 없는 것 (품목별 최신)
func (s *Service) unknownChangesForDate(eff string) ([]changeRec, error) {
	dIn, dArgs := inClause(priceDetails)
	groups, err := s.priceChangeGroups(`c.effective_date = ? AND c.detail = 'unknown' AND NOT EXISTS (SELECT 1 FROM price_change x
		WHERE x.product_code = c.product_code AND x.effective_date = c.effective_date AND x.detail IN (`+dIn+`))`, append([]any{eff}, dArgs...)...)
	if err != nil {
		return nil, err
	}
	out := make([]changeRec, 0, len(groups))
	for _, g := range groups {
		out = append(out, latestOf(g))
	}
	return out, nil
}

type laterCount struct {
	id   int64
	date string
	qty  domain.Qty
}

// laterCounts : asOf 뒤에 넣은 품목별 가장 최근 실계수 (지난 회차 화면에 '오늘 실계수'로 보여 준다)
func (s *Service) laterCounts(asOf string) map[string]laterCount {
	out := map[string]laterCount{}
	rows, err := s.DB.Query(`SELECT product_code, id, as_of, qty FROM stock_anchor WHERE source = 'count' AND as_of > ?
		ORDER BY product_code, as_of DESC, CASE timing WHEN 'end' THEN 1 ELSE 0 END DESC, id DESC`, asOf)
	if err != nil {
		s.Log.Warn("나중 실계수 조회 실패", "err", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c, d string
		var id, q int64
		if rows.Scan(&c, &id, &d, &q) != nil {
			continue
		}
		if _, ok := out[c]; !ok {
			out[c] = laterCount{id: id, date: d, qty: domain.Qty(q)}
		}
	}
	return out
}

// latestChange : 품목·시행일의 최신 고시 항목 (가격 고시가 있으면 그중 최신, 없으면 어떤 항목이든 최신)
func (s *Service) latestChange(eff, code string) (changeRec, error) {
	groups, err := s.priceChangeGroups(`c.effective_date = ? AND c.product_code = ?`, eff, code)
	if err != nil {
		return changeRec{}, err
	}
	if len(groups) == 0 {
		return changeRec{}, sql.ErrNoRows
	}
	isPrice := map[string]bool{}
	for _, d := range priceDetails {
		isPrice[d] = true
	}
	var priced []changeRec
	for _, c := range groups[0] {
		if isPrice[c.detail] {
			priced = append(priced, c)
		}
	}
	if len(priced) > 0 {
		return latestOf(priced), nil
	}
	return latestOf(groups[0]), nil
}

type settleAgg struct{ planned, done int }

func (s *Service) settlementStatus(eff string) map[string]settleAgg {
	out := map[string]settleAgg{}
	rows, err := s.DB.Query("SELECT product_code, status FROM settlement WHERE effective_date = ?", eff)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c, st string
		if rows.Scan(&c, &st) == nil {
			a := out[c]
			if st == "done" {
				a.done++
			} else {
				a.planned++
			}
			out[c] = a
		}
	}
	return out
}

func (s *Service) buildRankRow(ac *analysisCtx, c changeRec, pc *productCalc) (RankRow, domain.ProductDecision) {
	b := ac.briefs[c.code]
	fin, est := ac.final[c.code], ac.est[c.code]
	row := RankRow{ChangeID: c.id, NoticeID: c.noticeID, NoticeNo: c.noticeNo, Code: c.code, Name: b.Name, NoticeName: c.name,
		Company: c.company, Unit: b.Unit, NoticeUnit: c.unit, Spec: c.spec, Detail: c.detail, Flexible: c.flexible, Note: c.note,
		PriceBefore: c.before, PriceAfter: c.after, Estimate: est.Qty, Stock: fin.Qty, Basis: fin.Basis}
	if row.Name == "" {
		row.Name = c.name
	}
	if fin.Basis == "count" {
		q := fin.AnchorQty
		row.CountQty, row.CountDate, row.CountAnchorID = &q, fin.AnchorDate, fin.AnchorID
	}
	// 수량은 모두 고시 단위로 환산돼 있다(units.go) → 배율 1. 품목의 환산 상태를 그대로 보여준다.
	cur, init := ac.prices[c.code], ac.initPrices[c.code]
	ref := cur
	if ref == 0 {
		ref = init
	}
	row.UnitFactor, row.UnitStatus, row.UnitNote, row.BaseLabel = 1, b.UnitStatus, b.UnitNote, b.BaseLabel
	if row.Unit == "" {
		row.Unit = domain.ParsePriceUnit(c.spec, c.unit).Short
	}
	if ref > 0 && c.before > 0 {
		if r := ref / c.before; r < 0.65 || r > 1.55 { // 환산 후에도 매입가가 고시가와 크게 다르면 확인 필요
			row.UnitStatus = "check"
			row.UnitNote = fmt.Sprintf("매입 단가(%s원)가 고시 변경 전 가격(%s원)의 %.2f배 — 단위·가격 확인 필요", trimF(math.Round(ref)), trimF(c.before), r)
		}
	}
	if row.UnitStatus == "check" { // 매입가를 믿을 수 없으면 고시 가격 차이로 계산
		cur, init = 0, 0
	}
	pd := domain.ComputePriceDelta(cur, init, c.before, c.after, 1)
	row.CurrentUnitPrice, row.PriceSource, row.Delta, row.Rate = pd.CurrentUnitPrice, pd.PriceSource, pd.Delta, pd.Rate
	var positions []domain.SupplierPosition
	if pc != nil {
		positions = pc.positions
		row.Scan = pc.scan
	}
	unknownAfter := c.detail == "price_down" && c.after <= 0 // 인하인데 변경 후 가격을 모름 (빈칸) → 0원으로 보면 전액 손실이 된다
	unknownBefore := c.detail == "unknown"                   // 변경 전 가격을 모름 (변경대비표 없음, 과거 고시·급여목록에도 없음) → 인하인지 알 수 없다
	if unknownBefore && row.UnitNote == "" {
		row.UnitNote = "고시의 변경 전 가격을 알 수 없음 — 인하인지 고시 원문 확인 필요"
	}
	unknownAfter = unknownAfter || unknownBefore
	if c.detail == "agreed_price" || unknownAfter {
		// 약가유연계약: 고시 표시가와 실제 상한금액(별도합의)이 달라 손실을 계산할 수 없다 → 실제가 확인 필요
		row.Delta = 0
		pd.Delta = 0
		row.Rate = 0
	}
	if unknownAfter && row.UnitNote == "" {
		row.UnitNote = "고시의 변경 후 가격을 읽지 못함 — 고시 원문 확인 필요"
	}
	scannedTotal := domain.Qty(-1) // 이 품목에서 찍은 전체 수량 (도매를 못 정한 것 포함) — 찍는 중이면 안 찍은 몫을 추정으로 채운다
	if pc != nil && pc.scanned {
		scannedTotal = pc.scan.Qty
	}
	dec := domain.DecideScanned(fin.Qty, pd.Delta, positions, scannedTotal)
	if (c.detail == "agreed_price" || unknownAfter) && fin.Qty > 0 {
		dec.Recommendation = "check_price"
	}
	row.GrossLoss, row.TotalDocQty, row.Recommendation, row.Recovery, row.NetLoss = dec.GrossLoss, dec.TotalDocQty, dec.Recommendation, dec.Recovery, dec.NetLoss
	row.PhysicalUpside, row.UpsideAmount = dec.PhysicalUpside, dec.UpsideAmount
	for _, p := range dec.Positions {
		if p.Method == "physical" {
			row.PhysicalCount++
		}
	}
	return row, dec
}

// Ranking 은 시행일의 인하 품목을 예상 손실이 큰 순서로 정렬한다.
func (s *Service) Ranking(eff string, heldOnly bool) (*Ranking, error) {
	ac, err := s.newAnalysis(eff)
	if err != nil {
		return nil, err
	}
	changes, err := s.changesForDate(eff, []string{"price_down", "agreed_price"})
	if err != nil {
		return nil, err
	}
	unknowns, err := s.unknownChangesForDate(eff)
	if err != nil {
		return nil, err
	}
	for _, c := range unknowns { // 변경 후 가격이 매입가보다 낮을 수 있는 것만 (확실히 손실이 없으면 빼서 순위를 어지럽히지 않는다)
		ref := ac.prices[c.code]
		if ref == 0 {
			ref = ac.initPrices[c.code]
		}
		if c.after > 0 && ref > 0 && ref <= c.after {
			continue
		}
		changes = append(changes, c)
	}
	rk := &Ranking{EffectiveDate: eff, AsOf: ac.asOf, IsPast: eff <= s.Today()}
	notices := map[string]bool{}
	byNoticeDate := map[string][]string{} // 고시일 → 품목 (도매 기준이 '고시일'이면 품목마다 자기 고시의 고시일로 기간을 정한다)
	for _, c := range changes {
		if c.noticeNo != "" {
			notices[c.noticeNo] = true
		}
		if ac.final[c.code].Qty > 0 {
			byNoticeDate[c.published] = append(byNoticeDate[c.published], c.code)
		}
	}
	rk.Notices = sortedKeys(notices)
	calcs := map[string]*productCalc{}
	for nd, codes := range byNoticeDate {
		part, err := s.computePositions(ac, codes, nd)
		if err != nil {
			return nil, err
		}
		for k, v := range part {
			calcs[k] = v
		}
	}
	later := s.laterCounts(ac.asOf)
	settle := s.settlementStatus(eff)
	for _, c := range changes {
		rk.Totals.Items++
		held := ac.final[c.code].Qty > 0
		if heldOnly && !held {
			continue
		}
		row, _ := s.buildRankRow(ac, c, calcs[c.code])
		if lc, ok := later[c.code]; ok {
			q := lc.qty
			row.LaterCountQty, row.LaterCountDate, row.LaterCountAnchorID = &q, lc.date, lc.id
		}
		if a, ok := settle[c.code]; ok {
			switch {
			case a.done > 0 && a.planned == 0:
				row.SettleStatus = "done"
				rk.Totals.Done++
			case a.done > 0:
				row.SettleStatus = "partial"
			default:
				row.SettleStatus = "planned"
			}
		}
		if held {
			rk.Totals.Held++
			rk.Totals.GrossLoss += row.GrossLoss
			rk.Totals.Recovery += row.Recovery
			rk.Totals.NetLoss += row.NetLoss
			switch row.Recommendation {
			case "document":
				rk.Totals.Document++
			case "physical_review":
				rk.Totals.Physical++
			case "check_price":
				rk.Totals.Check++
			}
		}
		rk.Rows = append(rk.Rows, row)
	}
	sort.SliceStable(rk.Rows, func(i, j int) bool {
		if rk.Rows[i].GrossLoss != rk.Rows[j].GrossLoss {
			return rk.Rows[i].GrossLoss > rk.Rows[j].GrossLoss
		}
		return rk.Rows[i].Rate > rk.Rows[j].Rate
	})
	rk.Warnings = s.dataWarnings(ac.asOf)
	return rk, nil
}

func (s *Service) dataWarnings(asOf string) []string {
	var w []string
	lp, ld := s.DataFreshness()
	if lp == "" && ld == "" {
		return []string{"사입·조제 데이터가 없습니다. '데이터 가져오기'에서 이팜 엑셀을 올려주세요."}
	}
	if s.DB.GetSetting(SetBaselineDate, "") == "" {
		w = append(w, "초기재고가 없어 사입 − 조제만으로 추정합니다.")
	}
	for label, d := range map[string]string{"조제현황": ld, "사입현황": lp} {
		if d == "" {
			w = append(w, label+" 데이터가 없습니다.")
			continue
		}
		if t1, e1 := domain.ParseDate(d); e1 == nil {
			if t2, e2 := domain.ParseDate(asOf); e2 == nil && t2.Sub(t1) > 72*time.Hour {
				w = append(w, fmt.Sprintf("%s이 %s까지만 반영되어 있습니다. 최신 자료를 올리면 추정재고가 정확해집니다.", label, d))
			}
		}
	}
	var review int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM comp_rule WHERE needs_review = 1").Scan(&review)
	if review > 0 {
		w = append(w, fmt.Sprintf("보상 기준 %d건이 '확인 필요' 상태입니다 (기본값 사용 중). 도매처·보상기준에서 실제 기준을 입력하세요.", review))
	}
	sort.Strings(w)
	return w
}

// ── 시행일 목록 ───────────────────────────────────────────────────

type CutDate struct {
	EffectiveDate string   `json:"effectiveDate"`
	Notices       []string `json:"notices"`
	Down          int      `json:"down"`
	Up            int      `json:"up"`
	New           int      `json:"new"`
	Delete        int      `json:"delete"`
	Agreed        int      `json:"agreed"`
	HeldDown      int      `json:"heldDown"`
	HeldDelete    int      `json:"heldDelete"`
	GrossLoss     int64    `json:"grossLoss"`
	Recovery      int64    `json:"recovery"`
	NetLoss       int64    `json:"netLoss"`
	Done          int      `json:"done"`
	IsPast        bool     `json:"isPast"`
	DaysLeft      int      `json:"daysLeft"`
	Computed      bool     `json:"computed"`
}

func (s *Service) PriceCutDates() ([]CutDate, error) {
	// 신설·삭제는 품목 수, 인하·인상·유연계약은 품목별 최신 가격 고시 기준 (정정 고시가 앞선 인하를 취소 — changesForDate 와 같은 기준)
	rows, err := s.DB.Query(`SELECT c.effective_date, COUNT(DISTINCT CASE WHEN c.kind = 'new' THEN c.product_code END),
		COUNT(DISTINCT CASE WHEN c.kind = 'delete' THEN c.product_code END), GROUP_CONCAT(DISTINCT n.notice_no)
		FROM price_change c JOIN notice n ON n.id = c.notice_id WHERE c.effective_date <> '' GROUP BY c.effective_date ORDER BY c.effective_date DESC`)
	if err != nil {
		return nil, err
	}
	var out []CutDate
	for rows.Next() {
		var d CutDate
		var nos sql.NullString
		if err := rows.Scan(&d.EffectiveDate, &d.New, &d.Delete, &nos); err != nil {
			rows.Close()
			return nil, err
		}
		if nos.Valid && nos.String != "" {
			d.Notices = strings.Split(nos.String, ",")
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	dIn, dArgs := inClause(priceDetails)
	groups, err := s.priceChangeGroups(`c.effective_date <> '' AND c.detail IN (`+dIn+`)`, dArgs...)
	if err != nil {
		return nil, err
	}
	counts := map[string][3]int{}
	for _, g := range groups { // 품목별 최신 가격 고시 기준 (changesForDate 와 같은 latestOf)
		c := latestOf(g)
		v := counts[c.effDay]
		switch c.detail {
		case "price_down":
			v[0]++
		case "price_up":
			v[1]++
		case "agreed_price":
			v[2]++
		}
		counts[c.effDay] = v
	}
	for i := range out {
		v := counts[out[i].EffectiveDate]
		out[i].Down, out[i].Up, out[i].Agreed = v[0], v[1], v[2]
	}
	today := s.Today()
	tt, _ := domain.ParseDate(today)
	cutoff := domain.FormatDate(tt.AddDate(0, 0, -120))
	for i := range out {
		d := &out[i]
		d.IsPast = d.EffectiveDate <= today
		if et, err := domain.ParseDate(d.EffectiveDate); err == nil {
			d.DaysLeft = int(et.Sub(tt).Hours() / 24)
		}
		if d.EffectiveDate < cutoff || (d.Down == 0 && d.Delete == 0 && d.Agreed == 0) {
			continue
		}
		if d.Down > 0 || d.Agreed > 0 {
			if rk, err := s.Ranking(d.EffectiveDate, true); err == nil {
				d.HeldDown, d.GrossLoss, d.Recovery, d.NetLoss, d.Done = rk.Totals.Held, rk.Totals.GrossLoss, rk.Totals.Recovery, rk.Totals.NetLoss, rk.Totals.Done
				d.Computed = true
			}
		}
		if d.Delete > 0 {
			if dl, err := s.HeldDeletions(d.EffectiveDate); err == nil {
				d.HeldDelete = len(dl)
			}
		}
	}
	return out, nil
}

// ── 보유 중인 삭제(급여 제외) 품목 ────────────────────────────────

type DeletionRow struct {
	Code          string     `json:"code"`
	Name          string     `json:"name"`
	NoticeName    string     `json:"noticeName"`
	Company       string     `json:"company"`
	Stock         domain.Qty `json:"stock"`
	Price         float64    `json:"price"`
	Value         int64      `json:"value"`
	EffectiveDate string     `json:"effectiveDate"`
	GraceUntil    string     `json:"graceUntil"`
	NoticeNo      string     `json:"noticeNo"`
}

func (s *Service) HeldDeletions(eff string) ([]DeletionRow, error) {
	q := `SELECT c.product_code, c.product_name, c.company, IFNULL(c.price_before, 0), c.effective_date, c.grace_until, n.notice_no
		FROM price_change c JOIN notice n ON n.id = c.notice_id WHERE c.kind = 'delete'`
	var args []any
	if eff != "" {
		q += " AND c.effective_date = ?"
		args = append(args, eff)
	}
	stock, err := s.stockMap(s.Today(), true)
	if err != nil {
		return nil, err
	}
	briefs := s.productBriefs()
	rows, err := s.DB.Query(q+" ORDER BY c.effective_date DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []DeletionRow
	for rows.Next() {
		var d DeletionRow
		if err := rows.Scan(&d.Code, &d.NoticeName, &d.Company, &d.Price, &d.EffectiveDate, &d.GraceUntil, &d.NoticeNo); err != nil {
			return nil, err
		}
		if seen[d.Code] || stock[d.Code].Qty <= 0 {
			continue
		}
		seen[d.Code] = true
		d.Stock, d.Name = stock[d.Code].Qty, briefs[d.Code].Name
		if d.Name == "" {
			d.Name = d.NoticeName
		}
		d.Value = domain.RoundWon(d.Stock.Float() * d.Price)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	return out, nil
}

// ── 품목 상세 ─────────────────────────────────────────────────────

type PositionView struct {
	domain.SupplierPosition
	PosMeta
	ScannedUnits    int `json:"scannedUnits"`
	RegisteredUnits int `json:"registeredUnits"`
}

type PurchaseView struct {
	ID           int64      `json:"id"`
	Date         string     `json:"date"`
	SupplierID   int64      `json:"supplierId"`
	SupplierName string     `json:"supplierName"`
	Kind         string     `json:"kind"`
	Qty          domain.Qty `json:"qty"`    // 고시 단위
	RawQty       domain.Qty `json:"rawQty"` // 이팜 원본 (수량×단위수량)
	RawUnit      string     `json:"rawUnit"`
	Factor       float64    `json:"factor"`
	PackSize     float64    `json:"packSize"`
	UnitPrice    float64    `json:"unitPrice"` // 고시 단위 1개 단가
	Lot          string     `json:"lot"`
	Expiry       string     `json:"expiry"`
	InWindow     bool       `json:"inWindow"`
}

type ProductDetail struct {
	EffectiveDate string                 `json:"effectiveDate"`
	AsOf          string                 `json:"asOf"`
	Row           RankRow                `json:"row"`
	Decision      domain.ProductDecision `json:"decision"`
	Positions     []PositionView         `json:"positions"`
	Unattributed  domain.Qty             `json:"unattributed"`
	PackSize      float64                `json:"packSize"`
	Purchases     []PurchaseView         `json:"purchases"`
	Lots          []LotView              `json:"lots"`
	Scans         []ScanView             `json:"scans"`
	Settlements   []SettlementView       `json:"settlements"`
	Anchors       []AnchorView           `json:"anchors"`
	History       []ChangeRow            `json:"history"`
}

func (s *Service) ProductDetail(eff, code string) (*ProductDetail, error) {
	ac, err := s.newAnalysis(eff)
	if err != nil {
		return nil, err
	}
	c, err := s.latestChange(eff, code)
	if err != nil {
		return nil, fmt.Errorf("%s 시행 고시에서 %s 을(를) 찾을 수 없습니다", eff, code)
	}
	calcs, err := s.computePositions(ac, []string{code}, c.published)
	if err != nil {
		return nil, err
	}
	pc := calcs[code]
	row, dec := s.buildRankRow(ac, c, pc)
	d := &ProductDetail{EffectiveDate: eff, AsOf: ac.asOf, Row: row, Decision: dec, Unattributed: pc.unattributed, PackSize: ac.briefs[code].PackSize}
	windows := map[int64][2]string{}
	for i, p := range dec.Positions {
		pv := PositionView{SupplierPosition: p, PosMeta: pc.meta[i], ScannedUnits: pc.scan.unitsBySupp[p.SupplierID], RegisteredUnits: pc.scan.regBySupp[p.SupplierID]}
		d.Positions = append(d.Positions, pv)
		windows[p.SupplierID] = [2]string{p.WindowFrom, p.WindowTo}
	}
	// 사입 이력 (최근 순)
	rows, err := s.DB.Query(`SELECT p.id, p.date, IFNULL(p.supplier_id, 0), IFNULL(s.name, ''), p.kind, p.qty, IFNULL(p.raw_qty, p.qty), p.unit_label, p.factor,
		IFNULL(p.pack_size, 0), IFNULL(p.unit_price, 0), p.lot, p.expiry
		FROM purchase_line p LEFT JOIN supplier s ON s.id = p.supplier_id WHERE p.product_code = ? ORDER BY p.date DESC, p.id DESC LIMIT 300`, code)
	if err == nil {
		for rows.Next() {
			var v PurchaseView
			var q, rq int64
			if rows.Scan(&v.ID, &v.Date, &v.SupplierID, &v.SupplierName, &v.Kind, &q, &rq, &v.RawUnit, &v.Factor, &v.PackSize, &v.UnitPrice, &v.Lot, &v.Expiry) == nil {
				v.Qty, v.RawQty = domain.Qty(q), domain.Qty(rq)
				if w, ok := windows[v.SupplierID]; ok && w[0] != "" {
					v.InWindow = v.Date >= w[0] && v.Date <= w[1]
				}
				d.Purchases = append(d.Purchases, v)
			}
		}
		rows.Close()
	}
	d.Lots, _ = s.ListLots(code)
	d.Scans, _ = s.ListScans(eff, code)
	d.Settlements, _ = s.ListSettlements(SettlementFilter{EffectiveDate: eff, Code: code})
	d.Anchors, _ = s.ProductAnchors(code)
	if hist, err := s.DB.Query(`SELECT c.id, c.notice_id, c.kind, c.detail, IFNULL(c.price_before,0), IFNULL(c.price_after,0), c.effective_date, c.grace_until, c.note
		FROM price_change c WHERE c.product_code = ? ORDER BY c.effective_date DESC, c.id DESC`, code); err == nil {
		for hist.Next() {
			var h ChangeRow
			if hist.Scan(&h.ID, &h.NoticeID, &h.Kind, &h.Detail, &h.PriceBefore, &h.PriceAfter, &h.EffectiveDate, &h.GraceUntil, &h.Note) == nil {
				h.Code = code
				d.History = append(d.History, h)
			}
		}
		hist.Close()
	}
	return d, nil
}

// ── 회차별 대상기간 직접 지정 ─────────────────────────────────────

func (s *Service) SetWindowOverride(eff string, supplierID int64, from, to, memo string) error {
	for _, d := range []string{eff, from, to} {
		if _, err := domain.ParseDate(d); err != nil {
			return fmt.Errorf("날짜 형식 오류: %s", d)
		}
	}
	if from > to {
		return fmt.Errorf("시작일이 종료일보다 늦습니다")
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var of, ot, sup string
		_ = tx.QueryRow("SELECT window_from, window_to FROM window_override WHERE effective_date = ? AND supplier_id = ?", eff, supplierID).Scan(&of, &ot)
		_ = tx.QueryRow("SELECT name FROM supplier WHERE id = ?", supplierID).Scan(&sup)
		if _, err := tx.Exec(`INSERT INTO window_override(effective_date, supplier_id, window_from, window_to, memo) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(effective_date, supplier_id) DO UPDATE SET window_from = excluded.window_from, window_to = excluded.window_to, memo = excluded.memo`,
			eff, supplierID, from, to, memo); err != nil {
			return err
		}
		old := "기본 규칙"
		if of != "" {
			old = of + " ~ " + ot
		}
		return logChange(tx, ChangeEntry{Kind: "window_override", OldValue: old, NewValue: from + " ~ " + to, Note: fmt.Sprintf("%s 시행 · %s 서류보상 기간 · %s", eff, sup, memo), Source: "품목 판정"})
	})
}

func (s *Service) ClearWindowOverride(eff string, supplierID int64) error {
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var of, ot, sup string
		_ = tx.QueryRow("SELECT window_from, window_to FROM window_override WHERE effective_date = ? AND supplier_id = ?", eff, supplierID).Scan(&of, &ot)
		_ = tx.QueryRow("SELECT name FROM supplier WHERE id = ?", supplierID).Scan(&sup)
		if _, err := tx.Exec("DELETE FROM window_override WHERE effective_date = ? AND supplier_id = ?", eff, supplierID); err != nil {
			return err
		}
		return logChange(tx, ChangeEntry{Kind: "window_clear", OldValue: of + " ~ " + ot, NewValue: "기본 규칙", Note: fmt.Sprintf("%s 시행 · %s 서류보상 기간", eff, sup), Source: "품목 판정"})
	})
}

var _ = store.Now
