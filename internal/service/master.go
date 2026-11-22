package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"dpr/internal/crawler"
	"dpr/internal/domain"
	"dpr/internal/ingest"
	"dpr/internal/store"
)

// importDrugMaster : 심평원 약제급여목록표(전체, 월별)를 통째로 교체하고 단위 환산을 다시 계산한다.
func (s *Service) importDrugMaster(name, sha string, tables []*ingest.Table) (*ImportResult, error) {
	var recs []ingest.MasterRec
	res := &ImportResult{}
	var names []string
	for _, t := range tables {
		r, iss := ingest.ParseDrugMaster(t)
		recs = append(recs, r...)
		res.Issues = append(res.Issues, iss...)
		names = append(names, t.Sheet)
	}
	if len(recs) == 0 {
		return nil, errors.New("가져올 급여목록 행이 없습니다 (제품코드·규격·단위·상한금액 열 필요)")
	}
	listDate := ingest.ListDate(append(names, name)...)
	if listDate == "" {
		listDate = s.Today()
	}
	batchID, n, carried, err := s.storeDrugMaster(name, sha, listDate, recs, len(res.Issues))
	if err != nil {
		return nil, err
	}
	res.BatchID = batchID
	codes := make(map[string]bool, n)
	for _, r := range recs {
		codes[r.Code] = true
	}
	res.Rows, res.Products = len(recs), len(codes)
	res.RangeFrom, res.RangeTo = listDate, listDate
	res.Message = fmt.Sprintf("약제급여목록 %s품목을 가져왔습니다 (적용일 %s). 사입 단위 환산을 다시 계산했습니다.", comma(len(codes)), listDate) + carriedNote(carried)
	return res, nil
}

// sqlCurrentMaster : '지금 급여목록' 행. 그 뒤 목록에서 빠졌지만 약국 기록이 있어 남겨 둔 행(in_list = 0)은 뺀다.
const sqlCurrentMaster = "in_list = 1"

// sqlUsedCodes : 약국 기록(사입·조제·초기재고·실사·재고 기준)이 있는 제품코드
const sqlUsedCodes = `SELECT product_code FROM purchase_line UNION SELECT product_code FROM dispense_line
	UNION SELECT product_code FROM initial_line UNION SELECT product_code FROM count_line UNION SELECT product_code FROM stock_anchor`

func carriedNote(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" 새 목록에 없는 %s품목은 약국 기록이 있어 이전 규격·단위를 그대로 씁니다.", comma(n))
}

// storeDrugMaster : 급여목록 전체를 바꾸고(파일·자동 받기 공통) 단위 환산을 다시 계산한다. 이름 매칭도 다시 한다.
// 새 목록에 없는 품목이라도 약국 기록이 있으면 지우지 않는다 — 빠지면 단위 환산 기준이 사라져 재고 수량이 바뀐다
// (예: 박스로 산 약이 30정 → 1정). 남긴 행은 in_list = 0 (옛 적용일 그대로) — '지금 급여목록'에서는 빠진다. carried = 남긴 수.
func (s *Service) storeDrugMaster(name, sha, listDate string, recs []ingest.MasterRec, issues int) (batchID int64, n, carried int, err error) {
	batchID, n, carried, _, err = s.storeDrugMasterWith(name, sha, listDate, recs, issues, nil)
	return
}

// codeChangeResult : 급여목록을 바꾸며 처리한 코드 변경
type codeChangeResult struct {
	Merged      int          // 약국 기록을 새 코드로 옮긴 품목
	Review      []codeChange // 고시 단위가 달라 옮기지 않은 품목 (약국 기록 있음)
	BothAnchors []codeChange // 두 코드 모두 재고 기준점이 있던 품목 (재고 확인 필요)
}

