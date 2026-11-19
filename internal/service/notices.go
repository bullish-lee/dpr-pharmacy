package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/ingest"
	"dpr/internal/sheet"
	"dpr/internal/store"
)

var unsafeFileChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

func safeFileName(name string) string {
	name = unsafeFileChars.ReplaceAllString(name, "_")
	if r := []rune(name); len(r) > 120 {
		name = string(r[len(r)-120:])
	}
	return name
}

type noticeSummary struct {
	Counts   map[string]int `json:"counts"`
	Dates    []string       `json:"dates"`
	Warnings []string       `json:"warnings"`
	Sheets   any            `json:"sheets"`
}

// ingestNoticeFile 은 목록표 엑셀을 해석해 notice + price_change 로 저장한다 (같은 파일은 중복 저장하지 않음).
const msgDupNotice = "이미 가져온 고시 파일입니다"

// ingestNoticeFile 은 고시 목록표를 해석해 저장한다. dup=true 면 같은 파일을 이미 가져온 것 (새 고시 아님).
func (s *Service) ingestNoticeFile(source, listNo, title, url, publishedAt, fileName string, data []byte) (id int64, msg string, dup bool, err error) {
	sha := shaHex(data)
	var existingID int64
	var existingList sql.NullString
	if err := s.DB.QueryRow("SELECT id, list_no FROM notice WHERE file_sha256 = ?", sha).Scan(&existingID, &existingList); err == nil {
		if listNo != "" {
			s.linkDupNotice(existingID, existingList, listNo, title, url, publishedAt)
		}
		return existingID, msgDupNotice, true, nil
	}
	sheets, err := sheet.ReadBytes(fileName, data)
	if err != nil {
		return 0, "", false, err
	}
	parsed, err := ingest.ParseNotice(sheets, fileName+" "+title)
	if err != nil {
		return 0, "", false, err
	}
	dir := filepath.Join(s.DataDir, "raw", "notices")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, sha[:12]+"_"+safeFileName(fileName))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		s.Log.Warn("고시 원본 저장 실패", "err", err)
	}
	if publishedAt == "" {
		publishedAt = s.Today()
	}
	sum := noticeSummary{Counts: map[string]int{}, Warnings: parsed.Warnings, Sheets: parsed.Sheets}
	dates := map[string]bool{}
	changed := map[string]bool{}
	var noticeID int64
	err = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		now := store.Now()
		if listNo != "" {
			_ = tx.QueryRow("SELECT id FROM notice WHERE list_no = ?", listNo).Scan(&noticeID)
		}
		var lst any
		if listNo != "" {
			lst = listNo
		}
		if noticeID == 0 {
			res, err := tx.Exec(`INSERT INTO notice(source, list_no, notice_no, title, url, published_at, status, file_name, file_sha256, file_path, created_at, updated_at)
				VALUES(?, ?, ?, ?, ?, ?, 'parsed', ?, ?, ?, ?, ?)`, source, lst, parsed.NoticeNo, title, url, publishedAt, fileName, sha, path, now, now)
			if err != nil {
				return err
			}
			noticeID, _ = res.LastInsertId()
		} else {
			if _, err := tx.Exec(`UPDATE notice SET notice_no = ?, title = ?, url = ?, published_at = ?, status = 'parsed', error = '', attempts = 0, next_try = '', file_name = ?, file_sha256 = ?, file_path = ?, updated_at = ? WHERE id = ?`,
				parsed.NoticeNo, title, url, publishedAt, fileName, sha, path, now, noticeID); err != nil {
				return err
			}
		}
		// 다시 해석하는 고시면 이전 품목도 단위 재계산 대상 (새 해석에서 빠진 품목의 고시 가격이 사라진다)
		if prev, err := tx.Query("SELECT DISTINCT product_code FROM price_change WHERE notice_id = ?", noticeID); err == nil {
			for prev.Next() {
				var c string
				if prev.Scan(&c) == nil {
					changed[c] = true
				}
			}
			prev.Close()
		}
		if _, err := tx.Exec("DELETE FROM price_change WHERE notice_id = ?", noticeID); err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO price_change(notice_id, product_code, product_name, company, spec, unit, ingredient_code, ingredient, category, route,
			kind, detail, price_before, price_after, before_source, effective_date, grace_until, flexible, sheet, note)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		pstmt, err := tx.Prepare(`INSERT INTO product(code, name, company, stock_unit, updated_at) VALUES(?, ?, ?, ?, ?)
			ON CONFLICT(code) DO UPDATE SET name = CASE WHEN product.name = '' THEN excluded.name ELSE product.name END,
			  company = CASE WHEN product.company = '' THEN excluded.company ELSE product.company END, updated_at = excluded.updated_at`)
		if err != nil {
			return err
		}
		defer pstmt.Close()
		for _, c := range parsed.Changes {
			beforeSrc := ""
			if c.PriceBefore > 0 {
				beforeSrc = "notice"
			}
			if c.Kind == "change" && c.PriceBefore == 0 {
				if p, src := historyPrice(tx, c.Code, c.EffectiveDate); p > 0 {
					c.PriceBefore, beforeSrc = p, src
					// 사입 단가는 할인된 값이라 상한금액과 견줄 수 없다 — 인하인데 '인상'으로 보여 순위에서 빠질 수 있다 → 미확인으로 둔다
					if c.Detail == "unknown" && c.PriceAfter > 0 && src != "purchase" {
						switch {
						case c.PriceAfter < p:
							c.Detail = "price_down"
						case c.PriceAfter > p:
							c.Detail = "price_up"
						default:
							c.Detail = "same"
						}
					}
				}
			}
			if _, err := stmt.Exec(noticeID, c.Code, c.Name, c.Company, c.Spec, c.Unit, c.IngredientCode, c.Ingredient, c.Category, c.Route,
				c.Kind, c.Detail, c.PriceBefore, c.PriceAfter, beforeSrc, c.EffectiveDate, c.GraceUntil, b2i(c.Flexible), c.Sheet, c.Note); err != nil {
				return err
			}
			if _, err := pstmt.Exec(c.Code, c.Name, c.Company, "", now); err != nil {
				return err
			}
			sum.Counts[c.Detail]++
			if c.EffectiveDate != "" {
				dates[c.EffectiveDate] = true
			}
			changed[c.Code] = true
		}
		sum.Dates = sortedKeys(dates)
		if err := recomputeUnits(tx, sortedKeys(changed)); err != nil { // 고시 단위·가격으로 사입 환산 갱신
			return err
		}
		_, err = tx.Exec("UPDATE notice SET summary = ? WHERE id = ?", toJSON(sum), noticeID)
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") { // 다른 프로세스가 같은 파일을 방금 저장함
			if s.DB.QueryRow("SELECT id FROM notice WHERE file_sha256 = ?", sha).Scan(&existingID) == nil {
				return existingID, msgDupNotice, true, nil
			}
		}
		return 0, "", false, err
	}
	s.rematchUnresolved() // 고시 가격으로 단가가 바뀌면 이름 매칭 결과도 달라질 수 있다
	msg = fmt.Sprintf("고시 %s: 인하 %d · 인상 %d · 신설 %d · 삭제 %d (시행일 %s)", orDash(parsed.NoticeNo),
		sum.Counts["price_down"], sum.Counts["price_up"], sum.Counts["new"], sum.Counts["delete"], strings.Join(sum.Dates, ", "))
	return noticeID, msg, false, nil
}

