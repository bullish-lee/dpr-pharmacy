package service

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// ── 거래처(도매처) ───────────────────────────────────────────────

type RuleView struct {
	ID          int64           `json:"id"`
	SupplierID  int64           `json:"supplierId"` // 0 = 기본 규칙
	ValidFrom   string          `json:"validFrom"`
	Rule        domain.CompRule `json:"rule"`
	NeedsReview bool            `json:"needsReview"`
	Memo        string          `json:"memo"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"isDefault"` // 도매 전용 규칙이 없어 기본 규칙을 쓰는 중
	UpdatedAt   string          `json:"updatedAt"`
}

type SupplierView struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Active        bool      `json:"active"`
	Deleted       bool      `json:"deleted"`
	Contact       string    `json:"contact"`
	Memo          string    `json:"memo"`
	Aliases       []string  `json:"aliases"`
	PurchaseLines int       `json:"purchaseLines"`
	LastPurchase  string    `json:"lastPurchase"`
	Rule          *RuleView `json:"rule"` // 오늘 기준 적용 규칙 (기타 거래처는 nil)
	RuleCount     int       `json:"ruleCount"`
}

const ruleCols = "id, IFNULL(supplier_id,0), valid_from, rate_bp, window_mode, lookback, cutoff_day, extend_to_anchor, anchor, rounding, net_returns, settlement_day, needs_review, memo, updated_at"

func scanRule(sc interface{ Scan(...any) error }) (RuleView, error) {
	var v RuleView
	var ext, net, review int
	err := sc.Scan(&v.ID, &v.SupplierID, &v.ValidFrom, &v.Rule.RateBP, &v.Rule.WindowMode, &v.Rule.Lookback, &v.Rule.CutoffDay,
		&ext, &v.Rule.Anchor, &v.Rule.Rounding, &net, &v.Rule.SettlementDay, &review, &v.Memo, &v.UpdatedAt)
	v.Rule.ExtendToAnchor, v.Rule.NetReturns, v.NeedsReview = ext == 1, net == 1, review == 1
	v.Description = v.Rule.Describe()
	return v, err
}

// RuleFor 는 date 기준으로 거래처에 적용할 규칙. 전용 규칙이 없으면 기본 규칙, 기타 거래처면 nil.
func (s *Service) RuleFor(q interface {
	QueryRow(string, ...any) *sql.Row
}, supplierID int64, kind string, active bool, date string) *RuleView {
	if kind != "wholesaler" || !active {
		return nil
	}
	row := q.QueryRow("SELECT "+ruleCols+" FROM comp_rule WHERE supplier_id = ? AND valid_from <= ? ORDER BY valid_from DESC LIMIT 1", supplierID, date)
	if v, err := scanRule(row); err == nil {
		return &v
	}
	row = q.QueryRow("SELECT "+ruleCols+" FROM comp_rule WHERE supplier_id IS NULL AND valid_from <= ? ORDER BY valid_from DESC LIMIT 1", date)
	if v, err := scanRule(row); err == nil {
		v.IsDefault = true
		return &v
	}
	return nil
}

func (s *Service) ListSuppliers(includeDeleted bool) ([]SupplierView, error) {
	q := `SELECT s.id, s.name, s.kind, s.active, s.deleted, s.contact, s.memo,
	        (SELECT COUNT(*) FROM purchase_line p WHERE p.supplier_id = s.id),
	        IFNULL((SELECT MAX(date) FROM purchase_line p WHERE p.supplier_id = s.id), ''),
	        (SELECT COUNT(*) FROM comp_rule r WHERE r.supplier_id = s.id),
	        IFNULL((SELECT GROUP_CONCAT(alias, '|') FROM supplier_alias a WHERE a.supplier_id = s.id), '')
	      FROM supplier s`
	if !includeDeleted {
		q += " WHERE s.deleted = 0"
	}
	q += " ORDER BY s.kind = 'other', (SELECT COUNT(*) FROM purchase_line p WHERE p.supplier_id = s.id) DESC, s.name"
	rows, err := s.DB.Query(q)
	if err != nil {
		return nil, err
	}
	var out []SupplierView
	for rows.Next() {
		var v SupplierView
		var active, deleted int
		var aliases string
		if err := rows.Scan(&v.ID, &v.Name, &v.Kind, &active, &deleted, &v.Contact, &v.Memo, &v.PurchaseLines, &v.LastPurchase, &v.RuleCount, &aliases); err != nil {
			rows.Close()
			return nil, err
		}
		v.Active, v.Deleted = active == 1, deleted == 1
		if aliases != "" {
			v.Aliases = strings.Split(aliases, "|")
		}
		out = append(out, v)
	}
	rows.Close()
	today := s.Today()
	for i := range out {
		out[i].Rule = s.RuleFor(s.DB, out[i].ID, out[i].Kind, out[i].Active, today)
	}
	return out, nil
}

