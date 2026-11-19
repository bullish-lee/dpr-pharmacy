package service

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// 수정 기록 — 사용자가 고친 내역을 남겨 나중에 확인할 수 있게 한다 (change_log 표).
// 실계수·단위 배율·포장 수량·초기재고 지정/제외·기준일·데이터 점검 확인·보상 기간 변경을 기록한다.

type ChangeEntry struct {
	ID          int64  `json:"id"`
	At          string `json:"at"`
	Kind        string `json:"kind"`
	ProductCode string `json:"productCode"`
	ProductName string `json:"productName"`
	OldValue    string `json:"oldValue"`
	NewValue    string `json:"newValue"`
	Note        string `json:"note"`
	Source      string `json:"source"`
}

// logChange 는 같은 트랜잭션 안에서 기록한다 (q = tx 또는 DB).
func logChange(q querier, e ChangeEntry) error {
	if e.ProductCode != "" && e.ProductName == "" {
		_ = q.QueryRow("SELECT name FROM product WHERE code = ?", e.ProductCode).Scan(&e.ProductName)
	}
	_, err := q.Exec(`INSERT INTO change_log(at, kind, product_code, product_name, old_value, new_value, note, source) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		store.Now(), e.Kind, e.ProductCode, e.ProductName, e.OldValue, e.NewValue, strings.TrimSpace(e.Note), e.Source)
	return err
}

type ChangeFilter struct {
	ProductCode string `json:"productCode"`
	Kind        string `json:"kind"`
	Query       string `json:"query"` // 약품명·코드 일부
	Limit       int    `json:"limit"`
}

// ChangeLog 는 수정 기록을 최근 것부터 돌려준다.
func (s *Service) ChangeLog(f ChangeFilter) ([]ChangeEntry, error) {
	if f.Limit <= 0 || f.Limit > 2000 {
		f.Limit = 500
	}
	where, args := []string{"1=1"}, []any{}
	if c := domain.NormalizeProductCode(f.ProductCode); c != "" {
		where, args = append(where, "product_code = ?"), append(args, c)
	}
	if f.Kind != "" {
		where, args = append(where, "kind = ?"), append(args, f.Kind)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		where, args = append(where, "(product_name LIKE ? OR product_code LIKE ?)"), append(args, "%"+q+"%", q+"%")
	}
	rows, err := s.DB.Query(`SELECT id, at, kind, product_code, product_name, old_value, new_value, note, source FROM change_log
		WHERE `+strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, append(args, f.Limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeEntry
	for rows.Next() {
		var e ChangeEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Kind, &e.ProductCode, &e.ProductName, &e.OldValue, &e.NewValue, &e.Note, &e.Source); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ── 데이터 점검 '확인함' ─────────────────────────────────────────

// ackState 는 확인 당시의 상태 (바뀌면 다시 보여준다): 수량 문제는 재고 수량, 단위 문제는 단위 상태·근거
func ackState(flag string, si StockInfo, b productBrief) string {
	switch flag {
	case FlagNegative, FlagOverstock:
		return strconv.FormatFloat(si.Qty.Float(), 'f', -1, 64)
	default:
		return b.UnitStatus + "|" + b.UnitNote
	}
}

// ackStillValid : '확인함' 표시가 아직 유효한가.
// 수량 문제(음수·과다)는 확인 당시 재고에서 ±20%(최소 10단위) 안이면 유지 — 조제를 새로 올릴 때마다 다시 확인하지 않게.
// 단위 문제는 단위 상태·근거가 그대로일 때만 유지.
func ackStillValid(flag, state string, si StockInfo, b productBrief) bool {
	switch flag {
	case FlagNegative, FlagOverstock:
		then, err := strconv.ParseFloat(strings.TrimSpace(state), 64)
		if err != nil {
			return state == ackState(flag, si, b)
		}
		now := si.Qty.Float()
		if (then < 0) != (now < 0) {
			return false
		}
		tol := math.Max(10, math.Abs(then)*0.2)
		return math.Abs(now-then) <= tol
	default:
		return state == ackState(flag, si, b)
	}
}

// acks : '확인함' 표시 (캐시). 읽기에 실패하면 빈 목록을 캐시하지 않는다 — 캐시하면 모든 확인 표시가 사라진 것처럼 보인다.
func (s *Service) acks() map[string]string {
	m, err := cached(s, "acks", func() (map[string]string, error) {
		out := map[string]string{}
		rows, err := s.DB.Query("SELECT product_code, flag, state FROM review_ack")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var c, f, st string
			if err := rows.Scan(&c, &f, &st); err != nil {
				return nil, err
			}
			out[c+"|"+f] = st
		}
		return out, rows.Err()
	}, cloneMap[string, string])
	if err != nil {
		s.Log.Warn("확인 표시 조회 실패", "err", err)
		return map[string]string{}
	}
	return m
}

// AckHealth : 데이터 점검 항목을 '확인함(이상 없음)'으로 표시한다. 상태가 바뀌면 다시 나타난다.
func (s *Service) AckHealth(code, flag, note, source string) error {
	code = domain.NormalizeProductCode(code)
	switch flag {
	case FlagNegative, FlagUnitCheck, FlagOverstock, FlagUnitAuto:
	default:
		return fmt.Errorf("알 수 없는 점검 항목: %s", flag)
	}
	stock, err := s.stockMap(s.Today(), true)
	if err != nil {
		return err
	}
	b := s.productBriefs()[code]
	state := ackState(flag, stock[code], b)
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO review_ack(product_code, flag, state, note, created_at) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(product_code, flag) DO UPDATE SET state = excluded.state, note = excluded.note, created_at = excluded.created_at`,
			code, flag, state, strings.TrimSpace(note), store.Now()); err != nil {
			return err
		}
		return logChange(tx, ChangeEntry{Kind: "ack", ProductCode: code, NewValue: flagLabel(flag) + " 확인함", Note: note, Source: source})
	})
}

func (s *Service) UnackHealth(code, flag, source string) error {
	code = domain.NormalizeProductCode(code)
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		res, err := tx.Exec("DELETE FROM review_ack WHERE product_code = ? AND flag = ?", code, flag)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errors.New("확인 표시가 없습니다")
		}
		return logChange(tx, ChangeEntry{Kind: "unack", ProductCode: code, OldValue: flagLabel(flag) + " 확인함", NewValue: "다시 점검", Source: source})
	})
}

func flagLabel(f string) string {
	switch f {
	case FlagNegative:
		return "음수 재고"
	case FlagUnitCheck:
		return "단위 확인"
	case FlagOverstock:
		return "과다 재고"
	case FlagUnitAuto:
		return "자동 환산"
	}
	return f
}

// fmtQty : 수량 표시 (수정 기록용)
func fmtQtyUnit(q domain.Qty, unit string) string { return q.String() + unit }