// linkDupNotice : 수집한 첨부가 이미 가져온 파일과 같을 때 게시글을 정리한다 (같은 게시글을 다음 수집 때 또 받지 않게).
//   - 직접 올린 고시(게시글 번호 없음)면: 감지만 해 둔 자리 표시 행을 먼저 지우고 올린 고시에 게시글 번호·게시일을 단다.
//   - 이미 다른 게시글로 수집된 파일이면: 이 게시글의 자리 표시 행을 'duplicate' 로 표시 (수집 대상에서 제외, 목록에는 안 보임).
func (s *Service) linkDupNotice(existingID int64, existingList sql.NullString, listNo, title, url, publishedAt string) {
	err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if existingList.Valid && existingList.String == listNo {
			return nil
		}
		if !existingList.Valid {
			if _, err := tx.Exec("DELETE FROM notice WHERE list_no = ? AND id <> ? AND file_sha256 = ''", listNo, existingID); err != nil {
				return err
			}
			_, err := tx.Exec("UPDATE notice SET list_no = ?, title = CASE WHEN ? <> '' THEN ? ELSE title END, url = ?, published_at = CASE WHEN ? <> '' THEN ? ELSE published_at END, source = 'mohw', status = 'parsed', updated_at = ? WHERE id = ?",
				listNo, title, title, url, publishedAt, publishedAt, store.Now(), existingID)
			return err
		}
		_, err := tx.Exec("UPDATE notice SET status = 'duplicate', error = ?, next_try = '', updated_at = ? WHERE list_no = ? AND file_sha256 = ''",
			"같은 목록표가 다른 게시글("+existingList.String+")로 이미 수집됨", store.Now(), listNo)
		return err
	})
	if err != nil {
		s.Log.Warn("고시 게시글 연결 실패", "list_no", listNo, "err", err)
	}
}