type SupplierInput struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Active  bool   `json:"active"`
	Contact string `json:"contact"`
	Memo    string `json:"memo"`
}

func (s *Service) SaveSupplier(in SupplierInput) (int64, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return 0, errors.New("거래처 이름을 입력하세요")
	}
	if in.Kind != "wholesaler" && in.Kind != "other" {
		in.Kind = "wholesaler"
	}
	now := store.Now()
	if in.ID == 0 {
		res, err := s.DB.Exec("INSERT INTO supplier(name, kind, active, contact, memo, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?)",
			name, in.Kind, b2i(in.Active), in.Contact, in.Memo, now, now)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return 0, fmt.Errorf("이미 있는 거래처 이름입니다: %s", name)
			}
			return 0, err
		}
		id, _ := res.LastInsertId()
		_, _ = s.DB.Exec("INSERT OR IGNORE INTO supplier_alias(alias, supplier_id) VALUES(?, ?)", domain.NormalizeSupplierName(name), id)
		return id, nil
	}
	_, err := s.DB.Exec("UPDATE supplier SET name = ?, kind = ?, active = ?, contact = ?, memo = ?, updated_at = ? WHERE id = ?",
		name, in.Kind, b2i(in.Active), in.Contact, in.Memo, now, in.ID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return 0, fmt.Errorf("이미 있는 거래처 이름입니다: %s", name)
	}
	return in.ID, err
}

