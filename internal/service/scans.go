package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// ── 사입 근거 (제조번호·유효기간) ─────────────────────────────────

type LotView struct {
	ID           int64      `json:"id"`     // lot_evidence id (사입현황 출처면 0)
	Source       string     `json:"source"` // purchase | import | manual | photo
	SupplierID   int64      `json:"supplierId"`
	SupplierName string     `json:"supplierName"`
	Date         string     `json:"date"`
	Qty          domain.Qty `json:"qty"`
	Lot          string     `json:"lot"`
	Expiry       string     `json:"expiry"`
}

// ListLots 는 약품의 제조번호 근거 (사입현황의 제조번호 열 + 별도 근거) 를 합쳐 돌려준다.
func (s *Service) ListLots(code string) ([]LotView, error) {
	rows, err := s.DB.Query(`
		SELECT 0, 'purchase', IFNULL(p.supplier_id, 0), IFNULL(s.name, ''), p.date, p.qty, p.lot, p.expiry
		  FROM purchase_line p LEFT JOIN supplier s ON s.id = p.supplier_id
		 WHERE p.product_code = ? AND p.lot <> '' AND p.kind = 'purchase'
		UNION ALL
		SELECT l.id, l.source, l.supplier_id, IFNULL(s.name, ''), l.date, l.qty, l.lot, l.expiry
		  FROM lot_evidence l LEFT JOIN supplier s ON s.id = l.supplier_id WHERE l.product_code = ?
		ORDER BY 5 DESC`, code, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LotView
	for rows.Next() {
		var v LotView
		var q int64
		if err := rows.Scan(&v.ID, &v.Source, &v.SupplierID, &v.SupplierName, &v.Date, &q, &v.Lot, &v.Expiry); err != nil {
			return nil, err
		}
		v.Qty = domain.Qty(q)
		out = append(out, v)
	}
	return out, rows.Err()
}

type LotInput struct {
	Code       string  `json:"code"`
	SupplierID int64   `json:"supplierId"`
	Date       string  `json:"date"`
	Qty        float64 `json:"qty"`
	Lot        string  `json:"lot"`
	Expiry     string  `json:"expiry"`
	Source     string  `json:"source"`
}

func (s *Service) AddLot(in LotInput) (int64, error) {
	code := domain.NormalizeProductCode(in.Code)
	if code == "" || in.SupplierID == 0 || strings.TrimSpace(in.Lot) == "" {
		return 0, errors.New("약품·거래처·제조번호는 필수입니다")
	}
	if in.Expiry != "" {
		e, ok := domain.ParseFlexibleDate(in.Expiry)
		if !ok {
			return 0, fmt.Errorf("유효기간 형식 오류: %s", in.Expiry)
		}
		in.Expiry = e
	}
	if in.Source == "" {
		in.Source = "manual"
	}
	res, err := s.DB.Exec("INSERT INTO lot_evidence(product_code, supplier_id, date, qty, lot, expiry, source, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
		code, in.SupplierID, in.Date, int64(domain.QtyFromFloat(in.Qty)), strings.TrimSpace(in.Lot), in.Expiry, in.Source, store.Now())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	s.rematchScans(code)
	return id, nil
}

func (s *Service) DeleteLot(id int64) error {
	var code string
	_ = s.DB.QueryRow("SELECT product_code FROM lot_evidence WHERE id = ?", id).Scan(&code)
	_, err := s.DB.Exec("DELETE FROM lot_evidence WHERE id = ?", id)
	if err == nil && code != "" {
		s.rematchScans(code)
	}
	return err
}

func (s *Service) lotRecords(code string) []domain.LotRecord {
	lots, _ := s.ListLots(code)
	out := make([]domain.LotRecord, 0, len(lots))
	for _, l := range lots {
		if l.SupplierID > 0 {
			out = append(out, domain.LotRecord{SupplierID: l.SupplierID, Lot: l.Lot, Expiry: l.Expiry})
		}
	}
	return out
}

// rematchScans : 근거가 추가·삭제되면 자동 매칭 결과를 다시 계산 (사용자가 직접 지정한 건은 유지)
func (s *Service) rematchScans(code string) {
	recs := s.lotRecords(code)
	rows, err := s.DB.Query("SELECT id, lot, expiry FROM scan_unit WHERE product_code = ? AND resolved_by <> 'user'", code)
	if err != nil {
		return
	}
	type upd struct {
		id    int64
		m     domain.LotMatch
		owner sql.NullInt64
	}
	var ups []upd
	for rows.Next() {
		var id int64
		var lot, exp string
		if rows.Scan(&id, &lot, &exp) == nil {
			m := domain.MatchLot(lot, exp, recs)
			u := upd{id: id, m: m}
			if m.Status == "unique" {
				u.owner = sql.NullInt64{Int64: m.SupplierIDs[0], Valid: true}
			}
			ups = append(ups, u)
		}
	}
	rows.Close()
	for _, u := range ups {
		by := ""
		if u.owner.Valid {
			by = "auto"
		}
		_, _ = s.DB.Exec("UPDATE scan_unit SET match_status = ?, candidates = ?, supplier_id = ?, resolved_by = ? WHERE id = ?",
			u.m.Status, toJSON(u.m.SupplierIDs), u.owner, by, u.id)
	}
}

// ── 바코드 스캔 ───────────────────────────────────────────────────

type ScanView struct {
	ID            int64      `json:"id"`
	EffectiveDate string     `json:"effectiveDate"`
	Code          string     `json:"code"`
	GTIN          string     `json:"gtin"`
	Lot           string     `json:"lot"`
	Expiry        string     `json:"expiry"`
	Serial        string     `json:"serial"`
	Raw           string     `json:"raw"`
	Human         string     `json:"human"` // 재표시용 GS1 괄호 표기
	Qty           domain.Qty `json:"qty"`
	MatchStatus   string     `json:"matchStatus"`
	Candidates    []IDName   `json:"candidates"`
	SupplierID    int64      `json:"supplierId"`
	SupplierName  string     `json:"supplierName"`
	ResolvedBy    string     `json:"resolvedBy"`
	Registered    bool       `json:"registered"`
	CreatedAt     string     `json:"createdAt"`
}

type IDName struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (s *Service) ListScans(eff, code string) ([]ScanView, error) {
	q := `SELECT u.id, u.effective_date, u.product_code, u.gtin, u.lot, u.expiry, u.serial, u.raw, u.qty, u.match_status, u.candidates,
		IFNULL(u.supplier_id, 0), IFNULL(s.name, ''), u.resolved_by, u.registered, u.created_at
		FROM scan_unit u LEFT JOIN supplier s ON s.id = u.supplier_id WHERE u.effective_date = ?`
	args := []any{eff}
	if code != "" {
		q += " AND u.product_code = ?"
		args = append(args, code)
	}
	sup := s.suppliersMap() // 결과 행을 열기 전에 조회 (열린 커서 안에서 다른 쿼리 금지)
	rows, err := s.DB.Query(q+" ORDER BY u.id DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScanView
	for rows.Next() {
		var v ScanView
		var q int64
		var cands string
		var reg int
		if err := rows.Scan(&v.ID, &v.EffectiveDate, &v.Code, &v.GTIN, &v.Lot, &v.Expiry, &v.Serial, &v.Raw, &q, &v.MatchStatus, &cands,
			&v.SupplierID, &v.SupplierName, &v.ResolvedBy, &reg, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Qty, v.Registered = domain.Qty(q), reg == 1
		var ids []int64
		_ = json.Unmarshal([]byte(cands), &ids)
		for _, id := range ids {
			v.Candidates = append(v.Candidates, IDName{id, sup[id].Name})
		}
		v.Human = humanGS1(v.GTIN, v.Expiry, v.Lot, v.Serial)
		out = append(out, v)
	}
	return out, rows.Err()
}

func humanGS1(gtin, expiry, lot, serial string) string {
	g := domain.GS1{GTIN: gtin, Lot: lot, Serial: serial, Elements: map[string]string{}}
	if t, err := domain.ParseDate(expiry); err == nil {
		g.Elements["17"] = t.Format("060102")
	}
	return g.Human()
}

type ScanInput struct {
	EffectiveDate string  `json:"effectiveDate"`
	ProductCode   string  `json:"productCode"` // 작업 중인 약품 (GTIN 학습에 사용)
	Raw           string  `json:"raw"`
	Qty           float64 `json:"qty"` // 0 이면 품목 포장 수량
}

type ScanResult struct {
	Status     string      `json:"status"` // added | duplicate | unknown_product | other_product | invalid
	Message    string      `json:"message"`
	Unit       *ScanView   `json:"unit"`
	Parsed     *domain.GS1 `json:"parsed"`
	Candidates []Candidate `json:"candidates"`
}

// RecordScan 은 바코드 1개를 기록한다. 같은 일련번호는 중복으로 처리한다.
func (s *Service) RecordScan(in ScanInput) (*ScanResult, error) {
	if _, err := domain.ParseDate(in.EffectiveDate); err != nil {
		return nil, fmt.Errorf("시행일 형식 오류: %s", in.EffectiveDate)
	}
	g, ok := domain.ParseGS1(in.Raw)
	if !ok {
		return &ScanResult{Status: "invalid", Message: "GS1 바코드(01 GTIN)를 읽지 못했습니다. 포장의 2D 바코드를 다시 찍어주세요."}, nil
	}
	res := &ScanResult{Parsed: &g}
	code := ""
	_ = s.DB.QueryRow("SELECT product_code FROM gtin_map WHERE gtin = ?", g.GTIN).Scan(&code)
	if code == "" {
		prefix := domain.EDIPrefixFromGTIN(g.GTIN)
		var cands []Candidate
		if prefix != "" {
			rows, err := s.DB.Query("SELECT code, name FROM product WHERE code LIKE ? ORDER BY code", prefix+"_")
			if err == nil {
				for rows.Next() {
					var c Candidate
					if rows.Scan(&c.Code, &c.Name) == nil {
						cands = append(cands, c)
					}
				}
				rows.Close()
			}
		}
		// 후보가 여럿이면 '보험코드 9번째 자리 = 표준코드 12번째 자리' 규칙으로 좁힌다 (실측으로 모호 사례의 85% 해소)
		if ean := g.EAN13(); len(cands) > 1 && len(ean) == 13 {
			var narrowed []Candidate
			for _, c := range cands {
				if len(c.Code) == 9 && c.Code[8] == ean[11] {
					narrowed = append(narrowed, c)
				}
			}
			if len(narrowed) == 1 {
				cands = narrowed
			}
		}
		want := domain.NormalizeProductCode(in.ProductCode)
		switch {
		case want != "" && containsCode(cands, want):
			code = want
		case len(cands) == 1: // 표준코드 규칙으로 하나로 특정됨 (작업 중인 약품과 다르면 아래에서 '다른 약품' 처리)
			code = cands[0].Code
		case want != "" && len(cands) == 0:
			// 표준코드 규칙으로 후보를 못 찾았지만 작업 중인 약품이 있으면 사용자 확인 필요
			res.Status, res.Candidates = "unknown_product", []Candidate{{Code: want, Name: s.productName(want)}}
			res.Message = "이 바코드의 약품을 확인해 주세요 (처음 보는 GTIN)"
			return res, nil
		}
		if code == "" {
			res.Status, res.Candidates = "unknown_product", cands
			res.Message = "이 바코드의 약품을 선택해 주세요 (한 번 선택하면 기억합니다)"
			return res, nil
		}
		_, _ = s.DB.Exec("INSERT OR IGNORE INTO gtin_map(gtin, product_code, created_at, source) VALUES(?, ?, ?, 'heuristic')", g.GTIN, code, store.Now())
	}
	if want := domain.NormalizeProductCode(in.ProductCode); want != "" && want != code {
		res.Status = "other_product"
		res.Message = fmt.Sprintf("다른 약품의 바코드입니다: %s", s.productName(code))
		res.Candidates = []Candidate{{Code: code, Name: s.productName(code)}}
		return res, nil
	}
	if g.Serial != "" {
		var existing int64
		if s.DB.QueryRow("SELECT id FROM scan_unit WHERE effective_date = ? AND gtin = ? AND serial = ?", in.EffectiveDate, g.GTIN, g.Serial).Scan(&existing) == nil {
			res.Status, res.Message = "duplicate", "이미 스캔한 포장입니다 (같은 일련번호)"
			res.Unit = s.scanByID(existing)
			return res, nil
		}
	}
	qty := domain.QtyFromFloat(in.Qty)
	if qty <= 0 {
		qty = domain.QtyFromFloat(s.barcodeUnits(code, g.GTIN))
	}
	m := domain.MatchLot(g.Lot, g.Expiry, s.lotRecords(code))
	var owner sql.NullInt64
	by := ""
	if m.Status == "unique" {
		owner, by = sql.NullInt64{Int64: m.SupplierIDs[0], Valid: true}, "auto"
	}
	r, err := s.DB.Exec(`INSERT INTO scan_unit(effective_date, product_code, gtin, lot, expiry, serial, raw, qty, match_status, candidates, supplier_id, resolved_by, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.EffectiveDate, code, g.GTIN, g.Lot, g.Expiry, g.Serial, in.Raw, int64(qty), m.Status, toJSON(m.SupplierIDs), owner, by, store.Now())
	if err != nil {
		return nil, err
	}
	id, _ := r.LastInsertId()
	res.Status, res.Unit = "added", s.scanByID(id)
	switch m.Status {
	case "unique":
		res.Message = "사입 도매: " + res.Unit.SupplierName
	case "ambiguous":
		res.Message = fmt.Sprintf("같은 제조번호가 %d곳 도매 사입에 있습니다 — 도매 앱에서 확인 후 지정하세요", len(m.SupplierIDs))
	default:
		res.Message = "제조번호와 일치하는 사입 근거가 없습니다 — 사입 근거를 추가하거나 도매를 직접 지정하세요"
	}
	// 같은 일련번호(포장 하나)는 한 번만 보상된다 (협업 약사, 2026-11-25) — 다른 회차에서 이미 도매 앱에 등록한 포장이면 알린다.
	// 두 번째 인하에도 다시 보상되는지는 아직 확인 중이라 막지는 않는다.
	if g.Serial != "" {
		var other string
		if s.DB.QueryRow("SELECT effective_date FROM scan_unit WHERE gtin = ? AND serial = ? AND effective_date <> ? AND registered = 1 ORDER BY effective_date DESC LIMIT 1",
			g.GTIN, g.Serial, in.EffectiveDate).Scan(&other) == nil {
			res.Message += fmt.Sprintf(" · 주의: 이 포장(같은 일련번호)은 %s 회차에 이미 도매 앱에 등록했습니다 — 도매가 다시 받지 않을 수 있습니다", other)
		}
	}
	return res, nil
}

func containsCode(cands []Candidate, code string) bool {
	for _, c := range cands {
		if c.Code == code {
			return true
		}
	}
	return false
}

func (s *Service) productName(code string) string {
	var n string
	_ = s.DB.QueryRow("SELECT name FROM product WHERE code = ?", code).Scan(&n)
	if n == "" {
		return code
	}
	return n
}

func (s *Service) scanByID(id int64) *ScanView {
	var eff, code string
	if s.DB.QueryRow("SELECT effective_date, product_code FROM scan_unit WHERE id = ?", id).Scan(&eff, &code) != nil {
		return nil
	}
	list, _ := s.ListScans(eff, code)
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// ConfirmGTIN 은 바코드 GTIN 이 어떤 약품인지 사용자가 확정한다 (다음부터 자동).
func (s *Service) ConfirmGTIN(gtin, code string) error {
	code = domain.NormalizeProductCode(code)
	if gtin == "" || code == "" {
		return errors.New("GTIN 과 약품코드가 필요합니다")
	}
	_, err := s.DB.Exec("INSERT INTO gtin_map(gtin, product_code, created_at, source) VALUES(?, ?, ?, 'user') ON CONFLICT(gtin) DO UPDATE SET product_code = excluded.product_code, source = 'user'",
		gtin, code, store.Now())
	return err
}

// ResolveScan 은 '확인 필요' 스캔의 도매를 사용자가 지정한다 (0 = 지정 해제).
func (s *Service) ResolveScan(id, supplierID int64) error {
	var owner any
	by := ""
	if supplierID > 0 {
		owner, by = supplierID, "user"
	}
	_, err := s.DB.Exec("UPDATE scan_unit SET supplier_id = ?, resolved_by = ? WHERE id = ?", owner, by, id)
	return err
}

func (s *Service) SetScanRegistered(id int64, registered bool) error {
	_, err := s.DB.Exec("UPDATE scan_unit SET registered = ? WHERE id = ?", b2i(registered), id)
	return err
}

func (s *Service) SetScanQty(id int64, qty float64) error {
	if qty <= 0 {
		return errors.New("수량은 0보다 커야 합니다")
	}
	_, err := s.DB.Exec("UPDATE scan_unit SET qty = ? WHERE id = ?", int64(domain.QtyFromFloat(qty)), id)
	return err
}

func (s *Service) DeleteScan(id int64) error {
	_, err := s.DB.Exec("DELETE FROM scan_unit WHERE id = ?", id)
	return err
}

// barcodeUnits : 바코드(포장) 1개 = 고시 단위 몇 개.
// 사용자 지정 → 표준코드 제품총수량(고시 단위로 환산, 예: 28정 PTP → 1팩) → 사입 포장 환산 → 1
func (s *Service) barcodeUnits(code, gtin string) float64 {
	var manual, canon, legacy sql.NullFloat64
	_ = s.DB.QueryRow("SELECT pack_manual, canon_pack, pack_size FROM product WHERE code = ?", code).Scan(&manual, &canon, &legacy)
	if manual.Valid && manual.Float64 > 0 {
		return manual.Float64
	}
	var pq sql.NullFloat64
	_ = s.DB.QueryRow("SELECT pack_qty FROM gtin_map WHERE gtin = ?", gtin).Scan(&pq)
	if pq.Valid && pq.Float64 > 0 {
		if pu, ok := priceUnitFor(s.DB, code); ok {
			if n := pu.PerBarcode(pq.Float64); n > 0 {
				return n
			}
		}
		return pq.Float64
	}
	if canon.Valid && canon.Float64 > 0 {
		return canon.Float64
	}
	if legacy.Valid && legacy.Float64 > 0 {
		return legacy.Float64
	}
	return 1
}
