package service

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"dpr/internal/crawler"
	"dpr/internal/domain"
	"dpr/internal/ingest"
	"dpr/internal/store"
)

// 지난 가격 이력 — 약가기준정보(공공데이터포털)에는 제품코드마다 적용시작일별 상한가가 모두 들어 있다.
// 약제급여목록을 받을 때 함께 온 이 이력에서 지난 1년의 가격 변경(인하·인상)을 뽑아 '지난 고시'처럼 넣어 둔다.
// 추가 요청은 없다 (매달 받는 목록에 이미 들어 있는 자료).
//
// 쓰임: 프로그램을 쓰기 전에 있던 인하 회차 보기, 고시에 '변경 전 가격'이 없을 때 채우기,
// 사입 당시 상한가로 단위 환산 판단하기, 약품 조회의 가격 변화.
//
// 실제 고시 파일(자동 수집·직접 올림·게시글로 가져옴)이 같은 품목·시행일을 다루면 그쪽이 우선한다:
//   - 넣을 때: 실제 고시에 가격 항목이 있으면 이력 행을 만들지 않는다.
//   - 나중에 실제 고시가 들어오면: 이력 고시는 게시일이 비어 있어 latestOf 에서 항상 진다.
const (
	historySource  = "history"
	historyMonths  = 12
	setHistorySHA  = "price_history_sha"
	historyListPre = "history:" // notice.list_no = "history:2026-06-01"
)

// histChange : 이력에서 찾은 가격 변경 한 건
type histChange struct {
	Code, Eff     string
	Before, After float64
	Rec           ingest.MasterRec
}

// priceHistoryFromDgamt : 제품코드별로 적용시작일 순으로 늘어놓고, 앞뒤 상한가가 다른 곳을 가격 변경으로 본다.
// from ≤ 시행일 ≤ today 인 것만. '삭제' 행과 가격이 없는 행은 건너뛴다 (삭제 뒤 다시 등재된 경우는 변경이 아니다).
func priceHistoryFromDgamt(items []crawler.DgamtItem, from, today string) []histChange {
	type row struct {
		eff     string
		price   float64
		deleted bool
		rec     ingest.MasterRec
		seq     int
	}
	by := map[string][]row{}
	for i, it := range items {
		code := domain.NormalizeProductCode(it.MdsCd)
		if !domain.IsEDICode(code) {
			continue
		}
		eff, ok := domain.ParseFlexibleDate(it.AdtStaDd)
		if !ok || eff == "" {
			continue
		}
		price, _ := ingest.ParseNumber(it.MxCprc)
		by[code] = append(by[code], row{eff: eff, price: price, deleted: payDeleted(it.PayTpNm), seq: i,
			rec: ingest.MasterRec{Code: code, Name: strings.TrimSpace(it.ItmNm), Company: strings.TrimSpace(it.MnfEntpNm),
				Spec: strings.TrimSpace(it.NomNm), Unit: strings.TrimSpace(it.Unit), IngredientCode: strings.TrimSpace(it.GnlNmCd), Route: strings.TrimSpace(it.InjcPthNm)}})
	}
	var out []histChange
	for code, rows := range by {
		sort.SliceStable(rows, func(a, b int) bool {
			if rows[a].eff != rows[b].eff {
				return rows[a].eff < rows[b].eff
			}
			return rows[a].seq < rows[b].seq
		})
		var prev *row
		for i := range rows {
			r := &rows[i]
			if i+1 < len(rows) && rows[i+1].eff == r.eff {
				continue // 같은 날짜가 여러 번이면 마지막 것만
			}
			if r.deleted || r.price <= 0 {
				prev = nil
				continue
			}
			if prev != nil && prev.price != r.price && r.eff >= from && r.eff <= today {
				out = append(out, histChange{Code: code, Eff: r.eff, Before: prev.price, After: r.price, Rec: r.rec})
			}
			prev = r
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Eff != out[b].Eff {
			return out[a].Eff < out[b].Eff
		}
		return out[a].Code < out[b].Code
	})
	return out
}