// DeleteSupplier : 사용 이력이 없으면 완전 삭제, 있으면 숨김(삭제됨) 처리하고 보상 기준을 지운다.
func (s *Service) DeleteSupplier(id int64) (string, error) {
	var used int
	err := s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM purchase_line WHERE supplier_id = ?) + (SELECT COUNT(*) FROM lot_evidence WHERE supplier_id = ?)
		+ (SELECT COUNT(*) FROM settlement WHERE supplier_id = ?) + (SELECT COUNT(*) FROM scan_unit WHERE supplier_id = ?)`, id, id, id, id).Scan(&used)
	if err != nil {
		return "", err
	}
	return "", s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM comp_rule WHERE supplier_id = ?", id); err != nil {
			return err
		}
		if used == 0 {
			_, err := tx.Exec("DELETE FROM supplier WHERE id = ?", id)
			return err
		}
		_, err := tx.Exec("UPDATE supplier SET deleted = 1, active = 0, updated_at = ? WHERE id = ?", store.Now(), id)
		return err
	})
}

func (s *Service) RestoreSupplier(id int64) error {
	_, err := s.DB.Exec("UPDATE supplier SET deleted = 0, active = 1, updated_at = ? WHERE id = ?", store.Now(), id)
	return err
}

// MergeSupplier 는 from 의 별칭·사입 기록·근거를 into 로 옮기고 from 을 지운다 (같은 도매가 다른 이름으로 들어온 경우).
func (s *Service) MergeSupplier(fromID, intoID int64) error {
	if fromID == intoID {
		return errors.New("같은 거래처입니다")
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		for _, q := range []string{
			"UPDATE supplier_alias SET supplier_id = ? WHERE supplier_id = ?",
			"UPDATE purchase_line SET supplier_id = ? WHERE supplier_id = ?",
			"UPDATE lot_evidence SET supplier_id = ? WHERE supplier_id = ?",
			"UPDATE scan_unit SET supplier_id = ? WHERE supplier_id = ?",
			"UPDATE OR IGNORE settlement SET supplier_id = ? WHERE supplier_id = ?",
		} {
			if _, err := tx.Exec(q, intoID, fromID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("DELETE FROM settlement WHERE supplier_id = ?", fromID); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM supplier WHERE id = ?", fromID)
		return err
	})
}

// ── 보상 기준 ─────────────────────────────────────────────────────

func (s *Service) ListRules(supplierID int64) ([]RuleView, error) {
	var rows *sql.Rows
	var err error
	if supplierID == 0 {
		rows, err = s.DB.Query("SELECT " + ruleCols + " FROM comp_rule WHERE supplier_id IS NULL ORDER BY valid_from DESC")
	} else {
		rows, err = s.DB.Query("SELECT "+ruleCols+" FROM comp_rule WHERE supplier_id = ? ORDER BY valid_from DESC", supplierID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuleView
	for rows.Next() {
		v, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type RuleInput struct {
	ID          int64           `json:"id"`         // 0 = 새 규칙(버전)
	SupplierID  int64           `json:"supplierId"` // 0 = 기본 규칙
	ValidFrom   string          `json:"validFrom"`
	Rule        domain.CompRule `json:"rule"`
	NeedsReview bool            `json:"needsReview"`
	Memo        string          `json:"memo"`
}

func (in *RuleInput) normalize() error {
	if in.ValidFrom == "" {
		in.ValidFrom = "2000-01-01"
	}
	if _, err := domain.ParseDate(in.ValidFrom); err != nil {
		return fmt.Errorf("적용 시작일 형식 오류: %s", in.ValidFrom)
	}
	return in.Rule.Validate()
}

func supplierArg(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func upsertRule(tx *sql.Tx, in RuleInput) error {
	now := store.Now()
	r := in.Rule
	if in.ID > 0 {
		_, err := tx.Exec(`UPDATE comp_rule SET valid_from = ?, rate_bp = ?, window_mode = ?, lookback = ?, cutoff_day = ?, extend_to_anchor = ?, anchor = ?,
			rounding = ?, net_returns = ?, settlement_day = ?, needs_review = ?, memo = ?, updated_at = ? WHERE id = ?`,
			in.ValidFrom, r.RateBP, r.WindowMode, r.Lookback, r.CutoffDay, b2i(r.ExtendToAnchor), r.Anchor, r.Rounding, b2i(r.NetReturns),
			r.SettlementDay, b2i(in.NeedsReview), in.Memo, now, in.ID)
		return err
	}
	_, err := tx.Exec(`INSERT INTO comp_rule(supplier_id, valid_from, rate_bp, window_mode, lookback, cutoff_day, extend_to_anchor, anchor, rounding, net_returns, settlement_day, needs_review, memo, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(IFNULL(supplier_id, 0), valid_from) DO UPDATE SET rate_bp = excluded.rate_bp, window_mode = excluded.window_mode, lookback = excluded.lookback,
		  cutoff_day = excluded.cutoff_day, extend_to_anchor = excluded.extend_to_anchor, anchor = excluded.anchor, rounding = excluded.rounding,
		  net_returns = excluded.net_returns, settlement_day = excluded.settlement_day, needs_review = excluded.needs_review, memo = excluded.memo, updated_at = excluded.updated_at`,
		supplierArg(in.SupplierID), in.ValidFrom, r.RateBP, r.WindowMode, r.Lookback, r.CutoffDay, b2i(r.ExtendToAnchor), r.Anchor, r.Rounding,
		b2i(r.NetReturns), r.SettlementDay, b2i(in.NeedsReview), in.Memo, now, now)
	return err
}

// SaveRules 는 여러 거래처의 기준을 한 번에 저장한다(일괄 저장). 하나라도 잘못되면 전부 취소.
func (s *Service) SaveRules(inputs []RuleInput) error {
	for i := range inputs {
		if err := inputs[i].normalize(); err != nil {
			return fmt.Errorf("%d번째 기준: %w", i+1, err)
		}
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		for _, in := range inputs {
			if err := upsertRule(tx, in); err != nil {
				return err
			}
		}
		return nil
	})
}

// ApplyRuleToSuppliers 는 같은 기준을 선택한 거래처들에 일괄 적용한다.
func (s *Service) ApplyRuleToSuppliers(template RuleInput, supplierIDs []int64) error {
	if err := template.normalize(); err != nil {
		return err
	}
	if len(supplierIDs) == 0 {
		return errors.New("적용할 거래처를 선택하세요")
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		for _, id := range supplierIDs {
			in := template
			in.ID, in.SupplierID = 0, id
			if err := upsertRule(tx, in); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteRule : 기본 규칙의 마지막 버전은 지울 수 없다.
func (s *Service) DeleteRule(id int64) error {
	var supplierID sql.NullInt64
	if err := s.DB.QueryRow("SELECT supplier_id FROM comp_rule WHERE id = ?", id).Scan(&supplierID); err != nil {
		return ErrNotFound
	}
	if !supplierID.Valid {
		var n int
		_ = s.DB.QueryRow("SELECT COUNT(*) FROM comp_rule WHERE supplier_id IS NULL").Scan(&n)
		if n <= 1 {
			return errors.New("기본 규칙은 최소 1개가 필요합니다 (수정은 가능)")
		}
	}
	_, err := s.DB.Exec("DELETE FROM comp_rule WHERE id = ?", id)
	return err
}
