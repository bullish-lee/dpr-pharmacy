package service

import (
	"database/sql"
	"fmt"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// 제품코드 변경 — 약가기준정보에서 '삭제'된 품목은 두 가지다.
//   - 급여 중단: 변경이후약품코드가 없다 → 급여목록에서 뺀다 (약국 기록이 있으면 storeDrugMaster 가 in_list = 0 으로 남긴다).
//   - 코드 변경: 변경이후약품코드가 있다 → 옛 코드의 약국 기록(사입·조제·초기재고·실사·제조번호·바코드)을 새 코드로 옮겨 약품을 이어 간다.
// 옛 코드와 새 코드의 고시 단위(규격·단위)가 다르면 옮기지 않는다 — 조제 수량은 고시 단위로만 남아 있어 옮기면 재고가 틀어진다.
// 고시(price_change)·정산(settlement)·바코드 작업(scan_unit)·수정 기록은 그때의 기록이라 옮기지 않는다.

const (
	codeMerged = "merged"
	codeReview = "review"
)

// codeChange : 옛 코드 → 새 코드 (변경이 여러 번이면 마지막 코드까지 따라간 것)
type codeChange struct {
	Old, New   string
	ChangedOn  string // 옛 코드가 삭제된 적용일
	Compatible bool   // 고시 단위가 같음
	OldUnit    string // 고시 단위 표시 (확인용)
	NewUnit    string
}

// payDeleted : 약가기준정보 급여구분 '삭제'
func payDeleted(note string) bool { return strings.TrimSpace(note) == "삭제" }

// recordCodeChanges 는 변경 기록을 남기고(같은 옛 코드면 새로 받은 것으로 바꿈) 옮겨야 할 것을 옮긴다.
// 돌려주는 값: 옮긴 품목 수, 단위가 달라 확인이 필요한 품목(약국 기록이 있는 것만), 재고 기준점이 두 코드에 모두 있던 품목.
func recordCodeChanges(tx *sql.Tx, changes []codeChange) (merged int, review []codeChange, bothAnchors []codeChange, err error) {
	if len(changes) == 0 {
		return 0, nil, nil, nil
	}
	stmt, err := tx.Prepare(`INSERT INTO code_change(old_code, new_code, changed_on, status, note, created_at) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(old_code) DO UPDATE SET new_code = excluded.new_code, changed_on = excluded.changed_on, status = excluded.status, note = excluded.note`)
	if err != nil {
		return 0, nil, nil, err
	}
	defer stmt.Close()
	now := store.Now()
	for _, c := range changes {
		status, note := codeMerged, ""
		if !c.Compatible {
			status, note = codeReview, fmt.Sprintf("고시 단위가 다름: %s → %s", c.OldUnit, c.NewUnit)
		}
		if _, err := stmt.Exec(c.Old, c.New, c.ChangedOn, status, note, now); err != nil {
			return 0, nil, nil, err
		}
	}
	used, err := usedCodeSet(tx)
	if err != nil {
		return 0, nil, nil, err
	}
	for _, c := range changes {
		if !used[c.Old] {
			continue
		}
		if !c.Compatible {
			review = append(review, c)
			continue
		}
		both, err := mergeProductCode(tx, c.Old, c.New, c.ChangedOn)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("제품코드 %s → %s 옮기기 실패: %w", c.Old, c.New, err)
		}
		merged++
		if both {
			bothAnchors = append(bothAnchors, c)
		}
	}
	// 약국 기록은 없어도 바코드 → 옛 코드 연결은 새 코드로 (스캔하면 새 코드로 잡히게)
	if _, err := tx.Exec(`UPDATE gtin_map SET product_code = (SELECT new_code FROM code_change WHERE old_code = gtin_map.product_code AND status = 'merged')
		WHERE product_code IN (SELECT old_code FROM code_change WHERE status = 'merged')`); err != nil {
		return 0, nil, nil, err
	}
	return merged, review, bothAnchors, nil
}

// usedCodeSet : 약국 기록이 있는 제품코드
func usedCodeSet(q querier) (map[string]bool, error) {
	rows, err := q.Query(sqlUsedCodes + " UNION SELECT product_code FROM lot_evidence")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			out[c] = true
		}
	}
	return out, rows.Err()
}

