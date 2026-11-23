package service

import (
	"database/sql"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// 단위 환산 — 사입·초기재고 원본 수량을 고시 단위로 바꾼다 (규칙은 domain/units.go).
//
// 배율은 품목 × (이팜 단위, 단위수량) 묶음마다 정한다. 같은 품목이라도 '정 ×28' 행과 '팩 ×1' 행이 섞여 있기 때문이다.
// 급여목록·고시·사용자 지정이 바뀔 때마다 다시 계산하므로, 어떤 순서로 가져와도 결과가 같다.

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Exec(query string, args ...any) (sql.Result, error)
}

type priceStep struct {
	eff           string
	before, after float64
}

// unitRef : 품목의 고시 단위와 시점별 상한금액
type unitRef struct {
	pu       domain.PriceUnit
	master   float64
	steps    []priceStep // 시행일 순
	hasUnit  bool
	hasPrice bool
}

// priceAt : t 시점 상한금액 추정 — t 이후 시행되는 첫 변경의 '변경 전' 가격 → 급여목록 → 마지막 '변경 후' 가격
func (r *unitRef) priceAt(t string) float64 {
	for _, st := range r.steps {
		if st.eff > t && st.before > 0 {
			return st.before
		}
	}
	if r.master > 0 {
		return r.master
	}
	for i := len(r.steps) - 1; i >= 0; i-- {
		if r.steps[i].eff <= t && r.steps[i].after > 0 {
			return r.steps[i].after
		}
	}
	for _, st := range r.steps {
		if st.before > 0 {
			return st.before
		}
	}
	return 0
}

func codeFilter(col string, codes []string) (string, []any) {
	if codes == nil {
		return "1=1", nil
	}
	if len(codes) == 0 {
		return "0=1", nil
	}
	in, args := inClause(codes)
	return col + " IN (" + in + ")", args
}