func orDash(s string) string {
	if s == "" {
		return "(번호 미상)"
	}
	return "제" + s + "호"
}

// historyPrice : 시행일 이전의 마지막 상한금액 — 과거 고시 → 시행일 전 약제급여목록 → 최근 사입 단가(참고용, 인상·인하 판단에 쓰지 않음)
func historyPrice(tx *sql.Tx, code, eff string) (float64, string) {
	var p sql.NullFloat64
	q := `SELECT price_after FROM price_change WHERE product_code = ? AND kind IN ('new','change') AND price_after > 0 AND effective_date <> ''`
	args := []any{code}
	if eff != "" {
		q += " AND effective_date < ?"
		args = append(args, eff)
	}
	q += " ORDER BY effective_date DESC, id DESC LIMIT 1"
	if tx.QueryRow(q, args...).Scan(&p) == nil && p.Valid && p.Float64 > 0 {
		return p.Float64, "history"
	}
	if eff != "" { // 시행일 전 목록의 상한금액 (시행일 이후 목록이면 이미 바뀐 가격일 수 있어 쓰지 않는다)
		if tx.QueryRow("SELECT price FROM drug_master WHERE code = ? AND in_list = 1 AND price > 0 AND list_date <> '' AND list_date < ?", code, eff).Scan(&p) == nil && p.Valid && p.Float64 > 0 {
			return p.Float64, "master"
		}
	}
	q = "SELECT unit_price FROM purchase_line WHERE product_code = ? AND kind = 'purchase' AND unit_price > 0"
	args = []any{code}
	if eff != "" {
		q += " AND date < ?"
		args = append(args, eff)
	}
	q += " ORDER BY date DESC, id DESC LIMIT 1"
	if tx.QueryRow(q, args...).Scan(&p) == nil && p.Valid && p.Float64 > 0 {
		return p.Float64, "purchase"
	}
	return 0, ""
}

// ── 조회 ──────────────────────────────────────────────────────────

type NoticeView struct {
	ID          int64          `json:"id"`
	Source      string         `json:"source"`
	ListNo      string         `json:"listNo"`
	NoticeNo    string         `json:"noticeNo"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	PublishedAt string         `json:"publishedAt"`
	Status      string         `json:"status"`
	Error       string         `json:"error"`
	FileName    string         `json:"fileName"`
	CreatedAt   string         `json:"createdAt"`
	Counts      map[string]int `json:"counts"`
	Dates       []string       `json:"dates"`
	Warnings    []string       `json:"warnings"`
	HeldDown    int            `json:"heldDown"`
	HeldDelete  int            `json:"heldDelete"`
	HeldChange  int            `json:"heldChange"`
}

func (s *Service) ListNotices() ([]NoticeView, error) {
	rows, err := s.DB.Query(`SELECT id, source, IFNULL(list_no,''), notice_no, title, url, published_at, status, error, file_name, created_at, summary
		FROM notice WHERE status <> 'duplicate' ORDER BY published_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	var out []NoticeView
	for rows.Next() {
		var v NoticeView
		var sum string
		if err := rows.Scan(&v.ID, &v.Source, &v.ListNo, &v.NoticeNo, &v.Title, &v.URL, &v.PublishedAt, &v.Status, &v.Error, &v.FileName, &v.CreatedAt, &sum); err != nil {
			rows.Close()
			return nil, err
		}
		var ns noticeSummary
		_ = json.Unmarshal([]byte(sum), &ns)
		v.Counts, v.Dates, v.Warnings = ns.Counts, ns.Dates, ns.Warnings
		if v.Counts == nil {
			v.Counts = map[string]int{}
		}
		out = append(out, v)
	}
	rows.Close()
	stock, err := s.stockMap(s.Today(), true)
	if err != nil {
		return out, nil
	}
	for i := range out {
		r, err := s.DB.Query("SELECT product_code, kind, detail FROM price_change WHERE notice_id = ?", out[i].ID)
		if err != nil {
			continue
		}
		for r.Next() {
			var code, kind, detail string
			if r.Scan(&code, &kind, &detail) == nil && stock[code].Qty > 0 {
				switch {
				case detail == "price_down":
					out[i].HeldDown++
					out[i].HeldChange++
				case kind == "delete":
					out[i].HeldDelete++
				case kind == "change":
					out[i].HeldChange++
				}
			}
		}
		r.Close()
	}
	return out, nil
}

