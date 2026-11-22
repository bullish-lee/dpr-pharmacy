package service

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// 처리 결과: (시행일, 약품, 도매) 별로 어떤 방식으로 몇 개를 보상받았는지 기록한다.
// '판정 확정' → planned(예정) 으로 저장 → 도매 처리 후 done(완료) 으로 바꾼다.

type SettlementView struct {
	ID            int64      `json:"id"`
	EffectiveDate string     `json:"effectiveDate"`
	Code          string     `json:"code"`
	Name          string     `json:"name"`
	SupplierID    int64      `json:"supplierId"`
	SupplierName  string     `json:"supplierName"`
	Method        string     `json:"method"`
	Qty           domain.Qty `json:"qty"`
	UnitDelta     float64    `json:"unitDelta"`
	Amount        float64    `json:"amount"`
	Status        string     `json:"status"`
	Memo          string     `json:"memo"`
	RuleSnapshot  string     `json:"ruleSnapshot"`
	DoneAt        string     `json:"doneAt"`
	UpdatedAt     string     `json:"updatedAt"`
}

type SettlementFilter struct {
	EffectiveDate string `json:"effectiveDate"`
	From          string `json:"from"`
	To            string `json:"to"`
	Code          string `json:"code"`
	SupplierID    int64  `json:"supplierId"`
	Status        string `json:"status"`
}

func (s *Service) ListSettlements(f SettlementFilter) ([]SettlementView, error) {
	q := `SELECT t.id, t.effective_date, t.product_code, IFNULL(p.name, ''), t.supplier_id, IFNULL(s.name, ''), t.method, t.qty, t.unit_delta, t.amount,
		t.status, t.memo, t.rule_snapshot, t.done_at, t.updated_at
		FROM settlement t LEFT JOIN product p ON p.code = t.product_code LEFT JOIN supplier s ON s.id = t.supplier_id WHERE 1 = 1`
	var args []any
	if f.EffectiveDate != "" {
		q += " AND t.effective_date = ?"
		args = append(args, f.EffectiveDate)
	}
	if f.From != "" {
		q += " AND t.effective_date >= ?"
		args = append(args, f.From)
	}
	if f.To != "" {
		q += " AND t.effective_date <= ?"
		args = append(args, f.To)
	}
	if f.Code != "" {
		q += " AND t.product_code = ?"
		args = append(args, f.Code)
	}
	if f.SupplierID != 0 {
		q += " AND t.supplier_id = ?"
		args = append(args, f.SupplierID)
	}
	if f.Status != "" {
		q += " AND t.status = ?"
		args = append(args, f.Status)
	}
	rows, err := s.DB.Query(q+" ORDER BY t.effective_date DESC, t.amount DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SettlementView
	for rows.Next() {
		var v SettlementView
		var qty int64
		if err := rows.Scan(&v.ID, &v.EffectiveDate, &v.Code, &v.Name, &v.SupplierID, &v.SupplierName, &v.Method, &qty, &v.UnitDelta, &v.Amount,
			&v.Status, &v.Memo, &v.RuleSnapshot, &v.DoneAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.Qty = domain.Qty(qty)
		out = append(out, v)
	}
	return out, rows.Err()
}

// ConfirmDecision 은 현재 판정(도매별 서류/실물)을 처리 예정으로 저장한다. 이미 완료된 건은 건드리지 않는다.
func (s *Service) ConfirmDecision(eff, code string) (int, error) {
	d, err := s.ProductDetail(eff, code)
	if err != nil {
		return 0, err
	}
	n := 0
	err = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		now := store.Now()
		keep := []any{eff, code}
		for _, p := range d.Positions {
			if p.Method != "none" && p.RecoverQty > 0 {
				keep = append(keep, p.SupplierID)
			}
		}
		// 도매가 비활성·'기타'로 바뀌었거나 보상할 것이 없어진 도매의 '예정' 행 — 남겨 두면 처리 내역에 없는 보상이 잡힌다
		q := "DELETE FROM settlement WHERE effective_date = ? AND product_code = ? AND status = 'planned'"
		if len(keep) > 2 {
			q += " AND supplier_id NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(keep)-2), ",") + ")"
		}
		if _, err := tx.Exec(q, keep...); err != nil {
			return err
		}
		for _, p := range d.Positions {
			if p.Method == "none" || p.RecoverQty <= 0 {
				continue
			}
			snap := toJSON(map[string]any{"rule": p.Rule, "ruleText": p.RuleText, "window": []string{p.WindowFrom, p.WindowTo},
				"purchased": p.Purchased, "returned": p.Returned, "docQty": p.DocQty, "held": p.HeldUsed, "scanned": p.ScannedHeld != nil})
			res, err := tx.Exec(`INSERT INTO settlement(effective_date, product_code, supplier_id, method, qty, unit_delta, amount, status, rule_snapshot, created_at, updated_at)
				VALUES(?, ?, ?, ?, ?, ?, ?, 'planned', ?, ?, ?)
				ON CONFLICT(effective_date, product_code, supplier_id) DO UPDATE SET method = excluded.method, qty = excluded.qty, unit_delta = excluded.unit_delta,
				  amount = excluded.amount, rule_snapshot = excluded.rule_snapshot, updated_at = excluded.updated_at
				WHERE settlement.status <> 'done'`,
				eff, code, p.SupplierID, p.Method, int64(p.RecoverQty), d.Row.Delta, float64(p.Recovery), snap, now, now)
			if err != nil {
				return err
			}
			if k, _ := res.RowsAffected(); k > 0 {
				n++
			}
		}
		return nil
	})
	return n, err
}

