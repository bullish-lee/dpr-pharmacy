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

// 지난 고시 이력 — 약가기준정보(공공데이터포털)에는 제품코드마다 적용시작일별 행(상한가·급여구분·품목명)이 모두 들어 있다.
// 약제급여목록을 받을 때 함께 온 이 이력에서 지난 1년의 **인하·인상·신설·삭제·제품명 변경**을 뽑아 '지난 고시'처럼 넣어 둔다.
// 추가 요청은 없다 (매달 받는 목록에 이미 들어 있는 자료). 복지부 게시판을 거슬러 받지 않는다 (robots.txt 가 /board 를 막는다).
//
// 쓰임: 프로그램을 쓰기 전에 있던 인하 회차·신설·삭제 보기, 고시에 '변경 전 가격'이 없을 때 채우기,
// 사입 당시 상한가로 단위 환산 판단하기, 약품 조회의 가격 변화.
//
// 실제 고시 파일(자동 수집·직접 올림·게시글로 가져옴)이 같은 품목·시행일·종류를 다루면 그쪽이 우선한다:
// 이력 행을 지운다 (넣을 때, 그리고 실제 고시가 들어올 때마다 — pruneHistoryOverlaps).
// 이력에는 고시 번호·급여 유지 유예일·약가유연계약 표시가 없다.
const (
	historySource  = "history"
	historyMonths  = 12
	setHistorySHA  = "price_history_sha"
	historyListPre = "history:" // notice.list_no = "history:2026-06-01"
)

// histChange : 이력에서 찾은 변경 한 건
type histChange struct {
	Code, Eff     string
	Kind, Detail  string // new/new · delete/delete · change/price_down|price_up|name_change
	Before, After float64
	Rec           ingest.MasterRec
}

// histNameKey : 품목명 비교용 (띄어쓰기 차이는 변경으로 보지 않는다)
func histNameKey(s string) string { return strings.Join(strings.Fields(s), "") }

// priceHistoryFromDgamt : 제품코드별로 적용시작일 순으로 늘어놓고 앞뒤 행을 견준다. from ≤ 시행일 ≤ today 인 것만.
//   - 첫 행(그 코드의 가장 이른 적용일)             → 신설
//   - '삭제' 행 (앞 행은 급여 중)                    → 삭제
//   - 앞뒤 상한가가 다름                             → 인하·인상
//   - 상한가는 같고 품목명이 다름                    → 제품명 변경
//   - 삭제 뒤 다시 등재된 행                         → 신설
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
		var prev *row // 바로 앞 행 (급여 중일 때만)
		for i := range rows {
			r := &rows[i]
			if i+1 < len(rows) && rows[i+1].eff == r.eff {
				continue // 같은 날짜가 여러 번이면 마지막 것만
			}
			in := r.eff >= from && r.eff <= today
			switch {
			case r.deleted:
				if prev != nil && in {
					out = append(out, histChange{Code: code, Eff: r.eff, Kind: "delete", Detail: "delete", Before: prev.price, Rec: r.rec})
				}
				prev = nil
				continue
			case prev == nil:
				if in {
					out = append(out, histChange{Code: code, Eff: r.eff, Kind: "new", Detail: "new", After: r.price, Rec: r.rec})
				}
			case r.price > 0 && prev.price > 0 && prev.price != r.price:
				if in {
					d := "price_up"
					if r.price < prev.price {
						d = "price_down"
					}
					out = append(out, histChange{Code: code, Eff: r.eff, Kind: "change", Detail: d, Before: prev.price, After: r.price, Rec: r.rec})
				}
			case histNameKey(prev.rec.Name) != histNameKey(r.rec.Name) && r.rec.Name != "":
				if in {
					out = append(out, histChange{Code: code, Eff: r.eff, Kind: "change", Detail: "name_change", Before: prev.price, After: r.price, Rec: r.rec})
				}
			}
			prev = r
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Eff != out[b].Eff {
			return out[a].Eff < out[b].Eff
		}
		if out[a].Code != out[b].Code {
			return out[a].Code < out[b].Code
		}
		return out[a].Kind < out[b].Kind
	})
	return out
}