// storeDrugMasterWith : storeDrugMaster + 코드 변경(옛 코드의 약국 기록을 새 코드로 옮김)을 한 트랜잭션에서.
// 옮기기를 먼저 해야 옛 코드가 '약국 기록 있는 빠진 품목'으로 남지 않는다.
func (s *Service) storeDrugMasterWith(name, sha, listDate string, recs []ingest.MasterRec, issues int, changes []codeChange) (batchID int64, n, carried int, cr codeChangeResult, err error) {
	codes := map[string]bool{}
	err = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var err error
		batchID, err = insertBatch(tx, "drug_master", name, sha, listDate, listDate, len(recs), map[string]any{"issues": issues, "code_changes": len(changes)})
		if err != nil {
			return err
		}
		if cr.Merged, cr.Review, cr.BothAnchors, err = recordCodeChanges(tx, changes); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM drug_master WHERE code NOT IN (" + sqlUsedCodes + ")"); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE drug_master SET in_list = 0"); err != nil { // 남은 행 = 약국 품목. 새 목록에 있으면 아래에서 1
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO drug_master(code, name, company, spec, unit, price, route, ingredient_code, otc, note, list_date, batch_id, in_list)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
			ON CONFLICT(code) DO UPDATE SET name = excluded.name, company = excluded.company, spec = excluded.spec, unit = excluded.unit,
			  price = excluded.price, route = excluded.route, ingredient_code = excluded.ingredient_code, otc = excluded.otc, note = excluded.note,
			  list_date = excluded.list_date, batch_id = excluded.batch_id, in_list = 1`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, r := range recs {
			var price any
			if r.Price > 0 {
				price = r.Price
			}
			if _, err := stmt.Exec(r.Code, r.Name, r.Company, r.Spec, r.Unit, price, r.Route, r.IngredientCode, r.OTC, r.Note, listDate, batchID); err != nil {
				return err
			}
			codes[r.Code] = true
		}
		// 사입·조제 기록이 없는 품목(초기재고·고시에서만 온 품목)은 급여목록 이름으로 바로잡는다
		if _, err := tx.Exec(`UPDATE product SET name = (SELECT m.name FROM drug_master m WHERE m.code = product.code)
			WHERE code IN (SELECT code FROM drug_master)
			  AND code NOT IN (SELECT DISTINCT product_code FROM purchase_line)
			  AND code NOT IN (SELECT DISTINCT product_code FROM dispense_line)`); err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT COUNT(*) FROM drug_master WHERE in_list = 0").Scan(&carried); err != nil {
			return err
		}
		return recomputeUnits(tx, nil)
	})
	if err != nil {
		return 0, 0, 0, codeChangeResult{}, err
	}
	if carried > 0 {
		s.Log.Info("급여목록에서 빠졌지만 약국 기록이 있어 남긴 품목", "count", carried)
	}
	if cr.Merged > 0 || len(cr.Review) > 0 {
		s.Log.Info("제품코드 변경", "merged", cr.Merged, "review", len(cr.Review), "both_anchors", len(cr.BothAnchors))
	}
	s.rematchUnresolved()
	return batchID, len(codes), carried, cr, nil
}

// codeChangeNote : 코드 변경 결과 안내 (없으면 "")
func codeChangeNote(cr codeChangeResult) string {
	var b strings.Builder
	if cr.Merged > 0 {
		fmt.Fprintf(&b, " 제품코드가 바뀐 %s품목은 약국 기록을 새 코드로 옮겼습니다.", comma(cr.Merged))
	}
	if len(cr.BothAnchors) > 0 {
		fmt.Fprintf(&b, " 그중 %s품목은 옛 코드와 새 코드 모두 재고 기준점이 있어 재고를 확인해 주세요 (%s).", comma(len(cr.BothAnchors)), codePairs(cr.BothAnchors))
	}
	if len(cr.Review) > 0 {
		fmt.Fprintf(&b, " 제품코드가 바뀌었지만 고시 단위가 달라 옮기지 않은 품목 %s개가 있습니다 — 직접 확인해 주세요 (%s).", comma(len(cr.Review)), codePairs(cr.Review))
	}
	return b.String()
}

func codePairs(cs []codeChange) string {
	var parts []string
	for i, c := range cs {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("외 %d개", len(cs)-5))
			break
		}
		p := c.Old + "→" + c.New
		if c.OldUnit != c.NewUnit {
			p += fmt.Sprintf(" [%s→%s]", c.OldUnit, c.NewUnit)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

// ── 약제급여목록 자동 받기 (공공데이터포털 약가기준정보조회서비스) ──────

type MasterResult struct {
	Skipped  bool   `json:"skipped"`
	Updated  bool   `json:"updated"`
	Count    int    `json:"count"`
	ListDate string `json:"listDate"`
	Message  string `json:"message"`
}

const (
	setMasterFetched      = "master_fetched_at"   // 마지막으로 받은 시각 (화면 표시)
	setMasterMonth        = "master_month_done"   // 이번 달 목록(적용일이 이 달)을 받은 달 "2006-01" — 그달에는 다시 받지 않음
	setMasterCheckedOn    = "master_checked_on"   // 이번 달 목록이 아직 없어 확인만 한 날 — 하루 한 번만
	setMasterFailedAt     = "master_failed_at"    // 마지막 실패 (6시간 뒤 다시)
	setMasterFailNotified = "master_fail_noticed" // 실패 알림을 보냈는가 (장애마다 한 번)
)

// dgamtPick : 제품코드별로 고른 약가기준정보 행
type dgamtPick struct {
	rec ingest.MasterRec
	eff string
	chg string // 변경이후약품코드 (자기 코드면 "")
}

// dgamtMaster : 약가기준정보를 급여목록으로 바꾼 결과
type dgamtMaster struct {
	Recs     []ingest.MasterRec // 지금 급여목록 ('삭제' 품목 뺌)
	ListDate string
	Changes  []codeChange // 코드 변경 (옛 코드 → 새 코드)
	Removed  int          // 급여 중단으로 뺀 품목 수
}

// masterFromDgamt : 약가기준정보 → 급여목록 행. 같은 제품코드가 여러 번이면 오늘 적용 중인 것(오늘 이전 적용일 중 가장 늦은 것),
// 아직 시행 전인 것뿐이면 가장 이른 것. listDate = 오늘 이전 적용일 중 가장 늦은 날.
// 지금 행이 '삭제'인 품목은 급여목록에서 빼고, 변경이후약품코드가 있으면 코드 변경으로 돌려준다.
func masterFromDgamt(items []crawler.DgamtItem, today string) dgamtMaster {
	type pick = dgamtPick
	best := map[string]pick{}
	beforeOf := map[string]string{} // 새 코드 → 변경이전코드 (새 코드 행의 chgBfMdsCd)
	var order []string
	listDate := ""
	for _, it := range items {
		code := domain.NormalizeProductCode(it.MdsCd)
		if !domain.IsEDICode(code) {
			continue
		}
		if bf := domain.NormalizeProductCode(it.ChgBfMdsCd); domain.IsEDICode(bf) && bf != code {
			beforeOf[bf] = code
		}
		eff, _ := domain.ParseFlexibleDate(it.AdtStaDd)
		price, _ := ingest.ParseNumber(it.MxCprc)
		rec := ingest.MasterRec{Code: code, Name: strings.TrimSpace(it.ItmNm), Company: strings.TrimSpace(it.MnfEntpNm), Spec: strings.TrimSpace(it.NomNm),
			Unit: strings.TrimSpace(it.Unit), Price: price, Route: strings.TrimSpace(it.InjcPthNm), IngredientCode: strings.TrimSpace(it.GnlNmCd),
			OTC: strings.TrimSpace(it.SpcGnlTpNm), Note: strings.TrimSpace(it.PayTpNm)}
		chg := domain.NormalizeProductCode(it.ChgAfMdsCd)
		if !domain.IsEDICode(chg) || chg == code {
			chg = ""
		}
		if eff != "" && eff <= today && eff > listDate {
			listDate = eff
		}
		cur, ok := best[code]
		switch {
		case !ok:
			order = append(order, code)
			best[code] = pick{rec, eff, chg}
		case eff <= today && (cur.eff > today || eff > cur.eff): // 적용 중인 것 중 가장 늦은 것
			best[code] = pick{rec, eff, chg}
		case eff > today && cur.eff > today && eff < cur.eff: // 둘 다 시행 전이면 가장 이른 것
			best[code] = pick{rec, eff, chg}
		}
	}
	after := map[string]string{}
	for c, p := range best {
		if p.chg != "" {
			after[c] = p.chg
		}
	}
	res := dgamtMaster{ListDate: listDate, Changes: resolveCodeChanges(best, after, beforeOf)}
	sort.Slice(res.Changes, func(a, b int) bool { return res.Changes[a].Old < res.Changes[b].Old })
	res.Recs = make([]ingest.MasterRec, 0, len(order))
	for _, c := range order {
		if payDeleted(best[c].rec.Note) {
			continue
		}
		res.Recs = append(res.Recs, best[c].rec)
	}
	res.Removed = len(order) - len(res.Recs) - len(res.Changes)
	if res.ListDate == "" {
		res.ListDate = today
	}
	return res
}

func masterSHA(recs []ingest.MasterRec) string {
	lines := make([]string, 0, len(recs))
	for _, r := range recs {
		lines = append(lines, fmt.Sprintf("%s|%s|%s|%s|%g|%s|%s|%s|%s|%s", r.Code, r.Name, r.Spec, r.Unit, r.Price, r.IngredientCode, r.Company, r.Route, r.OTC, r.Note))
	}
	sort.Strings(lines)
	return shaHex([]byte(strings.Join(lines, "\n")))
}

// CheckDrugMaster : 약제급여목록 전체를 공공데이터포털에서 받는다 (인증키가 있을 때).
// force=false 면 이번 달 목록(적용일이 이번 달)을 받은 뒤로는 그달에 다시 받지 않고, 아직 지난달 목록뿐이면 하루 한 번 다시 본다.
// 실패하면 6시간 뒤 다시. 내용이 같으면 바꾸지 않는다.
// 바꾸기 전에 지금 목록과 견준다(masterGuard) — 더 옛 목록·크게 적은 목록·규격 단위가 많이 다른 목록으로는 바꾸지 않는다.
func (s *Service) CheckDrugMaster(ctx context.Context, force bool) (*MasterResult, error) {
	key := crawler.NormalizeServiceKey(s.DB.GetSetting(SetDataGoKey, ""))
	if key == "" {
		return &MasterResult{Skipped: true, Message: "공공데이터포털 인증키를 넣으면 약제급여목록을 매달 자동으로 받습니다"}, nil
	}
	if !s.masterMu.TryLock() {
		return &MasterResult{Skipped: true, Message: "이미 받는 중입니다"}, nil
	}
	defer s.masterMu.Unlock()
	now := s.NowKST()
	month, today := now.Format("2006-01"), now.Format("2006-01-02")
	if !force {
		if s.DB.GetSetting(setMasterMonth, "") == month {
			return &MasterResult{Skipped: true, Message: "이번 달 약제급여목록을 이미 받았습니다"}, nil
		}
		if s.DB.GetSetting(setMasterCheckedOn, "") == today {
			return &MasterResult{Skipped: true, Message: "오늘 확인했습니다 — 이번 달 목록이 나오면 받습니다"}, nil
		}
		if t, err := time.Parse(time.RFC3339, s.DB.GetSetting(setMasterFailedAt, "")); err == nil && time.Since(t) < 6*time.Hour {
			return &MasterResult{Skipped: true, Message: "최근에 실패해 6시간 뒤 다시 받습니다"}, nil
		}
	}
	job, ok := s.tryStartJob("drug_master")
	if !ok {
		return &MasterResult{Skipped: true, Message: "다른 창(작업 스케줄러)에서 받는 중입니다"}, nil
	}
	fail := func(err error) (*MasterResult, error) {
		if ctx.Err() != nil { // 앱 종료·시간 한도 → 실패로 세지 않는다
			s.finishJob(job, "error", "중단됨 (프로그램 종료 또는 시간 초과)")
			return nil, err
		}
		_ = s.DB.SetSetting(setMasterFailedAt, time.Now().Format(time.RFC3339))
		s.finishJob(job, "error", err.Error())
		s.Log.Warn("약제급여목록 받기 실패", "err", err)
		if s.DB.GetSetting(setMasterFailNotified, "") == "" { // 장애마다 한 번만 알린다
			_ = s.DB.SetSetting(setMasterFailNotified, store.Now())
			hint := " — 자동으로 다시 시도합니다."
			switch {
			case errors.Is(err, crawler.ErrDgamtKeyNotReady):
				hint = " — 활용신청 승인 직후라면 1~2시간 뒤 자동으로 다시 받습니다. 계속 이러면 설정 → 자동 확인에서 인증키를 확인하세요."
			case errors.Is(err, crawler.ErrDgamtKey):
				hint = " — 설정 → 자동 확인에서 공공데이터포털 인증키를 확인하세요."
			case errors.Is(err, errMasterRefused):
				hint = " — 지금 목록을 그대로 씁니다. 계속 이러면 설정 → 오류 보고로 알려 주세요."
			}
			s.notify("warn", "약제급여목록을 받지 못했습니다", err.Error()+hint, "settings")
		}
		return nil, err
	}
	items, err := NewCrawler().FetchDgamtAll(ctx, key, nil)
	if err != nil {
		return fail(err)
	}
	dm := masterFromDgamt(items, today)
	recs, listDate := dm.Recs, dm.ListDate
	if len(recs) < 1000 { // 한 달 목록은 2만 품목 넘게 — 이상하게 적으면 바꾸지 않는다
		return fail(fmt.Errorf("%w: 약제급여목록이 이상하게 적습니다 (%d품목)", errMasterRefused, len(recs)))
	}
	// 받기는 성공 — 이번 달 목록이면 그달은 끝, 아직 지난달 목록이면 내일 다시
	done := func() {
		_ = s.DB.SetSetting(setMasterFailNotified, "")
		_ = s.DB.SetSetting(setMasterFailedAt, "")
		_ = s.DB.SetSetting(setMasterFetched, time.Now().Format(time.RFC3339))
		if strings.HasPrefix(listDate, month) {
			_ = s.DB.SetSetting(setMasterMonth, month)
		} else {
			_ = s.DB.SetSetting(setMasterCheckedOn, today)
		}
	}
	s.importMu.Lock() // 가져오기와 겹치지 않게 (단위 재계산이 같은 표를 쓴다) — 견주기도 같은 잠금 안에서
	defer s.importMu.Unlock()
	older, why := s.masterGuard(recs, listDate)
	if older { // 직접 올린 더 새 목록이 있음 — 실패가 아니다
		done()
		msg := why + " — 바꾸지 않았습니다"
		s.finishJob(job, "ok", msg)
		return &MasterResult{Count: len(recs), ListDate: listDate, Message: msg}, nil
	}
	if why != "" {
		return fail(fmt.Errorf("%w: %s", errMasterRefused, why))
	}
	sha := shaHex([]byte(masterSHA(recs) + "|" + listDate + "|" + strconv.Itoa(len(dm.Changes)))) // 내용이 같아도 새 달 목록이면 적용일을 바꿔 둔다
	var lastSHA string
	_ = s.DB.QueryRow("SELECT sha256 FROM import_batch WHERE kind = 'drug_master' ORDER BY id DESC LIMIT 1").Scan(&lastSHA)
	if lastSHA == sha {
		done()
		msg := fmt.Sprintf("약제급여목록이 그대로입니다 (%s품목, 적용일 %s)", comma(len(recs)), listDate)
		s.finishJob(job, "ok", msg)
		return &MasterResult{Count: len(recs), ListDate: listDate, Message: msg}, nil
	}
	_, n, carried, cr, err := s.storeDrugMasterWith("공공데이터포털 약가기준정보 ("+listDate+")", sha, listDate, recs, 0, dm.Changes)
	if err != nil {
		return fail(err)
	}
	done()
	msg := fmt.Sprintf("약제급여목록 %s품목을 새로 받았습니다 (적용일 %s, 급여 중단 %s품목 제외). 단위 환산을 다시 계산했습니다.", comma(n), listDate, comma(dm.Removed)) +
		carriedNote(carried) + codeChangeNote(cr)
	if len(cr.Review) > 0 || len(cr.BothAnchors) > 0 {
		s.notify("warn", "제품코드 변경 확인 필요", strings.TrimSpace(codeChangeNote(cr)), "inventory")
	}
	s.finishJob(job, "ok", msg)
	s.notify("info", "약제급여목록 갱신", msg, "imports")
	s.Log.Info("약제급여목록 받음", "count", n, "list_date", listDate)
	if s.OnDataChanged != nil {
		s.OnDataChanged("drug_master")
	}
	return &MasterResult{Updated: true, Count: n, ListDate: listDate, Message: msg}, nil
}

// errMasterRefused : 받은 목록이 이상해서 바꾸지 않음 (지금 목록을 그대로 씀)
var errMasterRefused = errors.New("받은 약제급여목록을 쓰지 않았습니다")

// masterGuard : 자동으로 받은 목록으로 지금 목록을 바꿔도 되는지. older = 지금 목록이 더 새것(직접 올린 새 달 목록 등).
// why 가 있으면 바꾸지 않는다:
//   - 품목 수가 지금 목록의 90% 미만 (미청구 의약품 대량 삭제 달에도 몇 % 정도라 넉넉히)
//   - 같은 제품코드의 고시 단위(규격·단위)가 5% 넘게 다름 — 같은 코드의 규격이 바뀌는 일은 거의 없다.
//     포털 자료(nomNm·unit)의 뜻이 엑셀 목록(규격·단위)과 다르면 단위 환산이 통째로 틀어지므로 막는다.
func (s *Service) masterGuard(recs []ingest.MasterRec, listDate string) (older bool, why string) {
	var curN int
	var curDate string
	_ = s.DB.QueryRow("SELECT COUNT(*), IFNULL(MAX(list_date), '') FROM drug_master WHERE "+sqlCurrentMaster).Scan(&curN, &curDate)
	if curN == 0 {
		return false, ""
	}
	if listDate < curDate {
		return true, fmt.Sprintf("받은 목록(적용일 %s)보다 지금 목록(적용일 %s)이 더 새것입니다", listDate, curDate)
	}
	byCode := make(map[string]ingest.MasterRec, len(recs))
	for _, r := range recs {
		byCode[r.Code] = r
	}
	if len(byCode)*10 < curN*9 {
		return false, fmt.Sprintf("받은 목록이 지금 목록보다 크게 적습니다 (%s / %s품목)", comma(len(byCode)), comma(curN))
	}
	rows, err := s.DB.Query("SELECT code, spec, unit FROM drug_master WHERE " + sqlCurrentMaster)
	if err != nil {
		return false, "지금 목록을 읽지 못했습니다"
	}
	defer rows.Close()
	both, diff := 0, 0
	for rows.Next() {
		var code, spec, unit string
		if rows.Scan(&code, &spec, &unit) != nil {
			continue
		}
		r, ok := byCode[code]
		if !ok {
			continue
		}
		both++
		if domain.ParsePriceUnit(spec, unit).Label != domain.ParsePriceUnit(r.Spec, r.Unit).Label {
			diff++
		}
	}
	if both >= 100 && diff*20 > both {
		return false, fmt.Sprintf("같은 제품코드 %s개 중 %s개(%d%%)의 고시 단위(규격·단위)가 지금 목록과 다릅니다", comma(both), comma(diff), diff*100/both)
	}
	return false, ""
}

// runMasterLogged : 약제급여목록 자동 받기 (인증키가 없거나 이번 달에 받았으면 건너뜀)
func (s *Service) runMasterLogged(ctx context.Context) {
	if s.DB.GetSetting(SetMasterAuto, "1") != "1" || s.DB.GetSetting(SetDataGoKey, "") == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if res, err := s.CheckDrugMaster(cctx, false); err != nil {
		s.Log.Warn("약제급여목록 확인 실패", "err", err)
	} else if !res.Skipped {
		s.Log.Info("약제급여목록 확인", "updated", res.Updated, "count", res.Count)
	}
}

// ── 공공데이터포털 인증키 (화면에서 안내·확인·저장) ──────────────────

// KeyCheck : 인증키 확인 결과
type KeyCheck struct {
	OK      bool   `json:"ok"`      // 바로 쓸 수 있음
	Pending bool   `json:"pending"` // 아직 등록 전 (승인 직후 1시간쯤 걸릴 수 있음) — 저장해 두면 나중에 자동으로 받는다
	Total   int    `json:"total"`   // 약가기준정보 전체 품목 수
	Message string `json:"message"`
}

// VerifyDataGoKey : 인증키로 한 품목을 조회해 본다 (저장하지 않음)
func (s *Service) VerifyDataGoKey(ctx context.Context, key string) KeyCheck {
	key = crawler.NormalizeServiceKey(key)
	if len(key) < 20 {
		return KeyCheck{Message: "인증키가 너무 짧습니다 — 공공데이터포털 마이페이지의 '일반 인증키'를 통째로 복사해 붙여 넣으세요"}
	}
	c := NewCrawler()
	c.Retries = 1
	total, err := c.CheckDgamtKey(ctx, key)
	switch {
	case err == nil && total <= 0: // 키는 받아들여졌지만 목록이 비었다 — 받기도 '전체 품목 수를 알 수 없음'으로 실패한다
		return KeyCheck{Message: "인증키는 받아들여졌지만 공공데이터포털이 약가기준정보를 0품목으로 알려 왔습니다 — 포털 API 사용법이 바뀌었을 수 있습니다. 설정 → 오류 보고로 알려 주세요."}
	case err == nil:
		return KeyCheck{OK: true, Total: total, Message: fmt.Sprintf("인증키가 확인되었습니다 — 약제급여목록 %s품목을 받을 수 있습니다", comma(total))}
	case errors.Is(err, crawler.ErrDgamtKeyNotReady):
		return KeyCheck{Pending: true, Message: "등록되지 않은 인증키입니다. 먼저 키를 끝까지 정확히 복사했는지 확인하세요. 활용신청 승인 직후라면 등록까지 1시간쯤 걸립니다. 그런 경우 [그래도 저장]을 눌러 두면 등록된 뒤 받을 수 있습니다."}
	case errors.Is(err, context.DeadlineExceeded):
		return KeyCheck{Message: "공공데이터포털이 응답하지 않습니다 — 인터넷 연결을 확인하고 잠시 뒤 다시 눌러 보세요"}
	case errors.Is(err, context.Canceled):
		return KeyCheck{Message: "확인을 멈췄습니다 (프로그램을 닫는 중)"}
	default:
		return KeyCheck{Message: err.Error()}
	}
}

// SetDataGoKey : 인증키만 저장한다 ("" 이면 지움). 값은 기록에 남기지 않고, 실패 대기를 지워 바로 다시 받게 한다.
func (s *Service) SetDataGoKey(key string) error {
	key = crawler.NormalizeServiceKey(key)
	old := s.DB.GetSetting(SetDataGoKey, "")
	if key == old {
		return nil
	}
	for k, v := range map[string]string{SetDataGoKey: key, setMasterFailedAt: "", setMasterFailNotified: ""} {
		if err := s.DB.SetSetting(k, v); err != nil {
			return err
		}
	}
	what := "저장"
	if key == "" {
		what = "지움"
	}
	s.Log.Info("공공데이터포털 인증키 "+what, "len", len(key))
	return nil
}