type SettlementInput struct {
	ID     int64   `json:"id"`
	Method string  `json:"method"`
	Qty    float64 `json:"qty"`
	Amount float64 `json:"amount"` // 실제 받은 금액 (0 = 받지 못함)
	Recalc bool    `json:"recalc"` // true 면 금액을 수량 × 인하폭으로 다시 계산
	Status string  `json:"status"` // planned | done
	Memo   string  `json:"memo"`
}

// UpdateSettlement 는 실제 처리 결과(수량·금액·상태)를 수정한다.
func (s *Service) UpdateSettlement(in SettlementInput) error {
	if in.Method != "document" && in.Method != "physical" {
		return errors.New("방식은 서류보상 또는 실물보상이어야 합니다")
	}
	if in.Status != "done" {
		in.Status = "planned"
	}
	var delta float64
	if err := s.DB.QueryRow("SELECT unit_delta FROM settlement WHERE id = ?", in.ID).Scan(&delta); err != nil {
		return ErrNotFound
	}
	qty := domain.QtyFromFloat(in.Qty)
	amount := in.Amount
	if in.Recalc {
		amount = float64(domain.RoundWon(qty.Float() * delta))
	}
	doneAt := ""
	if in.Status == "done" {
		doneAt = store.Now()
	}
	_, err := s.DB.Exec("UPDATE settlement SET method = ?, qty = ?, amount = ?, status = ?, memo = ?, done_at = CASE WHEN ? = '' THEN '' ELSE COALESCE(NULLIF(done_at, ''), ?) END, updated_at = ? WHERE id = ?",
		in.Method, int64(qty), amount, in.Status, in.Memo, doneAt, doneAt, store.Now(), in.ID)
	return err
}

func (s *Service) SetSettlementStatus(id int64, status string) error {
	if status != "done" && status != "planned" {
		return fmt.Errorf("알 수 없는 상태: %s", status)
	}
	done := ""
	if status == "done" {
		done = store.Now()
	}
	_, err := s.DB.Exec("UPDATE settlement SET status = ?, done_at = ?, updated_at = ? WHERE id = ?", status, done, store.Now(), id)
	return err
}