// mergeProductCode 는 옛 코드의 약국 기록을 새 코드로 옮긴다. both = 두 코드 모두에 재고 기준점이 있었음 (재고 확인 필요).
// 단위 재계산은 부르는 쪽이 한다.
func mergeProductCode(tx *sql.Tx, oldCode, newCode, changedOn string) (both bool, err error) {
	if oldCode == newCode {
		return false, nil
	}
	var nOld, nNew int
	_ = tx.QueryRow("SELECT (SELECT COUNT(*) FROM stock_anchor WHERE product_code = ?1), (SELECT COUNT(*) FROM stock_anchor WHERE product_code = ?2)", oldCode, newCode).Scan(&nOld, &nNew)
	both = nOld > 0 && nNew > 0

	// 품목: 새 코드가 없으면 옛 품목을 그대로 옮기고, 있으면 사용자가 정한 값(배율·포장·메모)만 비어 있는 칸에 채운다
	if _, err := tx.Exec(`INSERT INTO product(code, name, company, stock_unit, pack_size, unit_factor, pack_manual, memo, updated_at)
		SELECT ?2, name, company, stock_unit, pack_size, unit_factor, pack_manual, memo, ?3 FROM product WHERE code = ?1
		ON CONFLICT(code) DO UPDATE SET
		  pack_size = COALESCE(product.pack_size, excluded.pack_size),
		  unit_factor = COALESCE(product.unit_factor, excluded.unit_factor),
		  pack_manual = COALESCE(product.pack_manual, excluded.pack_manual),
		  memo = CASE WHEN product.memo = '' THEN excluded.memo ELSE product.memo END,
		  updated_at = excluded.updated_at`, oldCode, newCode, store.Now()); err != nil {
		return false, err
	}

	// 재고 기준점: 같은 날·같은 시점·같은 종류가 새 코드에 이미 있으면 하나로 합친다 (최신 기준점 하나만 쓰므로 둘이면 한쪽이 사라진다)
	type anchor struct {
		id                 int64
		asOf, timing, kind string
		qty                int64
	}
	var olds []anchor
	rows, err := tx.Query("SELECT id, as_of, timing, source, qty FROM stock_anchor WHERE product_code = ?", oldCode)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var a anchor
		if err := rows.Scan(&a.id, &a.asOf, &a.timing, &a.kind, &a.qty); err != nil {
			rows.Close()
			return false, err
		}
		olds = append(olds, a)
	}
	rows.Close()
	for _, a := range olds {
		var target int64
		err := tx.QueryRow("SELECT id FROM stock_anchor WHERE product_code = ? AND as_of = ? AND timing = ? AND source = ? ORDER BY id DESC LIMIT 1",
			newCode, a.asOf, a.timing, a.kind).Scan(&target)
		switch {
		case err == sql.ErrNoRows:
			if _, err := tx.Exec("UPDATE stock_anchor SET product_code = ? WHERE id = ?", newCode, a.id); err != nil {
				return false, err
			}
			continue
		case err != nil:
			return false, err
		}
		if a.kind == "count" { // 실사: 행을 옮기고 수량을 더한다 (행이 있으면 단위 재계산 때 행 합으로 다시 맞춰진다)
			if _, err := tx.Exec("UPDATE count_line SET anchor_id = ? WHERE anchor_id = ?", target, a.id); err != nil {
				return false, err
			}
			if _, err := tx.Exec("UPDATE stock_anchor SET qty = qty + ? WHERE id = ?", a.qty, target); err != nil {
				return false, err
			}
		}
		// 초기재고 기준점은 initial_line 합으로 다시 만들어진다
		if _, err := tx.Exec("DELETE FROM stock_anchor WHERE id = ?", a.id); err != nil {
			return false, err
		}
	}

	for _, q := range []string{
		"UPDATE purchase_line SET product_code = ?2 WHERE product_code = ?1",
		"UPDATE dispense_line SET product_code = ?2 WHERE product_code = ?1",
		"UPDATE initial_line SET product_code = ?2 WHERE product_code = ?1",
		"UPDATE count_line SET product_code = ?2 WHERE product_code = ?1",
		"UPDATE lot_evidence SET product_code = ?2 WHERE product_code = ?1",
		"UPDATE gtin_map SET product_code = ?2 WHERE product_code = ?1",
		"UPDATE initial_unresolved SET resolved_code = ?2 WHERE resolved_code = ?1",
		"DELETE FROM review_ack WHERE product_code = ?1", // 확인 당시 상태가 달라졌다 — 새 코드로 다시 본다
		"DELETE FROM product WHERE code = ?1",
	} {
		if _, err := tx.Exec(q, oldCode, newCode); err != nil {
			return false, err
		}
	}
	note := "약가기준정보 제품코드 변경"
	if changedOn != "" {
		note += " (" + changedOn + ")"
	}
	if both {
		note += " — 두 코드 모두 재고 기준점이 있어 재고 확인 필요"
	}
	return both, logChange(tx, ChangeEntry{Kind: "code_change", ProductCode: newCode, OldValue: oldCode, NewValue: newCode, Note: note, Source: "약제급여목록"})
}