// storePriceHistory : 지난 1년 가격 변경을 '가격 이력' 고시(시행일마다 하나)로 바꿔 넣는다. 내용이 그대로면 아무것도 하지 않는다.
// 돌려주는 값: 넣은 변경 수, 그중 인하 수, 바뀌었는가.
func (s *Service) storePriceHistory(items []crawler.DgamtItem, now time.Time) (total, downs int, changed bool, err error) {
	today := now.Format("2006-01-02")
	from := now.AddDate(0, -historyMonths, 0).Format("2006-01-02")
	changes := priceHistoryFromDgamt(items, from, today)
	var b strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&b, "%s|%s|%g|%g\n", c.Eff, c.Code, c.Before, c.After)
	}
	sha := shaHex([]byte(b.String()))
	if s.DB.GetSetting(setHistorySHA, "") == sha {
		return len(changes), 0, false, nil
	}
	err = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM notice WHERE source = ?", historySource); err != nil { // price_change 는 함께 지워진다 (ON DELETE CASCADE)
			return err
		}
		// 실제 고시가 가격을 말하는 (품목, 시행일)은 건너뛴다
		real := map[string]bool{}
		dIn, dArgs := inClause(priceDetails)
		rows, err := tx.Query(`SELECT product_code, effective_date FROM price_change WHERE effective_date >= ? AND detail IN (`+dIn+`)`, append([]any{from}, dArgs...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c, e string
			if err := rows.Scan(&c, &e); err != nil {
				rows.Close()
				return err
			}
			real[c+"|"+e] = true
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		stmt, err := tx.Prepare(`INSERT INTO price_change(notice_id, product_code, product_name, company, spec, unit, ingredient_code, ingredient, category, route,
			kind, detail, price_before, price_after, before_source, effective_date, grace_until, flexible, sheet, note)
			VALUES(?, ?, ?, ?, ?, ?, ?, '', '', ?, 'change', ?, ?, ?, 'history', ?, '', 0, '가격 이력', '')`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		ts := store.Now()
		noticeOf := map[string]int64{}
		counts := map[string]map[string]int{}
		for _, c := range changes {
			if real[c.Code+"|"+c.Eff] {
				continue
			}
			id, ok := noticeOf[c.Eff]
			if !ok {
				res, err := tx.Exec(`INSERT INTO notice(source, list_no, notice_no, title, url, published_at, status, created_at, updated_at)
					VALUES(?, ?, '', ?, '', '', 'parsed', ?, ?)`, historySource, historyListPre+c.Eff,
					"가격 이력 — "+strings.ReplaceAll(c.Eff, "-", ".")+" 시행 (약가기준정보)", ts, ts)
				if err != nil {
					return err
				}
				id, _ = res.LastInsertId()
				noticeOf[c.Eff] = id
				counts[c.Eff] = map[string]int{}
			}
			detail := "price_up"
			if c.After < c.Before {
				detail = "price_down"
				downs++
			}
			if _, err := stmt.Exec(id, c.Code, c.Rec.Name, c.Rec.Company, c.Rec.Spec, c.Rec.Unit, c.Rec.IngredientCode, c.Rec.Route, detail, c.Before, c.After, c.Eff); err != nil {
				return err
			}
			counts[c.Eff][detail]++
			total++
		}
		for eff, id := range noticeOf {
			if _, err := tx.Exec("UPDATE notice SET summary = ? WHERE id = ?", toJSON(noticeSummary{Counts: counts[eff], Dates: []string{eff}}), id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("INSERT INTO app_setting(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", setHistorySHA, sha); err != nil {
			return err
		}
		return recomputeUnits(tx, nil) // 사입 당시 상한가(가격 이력)가 달라지면 단위 환산 판단이 달라질 수 있다
	})
	if err != nil {
		return 0, 0, false, err
	}
	s.Log.Info("가격 이력 반영", "changes", total, "downs", downs, "from", from)
	return total, downs, true, nil
}