// loadUnitRefs : 급여목록(우선) → 고시 변경 기록에서 고시 단위·가격을 모은다.
func loadUnitRefs(q querier, codes []string) (map[string]*unitRef, error) {
	refs := map[string]*unitRef{}
	get := func(c string) *unitRef {
		r := refs[c]
		if r == nil {
			r = &unitRef{}
			refs[c] = r
		}
		return r
	}
	w, args := codeFilter("code", codes)
	rows, err := q.Query("SELECT code, spec, unit, IFNULL(price, 0) FROM drug_master WHERE "+w, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c, spec, unit string
		var price float64
		if rows.Scan(&c, &spec, &unit, &price) == nil {
			r := get(c)
			if unit != "" {
				r.pu, r.hasUnit = domain.ParsePriceUnit(spec, unit), true
			}
			if price > 0 {
				r.master, r.hasPrice = price, true
			}
		}
	}
	rows.Close()
	w, args = codeFilter("product_code", codes)
	rows, err = q.Query(`SELECT product_code, spec, unit, IFNULL(price_before, 0), IFNULL(price_after, 0), effective_date FROM price_change
		WHERE `+w+` ORDER BY effective_date, id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c, spec, unit, eff string
		var before, after float64
		if rows.Scan(&c, &spec, &unit, &before, &after, &eff) == nil {
			r := get(c)
			if !r.hasUnit && unit != "" {
				r.pu, r.hasUnit = domain.ParsePriceUnit(spec, unit), true
			}
			if before > 0 || after > 0 {
				r.steps = append(r.steps, priceStep{eff, before, after})
				r.hasPrice = true
			}
		}
	}
	rows.Close()
	for _, r := range refs {
		r.normalizeBasis()
	}
	return refs, nil
}

// normalizeBasis : 고시 가격의 기준 단위가 도중에 바뀐 품목(예: 3mL/펜 — mL 당 가격 → 펜 당 가격)의 옛 가격을 지금 기준으로 맞춘다.
// 규격·단위 글자는 그대로인데 상한금액만 '고시 1단위에 든 양'(Size)배쯤 뛰거나 그만큼 줄면 기준이 바뀐 것으로 본다 (±15%).
// 맞추지 않으면 옛 가격을 지금 단위의 가격으로 읽어, 그 무렵 기록(초기재고·사입)의 수량이 Size 배 틀어진다.
func (r *unitRef) normalizeBasis() {
	n := r.pu.Size
	if !r.hasUnit || n < 2 {
		return
	}
	near := func(x, target float64) bool { return x > target*0.85 && x < target*1.15 }
	for i := range r.steps {
		st := &r.steps[i]
		if st.before <= 0 || st.after <= 0 {
			continue
		}
		k := 0.0
		switch ratio := st.after / st.before; {
		case near(ratio, n):
			k = n // 낱 단위 가격 → 용기 가격
		case near(ratio, 1/n):
			k = 1 / n
		}
		if k == 0 {
			continue
		}
		st.before *= k
		for j := 0; j < i; j++ {
			r.steps[j].before *= k
			r.steps[j].after *= k
		}
	}
}

type unitGroup struct {
	code, label string
	pack        float64
	kinds       map[string]bool
	ratios      []float64
	rows        int
	purchases   int    // 사입 행 수 (바코드 기본 수량은 실제로 산 포장으로 정한다)
	lastBought  string // 마지막 사입일
	curMin      float64
	curMax      float64
	dec         domain.UnitDecision
}

var leadingOne = regexp.MustCompile(`^1(\D)`)

// normLabel : 이팜 단위 이름 비교용 — 대소문자·공백·흔한 다른 표기('캅셀'='캡슐', 'T'='정', '1정'='정')를 맞춘다.
func normLabel(s string) string {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	s = leadingOne.ReplaceAllString(s, "$1")
	switch s {
	case "캅셀", "캅슐", "cap", "caps", "capsule":
		return "캡슐"
	case "t", "tab", "tabs", "tablet", "정제":
		return "정"
	}
	return s
}

// labelIsBase : 이팜 단위 이름이 고시 단위 자체인가 (예: 고시 '팩(28정)' 에 '팩', 고시 '정' 에 '정').
// 'mL/펜' 처럼 규격 표기를 단위로 쓴 경우는 낱개(mL)인지 용기(펜)인지 알 수 없으므로 같다고 보지 않는다.
func labelIsBase(label string, pu domain.PriceUnit) bool {
	nl := normLabel(label)
	if nl == "" || !pu.Known {
		return false
	}
	return nl == normLabel(pu.Short) || nl == normLabel(pu.Label)
}

// sameLabelDecision : 같은 단위 이름의 사입 묶음 판단 중 근거(단가 표본)가 가장 많은 것.
// 원본 수량은 단위 이름의 낱개 기준이라(정 ×28 사입 = 28정) 단위수량이 달라도 배율이 같다.
// 포장 수량 오기 보정(auto_pack)은 그 묶음의 단위수량에만 맞으므로 물려주지 않는다 (예: 90정 반품에 ×90 을 곱하면 안 됨).
func sameLabelDecision(label string, gs []*unitGroup) (domain.UnitDecision, bool) {
	nl := normLabel(label)
	var best *unitGroup
	for _, o := range gs {
		if normLabel(o.label) != nl {
			continue
		}
		switch {
		case o.dec.Status == "manual":
		case len(o.ratios) > 0 && (o.dec.Status == "ok" || o.dec.Status == "auto_unit"):
		default:
			continue
		}
		if best == nil || len(o.ratios) > len(best.ratios) || (len(o.ratios) == len(best.ratios) && o.rows > best.rows) {
			best = o
		}
	}
	if best == nil {
		return domain.UnitDecision{}, false
	}
	return domain.UnitDecision{Factor: best.dec.Factor, Status: best.dec.Status, Note: best.dec.Note}, true
}

func kindsLabel(kinds map[string]bool) string {
	names := map[string]string{"purchase": "사입", "return": "반품", "borrow_in": "차용입고", "borrow_out": "차용출고", "disposal": "폐기", "other": "기타"}
	var out []string
	for _, k := range sortedKeys(kinds) {
		if n, ok := names[k]; ok {
			out = append(out, n)
		} else {
			out = append(out, k)
		}
	}
	return strings.Join(out, "·")
}

// recomputeUnits 는 품목(codes; nil 이면 전체)의 사입·초기재고·실사 배율을 다시 정하고 고시 단위 수량을 갱신한다.
// 같은 데이터면 가져온 순서·읽은 순서와 상관없이 같은 결과가 나오도록 묶음을 정렬해 판단한다.
func recomputeUnits(q querier, codes []string) error {
	refs, err := loadUnitRefs(q, codes)
	if err != nil {
		return err
	}
	// 사용자 지정 배율·포장
	manual := map[string]float64{}
	w, args := codeFilter("code", codes)
	rows, err := q.Query("SELECT code, IFNULL(unit_factor, 0) FROM product WHERE unit_factor > 0 AND "+w, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c string
		var f float64
		if rows.Scan(&c, &f) == nil {
			manual[c] = f
		}
	}
	rows.Close()
	// 알려진 포장 수량 (표준코드 제품총수량)
	packs := map[string][]float64{}
	w, args = codeFilter("product_code", codes)
	rows, err = q.Query("SELECT product_code, pack_qty FROM gtin_map WHERE pack_qty > 0 AND "+w+" ORDER BY product_code, pack_qty", args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c string
		var p float64
		if rows.Scan(&c, &p) == nil {
			packs[c] = append(packs[c], p)
			if r := refs[c]; r != nil && r.hasUnit {
				packs[c] = append(packs[c], r.pu.PerBarcode(p))
			}
		}
	}
	rows.Close()

	// ── 사입 묶음 ──
	groups := map[string]*unitGroup{}
	rows, err = q.Query(`SELECT product_code, unit_label, IFNULL(pack_size, 0), date, kind, IFNULL(raw_qty, qty), IFNULL(raw_unit_price, 0), factor
		FROM purchase_line WHERE `+w, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c, label, date, kind string
		var pack, price, factor float64
		var raw int64
		if rows.Scan(&c, &label, &pack, &date, &kind, &raw, &price, &factor) != nil {
			continue
		}
		key := c + "\x00" + label + "\x00" + trimF(pack)
		g := groups[key]
		if g == nil {
			g = &unitGroup{code: c, label: label, pack: pack, kinds: map[string]bool{}, curMin: factor, curMax: factor}
			groups[key] = g
		}
		g.rows++
		g.kinds[kind] = true
		if kind == "purchase" {
			g.purchases++
			if date > g.lastBought {
				g.lastBought = date
			}
		}
		if factor < g.curMin {
			g.curMin = factor
		}
		if factor > g.curMax {
			g.curMax = factor
		}
		if kind == "purchase" && raw > 0 && price > 0 {
			if r := refs[c]; r != nil {
				if p := r.priceAt(date); p > 0 {
					g.ratios = append(g.ratios, price/p)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	order := sortedKeys(groups) // 품목 → 단위 이름 → 단위수량 순 (읽은 순서와 무관)
	byCode := map[string][]*unitGroup{}
	for _, key := range order {
		g := groups[key]
		byCode[g.code] = append(byCode[g.code], g)
	}
	puOf := func(c string) domain.PriceUnit {
		if r := refs[c]; r != nil {
			return r.pu
		}
		return domain.PriceUnit{}
	}
	for c, gs := range byCode {
		pu := puOf(c)
		kp := append([]float64(nil), packs[c]...)
		for _, g := range gs { // 같은 품목의 다른 묶음 포장 수량 (예: 1 과 90 이 섞인 경우 90)
			if g.pack > 1 {
				kp = append(kp, g.pack)
			}
		}
		for _, g := range gs {
			switch {
			case manual[c] > 0:
				g.dec = domain.UnitDecision{Factor: manual[c], Status: "manual", Note: "사용자 지정 배율"}
			case len(g.ratios) > 0:
				g.dec = domain.DecideFactor(domain.Median(g.ratios), pu, kp, g.label)
			default:
				g.dec = domain.UnitDecision{Factor: 0, Status: "no_reference"} // 아래에서 채움
			}
		}
		// 단가 근거가 있는 묶음이 하나라도 있는가 (없으면 품목 전체가 '근거 없음')
		hasEvidence := false
		for _, g := range gs {
			if len(g.ratios) > 0 {
				hasEvidence = true
				break
			}
		}
		// 단가 근거가 없는 묶음(반품·폐기만 있는 등):
		//   1) 같은 단위 이름의 묶음 판단을 물려받는다 (auto_pack 제외)
		//   2) 단위 이름이 고시 단위 자체면 배율 1
		//   3) 그 외에는 근거가 없으므로 배율 1 + '확인 필요' (다른 단위 묶음의 배율을 짐작해 곱하지 않는다)
		for _, g := range gs {
			if g.dec.Factor > 0 {
				continue
			}
			switch d, ok := sameLabelDecision(g.label, gs); {
			case ok:
				g.dec = d
			case labelIsBase(g.label, pu):
				g.dec = domain.UnitDecision{Factor: 1, Status: "ok"}
			case !pu.Known: // 고시 단위를 모름 → 판단할 수 없음
				g.dec = domain.UnitDecision{Factor: 1, Status: "no_reference"}
			case !hasEvidence:
				g.dec = domain.UnitDecision{Factor: 1, Status: "check",
					Note: fmt.Sprintf("단가가 있는 사입 기록이 없어 %s 기록(단위 '%s' ×%s)을 고시 단위로 바꿀 근거가 없음 — 수량 확인 필요", kindsLabel(g.kinds), g.label, trimF(g.pack))}
			default:
				g.dec = domain.UnitDecision{Factor: 1, Status: "check",
					Note: fmt.Sprintf("단가가 없는 %s 기록(단위 '%s' ×%s)을 고시 단위로 바꿀 근거가 없음 — 수량 확인 필요", kindsLabel(g.kinds), g.label, trimF(g.pack))}
			}
		}
	}
	for _, key := range order {
		g := groups[key]
		f := g.dec.Factor
		if f <= 0 {
			f = 1
		}
		if g.curMin == f && g.curMax == f {
			continue
		}
		if _, err := q.Exec(`UPDATE purchase_line SET factor = ?1, qty = CAST(ROUND(IFNULL(raw_qty, qty) * ?1) AS INTEGER),
			unit_price = CASE WHEN raw_unit_price IS NULL THEN unit_price ELSE raw_unit_price / ?1 END
			WHERE product_code = ?2 AND unit_label = ?3 AND IFNULL(pack_size, 0) = ?4`, f, g.code, g.label, g.pack); err != nil {
			return err
		}
	}

	// ── 초기재고·실사 파일 행 ──
	// 단가가 있으면 그 시점(초기재고 = 기준일, 실사 = 실사일) 고시 가격과 비교해 정하고,
	// 없으면 같은 단위 이름의 사입 판단을 따른다. 그래도 근거가 없고 이 약의 사입 기록이 고시 단위와 다르면 '확인 필요'.
	type sline struct {
		id       int64
		code     string
		price    float64
		factor   float64
		label    string
		count    bool
		asOf     string
		anchorID int64
	}
	var lines []sline
	rows, err = q.Query("SELECT id, product_code, IFNULL(raw_unit_price, 0), factor, unit_label FROM initial_line WHERE "+w+" ORDER BY id", args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var l sline
		if rows.Scan(&l.id, &l.code, &l.price, &l.factor, &l.label) == nil {
			lines = append(lines, l)
		}
	}
	rows.Close()
	cw, cargs := codeFilter("l.product_code", codes)
	rows, err = q.Query(`SELECT l.id, l.product_code, IFNULL(l.raw_unit_price, 0), l.factor, l.unit_label, a.as_of, a.id
		FROM count_line l JOIN stock_anchor a ON a.id = l.anchor_id WHERE `+cw+` ORDER BY l.id`, cargs...)
	if err != nil {
		return err
	}
	for rows.Next() {
		l := sline{count: true}
		if rows.Scan(&l.id, &l.code, &l.price, &l.factor, &l.label, &l.asOf, &l.anchorID) == nil {
			lines = append(lines, l)
		}
	}
	rows.Close()
	baseline := ""
	_ = q.QueryRow("SELECT value FROM app_setting WHERE key = ?", SetBaselineDate).Scan(&baseline)
	if baseline == "" {
		baseline = "9999-12-31"
	}
	canonPrice := map[string]float64{} // 사입 최신 고시 단위 단가 (참조 가격이 없을 때 비교 기준)
	if len(lines) > 0 {
		prow, err := q.Query(`SELECT product_code, unit_price FROM (SELECT product_code, unit_price,
			ROW_NUMBER() OVER (PARTITION BY product_code ORDER BY date DESC, id DESC) AS rn
			FROM purchase_line WHERE kind = 'purchase' AND qty > 0 AND unit_price > 0 AND `+w+`) WHERE rn = 1`, args...)
		if err != nil {
			return err
		}
		for prow.Next() {
			var c string
			var p float64
			if prow.Scan(&c, &p) == nil {
				canonPrice[c] = p
			}
		}
		prow.Close()
	}
	nonUnit := func(c string) bool { // 이 약의 사입 기록 중 고시 단위와 다른(배율 ≠ 1) 것이 있는가
		for _, g := range byCode[c] {
			if g.dec.Factor > 0 && math.Abs(g.dec.Factor-1) > 1e-9 {
				return true
			}
		}
		return false
	}
	type lineDec struct {
		dec   domain.UnitDecision
		count bool
	}
	lineDecs := map[string][]lineDec{}
	touchedInit := map[string]bool{}
	touchedAnchor := map[int64]bool{}
	for _, l := range lines {
		pu := puOf(l.code)
		refDate := baseline
		if l.count {
			refDate = l.asOf
			touchedAnchor[l.anchorID] = true
		} else {
			touchedInit[l.code] = true
		}
		dec := domain.UnitDecision{Factor: 0, Status: "no_reference"}
		if l.price > 0 {
			ref := 0.0
			if r := refs[l.code]; r != nil {
				ref = r.priceAt(refDate)
			}
			if ref <= 0 {
				ref = canonPrice[l.code]
			}
			if ref > 0 {
				dec = domain.DecideFactor(l.price/ref, pu, packs[l.code], l.label)
			}
		}
		if dec.Factor <= 0 || (dec.Status == "no_reference" && l.price <= 0) {
			switch d, ok := sameLabelDecision(l.label, byCode[l.code]); {
			case ok:
				dec = d
				if dec.Note != "" {
					dec.Note = "같은 단위로 산 사입 기록 기준 — " + dec.Note
				}
			case labelIsBase(l.label, pu):
				dec = domain.UnitDecision{Factor: 1, Status: "ok"}
			case nonUnit(l.code):
				what := "초기재고"
				if l.count {
					what = "실사 파일"
				}
				dec = domain.UnitDecision{Factor: 1, Status: "check",
					Note: fmt.Sprintf("%s 행에 단가가 없고 단위('%s')가 사입 기록과 달라 고시 단위로 바꿀 근거가 없음 — 수량 확인 필요", what, l.label)}
			default:
				dec = domain.UnitDecision{Factor: 1, Status: "no_reference"}
			}
		}
		if dec.Factor <= 0 {
			dec.Factor = 1
		}
		lineDecs[l.code] = append(lineDecs[l.code], lineDec{dec, l.count})
		if dec.Factor != l.factor {
			table := "initial_line"
			if l.count {
				table = "count_line"
			}
			if _, err := q.Exec("UPDATE "+table+" SET factor = ?1, qty = CAST(ROUND(raw_qty * ?1) AS INTEGER) WHERE id = ?2", dec.Factor, l.id); err != nil {
				return err
			}
		}
	}
	if len(touchedInit) > 0 {
		if err := rebuildInitialAnchors(q, sortedKeys(touchedInit)); err != nil {
			return err
		}
	}
	if len(touchedAnchor) > 0 {
		if err := rebuildCountAnchors(q, slices.Sorted(maps.Keys(touchedAnchor))); err != nil {
			return err
		}
	}

	// 조제 단위수량 (이팜 청구 단위) — 고시 규격과 다르면 조제 수량의 단위를 믿을 수 없다
	dispSize := map[string]float64{}
	if drows, err := q.Query("SELECT code, IFNULL(disp_size, 0) FROM product WHERE disp_size > 0 AND "+strings.Replace(w, "product_code", "code", 1), args...); err == nil {
		for drows.Next() {
			var c string
			var v float64
			if drows.Scan(&c, &v) == nil {
				dispSize[c] = v
			}
		}
		drows.Close()
	}

	// ── 품목 요약 (화면 표시·바코드 기본 수량) ──
	all := map[string]bool{}
	for c := range byCode {
		all[c] = true
	}
	for c := range lineDecs {
		all[c] = true
	}
	for c, r := range refs {
		if r.hasUnit {
			all[c] = true
		}
	}
	// 근거가 모두 사라진 품목(예: 그 약이 나온 고시만 지움)도 요약을 비운다 — 옛 고시 단위·상태가 남지 않게
	if codes != nil {
		for _, c := range codes {
			all[c] = true
		}
	} else if srows, err := q.Query("SELECT code FROM product WHERE base_unit <> '' OR unit_status <> ''"); err == nil {
		for srows.Next() {
			var c string
			if srows.Scan(&c) == nil {
				all[c] = true
			}
		}
		srows.Close()
	}
	stmt := `UPDATE product SET base_unit = ?, base_label = ?, unit_status = ?, unit_note = ?, canon_pack = ? WHERE code = ?`
	for _, c := range sortedKeys(all) {
		r := refs[c]
		short, label := "", ""
		if r != nil && r.hasUnit {
			short, label = r.pu.Short, r.pu.Label
			if n := r.pu.ContainerNote(); n != "" {
				label += " (" + n + ")"
			}
		}
		var initDecs, countDecs []domain.UnitDecision
		for _, d := range lineDecs[c] {
			if d.count {
				countDecs = append(countDecs, d.dec)
			} else {
				initDecs = append(initDecs, d.dec)
			}
		}
		status, note := summarizeUnits(byCode[c], initDecs, countDecs, r)
		if ds, ok := dispSize[c]; ok && r != nil && r.hasUnit && r.pu.Known && math.Abs(ds-r.pu.Size) > 1e-6 {
			status = "check"
			msg := fmt.Sprintf("조제 기록의 단위수량 %s 이 고시 규격 %s 과 다름 — 조제 수량의 단위 확인 필요", trimF(ds), trimF(r.pu.Size))
			if note != "" {
				note = msg + " · " + note
			} else {
				note = msg
			}
		}
		var canon any
		var best *unitGroup
		// 가장 많이 산 포장 — 사입 행 수 → 마지막으로 산 것(지금 선반에 있을 포장) → 전체 행 수 → 포장 1개가 더 큰 쪽.
		// 반품만 있는 묶음이 사입 묶음을 이기지 않게 사입 수를 먼저 본다 (반품만 있는 약은 그 포장이라도 쓴다)
		better := func(g, b *unitGroup) bool {
			switch {
			case g.purchases != b.purchases:
				return g.purchases > b.purchases
			case g.lastBought != b.lastBought:
				return g.lastBought > b.lastBought
			case g.rows != b.rows:
				return g.rows > b.rows
			}
			return g.pack*g.dec.Factor > b.pack*b.dec.Factor
		}
		for _, g := range byCode[c] {
			if g.pack > 0 && (best == nil || better(g, best)) {
				best = g
			}
		}
		if best != nil {
			canon = best.pack * best.dec.Factor
		}
		if _, err := q.Exec(stmt, short, label, status, note, canon, c); err != nil {
			return err
		}
	}
	return nil
}

// summarizeUnits : 품목 전체 상태 — 확인 필요 > 사용자 지정 > 자동 환산 > 정상
func summarizeUnits(gs []*unitGroup, init, count []domain.UnitDecision, r *unitRef) (string, string) {
	if r == nil || !r.hasPrice {
		if len(gs) == 0 && len(init) == 0 && len(count) == 0 {
			return "", ""
		}
		return "no_reference", "급여목록·고시에 가격이 없어 단위를 확인할 수 없음 (급여목록표를 가져오면 자동 확인)"
	}
	rank := map[string]int{"check": 5, "manual": 4, "auto_pack": 3, "auto_unit": 2, "ok": 1, "no_reference": 0}
	status := ""
	notes := map[string]bool{}
	consider := func(d domain.UnitDecision, prefix string) {
		if rank[d.Status] > rank[status] || status == "" {
			status = d.Status
		}
		if d.Note != "" && d.Status != "ok" {
			notes[prefix+d.Note] = true
		}
	}
	for _, g := range gs {
		consider(g.dec, "")
	}
	for _, d := range init {
		if d.Status == "no_reference" {
			continue
		}
		consider(d, "초기재고: ")
	}
	for _, d := range count {
		if d.Status == "no_reference" {
			continue
		}
		consider(d, "실사: ")
	}
	if status == "" {
		status = "ok"
	}
	ns := sortedKeys(notes)
	return status, strings.Join(ns, " · ")
}

// rebuildCountAnchors : 실사 파일 기준점 수량 = 그 기준점 행의 고시 단위 수량 합
func rebuildCountAnchors(q querier, ids []int64) error {
	for _, id := range ids {
		if _, err := q.Exec("UPDATE stock_anchor SET qty = (SELECT IFNULL(SUM(qty), 0) FROM count_line WHERE anchor_id = ?1) WHERE id = ?1", id); err != nil {
			return err
		}
	}
	return nil
}

// rebuildInitialAnchors : 초기재고 기준점 = initial_line 합 (기준점이 없으면 기준일 설정으로 새로 만든다)
func rebuildInitialAnchors(q querier, codes []string) error {
	baseline, timing := "", "start"
	_ = q.QueryRow("SELECT value FROM app_setting WHERE key = ?", SetBaselineDate).Scan(&baseline)
	_ = q.QueryRow("SELECT value FROM app_setting WHERE key = ?", SetBaselineTiming).Scan(&timing)
	now := store.Now()
	for _, c := range codes {
		var qty int64
		var price sql.NullFloat64
		var batch sql.NullInt64
		if err := q.QueryRow(`SELECT IFNULL(SUM(qty), 0),
			(SELECT raw_unit_price / factor FROM initial_line WHERE product_code = ?1 AND raw_unit_price > 0 ORDER BY id LIMIT 1),
			(SELECT batch_id FROM initial_line WHERE product_code = ?1 ORDER BY id LIMIT 1)
			FROM initial_line WHERE product_code = ?1`, c).Scan(&qty, &price, &batch); err != nil {
			return err
		}
		res, err := q.Exec("UPDATE stock_anchor SET qty = ?, unit_price = ? WHERE source = 'initial' AND product_code = ?", qty, price, c)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 && baseline != "" {
			if _, err := q.Exec("INSERT INTO stock_anchor(product_code, as_of, timing, qty, source, unit_price, batch_id, created_at) VALUES(?, ?, ?, ?, 'initial', ?, ?, ?)",
				c, baseline, timing, qty, price, batch, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// priceUnitFor : 품목의 고시 단위 (급여목록 → 최근 고시)
func priceUnitFor(q querier, code string) (domain.PriceUnit, bool) {
	var spec, unit string
	if q.QueryRow("SELECT spec, unit FROM drug_master WHERE code = ? AND unit <> ''", code).Scan(&spec, &unit) == nil {
		return domain.ParsePriceUnit(spec, unit), true
	}
	if q.QueryRow("SELECT spec, unit FROM price_change WHERE product_code = ? AND unit <> '' ORDER BY effective_date DESC, id DESC LIMIT 1", code).Scan(&spec, &unit) == nil {
		return domain.ParsePriceUnit(spec, unit), true
	}
	return domain.PriceUnit{}, false
}

func trimF(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