type ChangeRow struct {
	ID            int64      `json:"id"`
	NoticeID      int64      `json:"noticeId"`
	Code          string     `json:"code"`
	Name          string     `json:"name"`
	EfarmName     string     `json:"efarmName"`
	Company       string     `json:"company"`
	Spec          string     `json:"spec"`
	Unit          string     `json:"unit"`
	Kind          string     `json:"kind"`
	Detail        string     `json:"detail"`
	PriceBefore   float64    `json:"priceBefore"`
	PriceAfter    float64    `json:"priceAfter"`
	Rate          float64    `json:"rate"`
	BeforeSource  string     `json:"beforeSource"`
	EffectiveDate string     `json:"effectiveDate"`
	GraceUntil    string     `json:"graceUntil"`
	Flexible      bool       `json:"flexible"`
	Category      string     `json:"category"`
	Ingredient    string     `json:"ingredient"`
	Sheet         string     `json:"sheet"`
	Note          string     `json:"note"`
	Stock         domain.Qty `json:"stock"`
	Held          bool       `json:"held"`
}

// NoticeChanges : group = all | down | change | new | delete, heldOnly = 보유 품목만
func (s *Service) NoticeChanges(noticeID int64, group string, heldOnly bool) ([]ChangeRow, error) {
	q := `SELECT c.id, c.notice_id, c.product_code, c.product_name, IFNULL(p.name,''), c.company, c.spec, c.unit, c.kind, c.detail,
		IFNULL(c.price_before,0), IFNULL(c.price_after,0), c.before_source, c.effective_date, c.grace_until, c.flexible, c.category, c.ingredient, c.sheet, c.note
		FROM price_change c LEFT JOIN product p ON p.code = c.product_code WHERE c.notice_id = ?`
	switch group {
	case "down":
		q += " AND c.detail = 'price_down'"
	case "change":
		q += " AND c.kind = 'change'"
	case "new":
		q += " AND c.kind = 'new'"
	case "delete":
		q += " AND c.kind = 'delete'"
	}
	stock, _ := s.stockMap(s.Today(), true)
	rows, err := s.DB.Query(q, noticeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeRow
	for rows.Next() {
		var c ChangeRow
		var flex int
		if err := rows.Scan(&c.ID, &c.NoticeID, &c.Code, &c.Name, &c.EfarmName, &c.Company, &c.Spec, &c.Unit, &c.Kind, &c.Detail,
			&c.PriceBefore, &c.PriceAfter, &c.BeforeSource, &c.EffectiveDate, &c.GraceUntil, &flex, &c.Category, &c.Ingredient, &c.Sheet, &c.Note); err != nil {
			return nil, err
		}
		c.Flexible = flex == 1
		if c.PriceBefore > 0 && c.PriceAfter > 0 {
			c.Rate = (c.PriceBefore - c.PriceAfter) / c.PriceBefore
		}
		c.Stock = stock[c.Code].Qty
		c.Held = c.Stock > 0
		if heldOnly && !c.Held {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Held != out[j].Held {
			return out[i].Held
		}
		return out[i].Rate > out[j].Rate
	})
	return out, rows.Err()
}

// DeleteNotice 는 고시를 지우고, 그 고시 가격으로 환산했던 품목의 단위를 다시 계산한다. 수정 기록에 남긴다.
func (s *Service) DeleteNotice(id int64) error {
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var no, title, file string
		if err := tx.QueryRow("SELECT notice_no, title, file_name FROM notice WHERE id = ?", id).Scan(&no, &title, &file); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		codes := []string{} // nil 이면 전체 재계산이 된다
		rows, err := tx.Query("SELECT DISTINCT product_code FROM price_change WHERE notice_id = ?", id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c string
			if rows.Scan(&c) == nil {
				codes = append(codes, c)
			}
		}
		rows.Close()
		if _, err := tx.Exec("DELETE FROM notice WHERE id = ?", id); err != nil {
			return err
		}
		if err := recomputeUnits(tx, codes); err != nil {
			return err
		}
		what := strings.TrimSpace(orDash(no) + " " + file)
		if title != "" {
			what = strings.TrimSpace(orDash(no) + " " + title)
		}
		return logChange(tx, ChangeEntry{Kind: "notice_delete", OldValue: what, NewValue: "삭제", Note: fmt.Sprintf("품목 %d개 단위 다시 계산", len(codes)), Source: "고시"})
	})
}