// FinishPhysical 은 도매 앱 등록을 마친 실물보상을 '완료'로 기록한다 (등록 화면의 [처리 완료로 기록]).
// 수량은 **도매 앱에 등록했다고 표시한 바코드의 수량**이다 — 판정 화면의 보유량에는 아직 안 찍은 재고의 추정분이 섞일 수 있어,
// 그 값으로 완료를 기록하면 등록하지 않은 수량까지 보상받은 것으로 남는다. 금액 = 등록 수량 × 인하폭.
func (s *Service) FinishPhysical(eff, code string, supplierID int64) (*SettlementView, error) {
	var regQty int64
	var regUnits int
	if err := s.DB.QueryRow(`SELECT IFNULL(SUM(qty), 0), COUNT(*) FROM scan_unit WHERE effective_date = ? AND product_code = ? AND supplier_id = ? AND registered = 1`,
		eff, code, supplierID).Scan(&regQty, &regUnits); err != nil {
		return nil, err
	}
	if regUnits == 0 {
		return nil, errors.New("도매 앱에 등록했다고 표시한 바코드가 없습니다 — 등록한 포장에 '스캔완료'를 표시한 뒤 기록하세요")
	}
	d, err := s.ProductDetail(eff, code)
	if err != nil {
		return nil, err
	}
	var pos *PositionView
	for i := range d.Positions {
		if d.Positions[i].SupplierID == supplierID {
			pos = &d.Positions[i]
		}
	}
	if pos == nil || pos.Rule == nil {
		return nil, errors.New("이 도매는 보상 대상이 아닙니다")
	}
	qty := domain.Qty(regQty)
	amount := float64(domain.RoundWon(qty.Float() * d.Row.Delta))
	snap := toJSON(map[string]any{"rule": pos.Rule, "ruleText": pos.RuleText, "window": []string{pos.WindowFrom, pos.WindowTo},
		"purchased": pos.Purchased, "returned": pos.Returned, "docQty": pos.DocQty, "held": pos.HeldUsed, "scanned": true,
		"registeredUnits": regUnits, "registeredQty": qty})
	now := store.Now()
	if _, err := s.DB.Exec(`INSERT INTO settlement(effective_date, product_code, supplier_id, method, qty, unit_delta, amount, status, rule_snapshot, done_at, created_at, updated_at)
		VALUES(?, ?, ?, 'physical', ?, ?, ?, 'done', ?, ?, ?, ?)
		ON CONFLICT(effective_date, product_code, supplier_id) DO UPDATE SET method = 'physical', qty = excluded.qty, unit_delta = excluded.unit_delta,
		  amount = excluded.amount, status = 'done', rule_snapshot = excluded.rule_snapshot, done_at = excluded.done_at, updated_at = excluded.updated_at`,
		eff, code, supplierID, int64(qty), d.Row.Delta, amount, snap, now, now, now); err != nil {
		return nil, err
	}
	list, err := s.ListSettlements(SettlementFilter{EffectiveDate: eff, Code: code})
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].SupplierID == supplierID {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

func (s *Service) DeleteSettlement(id int64) error {
	_, err := s.DB.Exec("DELETE FROM settlement WHERE id = ?", id)
	return err
}

// ── 리포트 ────────────────────────────────────────────────────────

type ReportGroup struct {
	Key      string     `json:"key"`
	Label    string     `json:"label"`
	Count    int        `json:"count"`
	Qty      domain.Qty `json:"qty"`
	Amount   float64    `json:"amount"`
	Document float64    `json:"document"`
	Physical float64    `json:"physical"`
	Done     int        `json:"done"`
	Planned  int        `json:"planned"`
}

type Report struct {
	Filter     SettlementFilter `json:"filter"`
	Total      ReportGroup      `json:"total"`
	ByDate     []ReportGroup    `json:"byDate"`
	BySupplier []ReportGroup    `json:"bySupplier"`
	ByProduct  []ReportGroup    `json:"byProduct"`
	Rows       []SettlementView `json:"rows"`
}

func (s *Service) Report(f SettlementFilter) (*Report, error) {
	rows, err := s.ListSettlements(f)
	if err != nil {
		return nil, err
	}
	r := &Report{Filter: f, Rows: rows, Total: ReportGroup{Key: "total", Label: "합계"}}
	byDate, bySup, byProd := map[string]*ReportGroup{}, map[string]*ReportGroup{}, map[string]*ReportGroup{}
	acc := func(m map[string]*ReportGroup, key, label string, v SettlementView) {
		g := m[key]
		if g == nil {
			g = &ReportGroup{Key: key, Label: label}
			m[key] = g
		}
		addTo(g, v)
	}
	for _, v := range rows {
		addTo(&r.Total, v)
		acc(byDate, v.EffectiveDate, v.EffectiveDate+" 시행", v)
		acc(bySup, fmt.Sprint(v.SupplierID), v.SupplierName, v)
		acc(byProd, v.Code, v.Name, v)
	}
	r.ByDate, r.BySupplier, r.ByProduct = flatten(byDate), flatten(bySup), flatten(byProd)
	sort.Slice(r.ByDate, func(i, j int) bool { return r.ByDate[i].Key > r.ByDate[j].Key })
	return r, nil
}

func addTo(g *ReportGroup, v SettlementView) {
	g.Count++
	g.Qty += v.Qty
	g.Amount += v.Amount
	if v.Method == "physical" {
		g.Physical += v.Amount
	} else {
		g.Document += v.Amount
	}
	if v.Status == "done" {
		g.Done++
	} else {
		g.Planned++
	}
}

func flatten(m map[string]*ReportGroup) []ReportGroup {
	out := make([]ReportGroup, 0, len(m))
	for _, g := range m {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
	return out
}