// applyStoredCodeChanges : 이미 옮긴 코드 변경(merged)을 새로 가져온 자료에도 적용한다 (옛 코드로 된 이팜 자료를 다시 가져온 경우).
func (s *Service) applyStoredCodeChanges() {
	type pair struct{ old, new, on string }
	var todo []pair
	rows, err := s.DB.Query(`SELECT old_code, new_code, changed_on FROM code_change WHERE status = 'merged' AND old_code IN (` +
		sqlUsedCodes + ` UNION SELECT product_code FROM lot_evidence UNION SELECT product_code FROM gtin_map)`)
	if err != nil {
		s.Log.Warn("코드 변경 확인 실패", "err", err)
		return
	}
	for rows.Next() {
		var p pair
		if rows.Scan(&p.old, &p.new, &p.on) == nil {
			todo = append(todo, p)
		}
	}
	rows.Close()
	if len(todo) == 0 {
		return
	}
	err = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var codes []string
		for _, p := range todo {
			if _, err := mergeProductCode(tx, p.old, p.new, p.on); err != nil {
				return err
			}
			codes = append(codes, p.new)
		}
		return recomputeUnits(tx, codes)
	})
	if err != nil {
		s.Log.Warn("가져온 자료의 옛 제품코드를 새 코드로 옮기지 못함", "err", err)
		return
	}
	s.Log.Info("가져온 자료의 옛 제품코드를 새 코드로 옮김", "count", len(todo))
}

// resolveCodeChanges : 약가기준정보 품목(제품코드별 지금 적용 중인 행)에서 코드 변경을 찾는다.
// cur = 코드 → 지금 행, after = 코드 → 변경이후코드, beforeOf = 새 코드 → 변경이전코드 (새 코드 행에만 적힌 경우).
// 변경이 이어지면(A→B→C) 지금 급여목록에 있는 마지막 코드까지 따라간다. 끝이 삭제뿐이면 변경이 아니다.
func resolveCodeChanges(cur map[string]dgamtPick, after, beforeOf map[string]string) []codeChange {
	succ := func(code string) string {
		if n := after[code]; n != "" {
			return n
		}
		return beforeOf[code]
	}
	var out []codeChange
	for code, p := range cur {
		if !payDeleted(p.rec.Note) {
			continue
		}
		target, seen := code, map[string]bool{code: true}
		for i := 0; i < 20; i++ {
			n := succ(target)
			if n == "" || seen[n] {
				break
			}
			seen[n] = true
			target = n
			if t, ok := cur[target]; !ok || !payDeleted(t.rec.Note) {
				break
			}
		}
		t, ok := cur[target]
		if target == code || !ok || payDeleted(t.rec.Note) {
			continue // 변경이후코드가 없거나 목록에 없음 / 끝까지 삭제 → 급여 중단
		}
		ou, nu := domain.ParsePriceUnit(p.rec.Spec, p.rec.Unit), domain.ParsePriceUnit(t.rec.Spec, t.rec.Unit)
		out = append(out, codeChange{Old: code, New: target, ChangedOn: p.eff, Compatible: ou.Label == nu.Label, OldUnit: ou.Label, NewUnit: nu.Label})
	}
	return out
}