// pruneHistoryOverlaps : 실제 고시가 같은 품목·시행일·종류(신설/변경/삭제)를 다루면 이력 행을 지우고, 빈 이력 고시도 지운다.
// 이력을 넣을 때와 실제 고시를 가져올 때마다 부른다 — 두 번 세지 않게.
func pruneHistoryOverlaps(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM price_change WHERE notice_id IN (SELECT id FROM notice WHERE source = ?1)
		AND EXISTS (SELECT 1 FROM price_change r JOIN notice n ON n.id = r.notice_id
			WHERE n.source <> ?1 AND r.product_code = price_change.product_code AND r.effective_date = price_change.effective_date AND r.kind = price_change.kind)`, historySource); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM notice WHERE source = ? AND NOT EXISTS (SELECT 1 FROM price_change c WHERE c.notice_id = notice.id)`, historySource); err != nil {
		return err
	}
	// 고시 목록에 보이는 건수
	rows, err := tx.Query(`SELECT n.id, c.detail, COUNT(*), MIN(c.effective_date) FROM notice n JOIN price_change c ON c.notice_id = n.id
		WHERE n.source = ? GROUP BY n.id, c.detail`, historySource)
	if err != nil {
		return err
	}
	counts := map[int64]map[string]int{}
	dates := map[int64]string{}
	for rows.Next() {
		var id int64
		var detail, eff string
		var n int
		if err := rows.Scan(&id, &detail, &n, &eff); err != nil {
			rows.Close()
			return err
		}
		if counts[id] == nil {
			counts[id] = map[string]int{}
		}
		counts[id][detail] = n
		dates[id] = eff
	}
	if err := closeRows(rows); err != nil {
		return err
	}
	for id, c := range counts {
		if _, err := tx.Exec("UPDATE notice SET summary = ? WHERE id = ?", toJSON(noticeSummary{Counts: c, Dates: []string{dates[id]}}), id); err != nil {
			return err
		}
	}
	return nil
}

// storePriceHistory : 지난 1년 변경을 '지난 이력' 고시(시행일마다 하나)로 바꿔 넣는다. 내용이 그대로면 아무것도 하지 않는다.
// 돌려주는 값: 이력에서 찾은 변경 수, 그중 인하 수, 바뀌었는가.
func (s *Service) storePriceHistory(items []crawler.DgamtItem, now time.Time) (total, downs int, changed bool, err error) {
	today := now.Format("2006-01-02")
	from := now.AddDate(0, -historyMonths, 0).Format("2006-01-02")
	changes := priceHistoryFromDgamt(items, from, today)
	var b strings.Builder
	for _, c := range changes {
		fmt.Fprintf(&b, "%s|%s|%s|%g|%g|%s\n", c.Eff, c.Code, c.Detail, c.Before, c.After, histNameKey(c.Rec.Name))
		if c.Detail == "price_down" {
			downs++
		}
	}
	total = len(changes)
	sha := shaHex([]byte(b.String()))
	if s.DB.GetSetting(setHistorySHA, "") == sha {
		return total, downs, false, nil
	}
	err = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM notice WHERE source = ?", historySource); err != nil { // price_change 는 함께 지워진다 (ON DELETE CASCADE)
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO price_change(notice_id, product_code, product_name, company, spec, unit, ingredient_code, ingredient, category, route,
			kind, detail, price_before, price_after, before_source, effective_date, grace_until, flexible, sheet, note)
			VALUES(?, ?, ?, ?, ?, ?, ?, '', '', ?, ?, ?, ?, ?, 'history', ?, '', 0, '지난 이력', '')`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		ts := store.Now()
		noticeOf := map[string]int64{}
		for _, c := range changes {
			id, ok := noticeOf[c.Eff]
			if !ok {
				res, err := tx.Exec(`INSERT INTO notice(source, list_no, notice_no, title, url, published_at, status, created_at, updated_at)
					VALUES(?, ?, '', ?, '', '', 'parsed', ?, ?)`, historySource, historyListPre+c.Eff,
					"지난 이력 — "+strings.ReplaceAll(c.Eff, "-", ".")+" 시행 (약가기준정보에서 뽑음)", ts, ts)
				if err != nil {
					return err
				}
				id, _ = res.LastInsertId()
				noticeOf[c.Eff] = id
			}
			if _, err := stmt.Exec(id, c.Code, c.Rec.Name, c.Rec.Company, c.Rec.Spec, c.Rec.Unit, c.Rec.IngredientCode, c.Rec.Route,
				c.Kind, c.Detail, c.Before, c.After, c.Eff); err != nil {
				return err
			}
		}
		if err := pruneHistoryOverlaps(tx); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO app_setting(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", setHistorySHA, sha); err != nil {
			return err
		}
		return recomputeUnits(tx, nil) // 사입 당시 상한가(이력)가 달라지면 단위 환산 판단이 달라질 수 있다
	})
	if err != nil {
		return 0, 0, false, err
	}
	s.Log.Info("지난 이력 반영", "changes", total, "downs", downs, "from", from)
	return total, downs, true, nil
}
