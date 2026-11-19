package service

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"dpr/internal/domain"
)

// 약품명 → 약품코드 매칭 (코드 없이 이름만 있는 초기재고·실사 행용).
//
// 이름 표기가 세 가지로 섞여 온다.
//
//	이팜·심평원: '바라크루드정1.0밀리그램(엔테카비르)_(1.06mg/1정)'
//	도매: '바라크루드정1mg 30T 한국BMS제약'
//	줄임말:       '가람 40', '나래 5 (0.25T)'
//
// 이름을 어간·제형·용량·업체로 나눈 뒤 후보마다 점수를 매긴다. 단가가 맞으면 강한 근거가 된다.
// 사전 = 약국 품목(사입·조제·고시) + 심평원 급여목록 전체. 확신이 없으면 후보만 돌려주고 사용자가 고른다.

type Candidate struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	UnitPrice float64 `json:"unitPrice"`
	Reason    string  `json:"reason,omitempty"` // 예: '단가 일치 · 용량 일치'
	Source    string  `json:"source,omitempty"` // pharmacy | master
}

type nameFeat struct {
	stem     string    // 브랜드 어간 (업체 접두가 붙을 수 있음)
	form     string    // 제형 (정·서방정·연질캡슐·시럽 …)
	nums     []float64 // 이름 본문 용량 (mg 환산, 단위 없는 수는 그대로)
	extra    []float64 // _(…) 안 용량 (참고)
	maker    string    // 도매 표기 끝의 업체명
	bareNums bool      // 단위 없는 숫자만 있음 (줄임말 '가람 40')
}

type nameEntry struct {
	code, name string
	company    string // 업체명 (한글만)
	feat       nameFeat
	prices     []float64 // 약국 사입 단가(고시 단위·원본), 급여목록 상한금액
	pharmacy   bool      // 약국 품목(사입·조제·고시에 나온 약)
	inMaster   bool      // 현재 급여목록에 있음
	lastSeen   string    // 마지막 사입·조제일
}

type nameIndex struct {
	entries []nameEntry
	exact   map[string][]int
	byGram  map[string][]int
	codes   map[string]bool // 약국 품목 코드 (product 표)
	byCode  map[string]int  // 코드 → entries 위치
}

var (
	reParen    = regexp.MustCompile(`\([^()]*\)`)
	rePackTok  = regexp.MustCompile(`^\d+(\.\d+)?(t|c|p|ea|ml|pen|v|a|g|정|캡슐|포|관|병|매|개|통|박스|box|bt)$`)
	reNumUnit  = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(mg|mcg|g|ml|iu|i\.u|단위|unit|%)?`)
	reThousand = regexp.MustCompile(`(\d),(\d{3})`)
	reUnitWord = regexp.MustCompile(`\b(mg|mcg|ml|g|iu|i\.u|unit|단위|mm|cm|kg)\b`)
	formWords  = []string{"구강붕해정", "필름코팅정", "서방캡슐", "연질캡슐", "경질캡슐", "건조시럽", "서방정", "장용정", "속붕정", "츄정", "발포정", "캡슐", "시럽", "현탁액", "점안액", "점안겔", "점이액", "내용액", "액", "세립", "과립", "산", "연고", "크림", "겔", "로션", "패취", "패치", "카타플라스마", "좌제", "좌약", "흡입", "주", "정"}
	formGroups = map[string]string{"구강붕해정": "tab", "필름코팅정": "tab", "서방정": "tab", "장용정": "tab", "속붕정": "tab", "츄정": "tab", "발포정": "tab", "정": "tab",
		"서방캡슐": "cap", "경질캡슐": "cap", "캡슐": "cap", "연질캡슐": "cap",
		"시럽": "liq", "현탁액": "liq", "내용액": "liq", "액": "liq", "건조시럽": "dry", "세립": "dry", "과립": "dry", "산": "dry",
		"연고": "top", "크림": "top", "겔": "top", "로션": "top", "점안액": "eye", "점안겔": "eye", "점이액": "eye",
		"패취": "patch", "패치": "patch", "카타플라스마": "patch", "좌제": "supp", "좌약": "supp", "흡입": "inh", "주": "inj"}
	unitSyn = strings.NewReplacer("캅셀", "캡슐", "카프셀", "캡슐", "밀리그램", "mg", "밀리그람", "mg", "마이크로그램", "mcg", "마이크로그람", "mcg",
		"μg", "mcg", "㎍", "mcg", "ug", "mcg", "㎎", "mg", "밀리리터", "ml", "㎖", "ml", "그램", "g", "그람", "g", "패치", "패취")
	latinKo = map[rune]string{'a': "에이", 'b': "비", 'c': "씨", 'd': "디", 'e': "이", 'f': "에프", 'g': "지", 'h': "에이치", 'i': "아이", 'j': "제이", 'k': "케이",
		'l': "엘", 'm': "엠", 'n': "엔", 'o': "오", 'p': "피", 'q': "큐", 'r': "알", 's': "에스", 't': "티", 'u': "유", 'v': "브이", 'w': "더블유", 'x': "엑스", 'y': "와이", 'z': "지"}
)

func exactKey(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(norm.NFKC.String(name)), ""))
}

var featCache sync.Map // 이름 → 특징 (순수 함수라 한 번 계산하면 재사용: 급여목록 2만여 개를 매번 다시 나누지 않는다)

// featuresOf 는 이름을 어간·제형·용량·업체로 나눈다.
func featuresOf(raw string) nameFeat {
	if v, ok := featCache.Load(raw); ok {
		return v.(nameFeat)
	}
	f := computeFeatures(raw)
	featCache.Store(raw, f)
	return f
}

func computeFeatures(raw string) nameFeat {
	s := strings.ToLower(norm.NFKC.String(raw))
	s = strings.TrimLeft(s, "[]<>*#- ")
	s = reThousand.ReplaceAllString(s, "$1$2") // 25,000IU → 25000IU
	var f nameFeat
	head, tail := s, ""
	if i := strings.Index(head, "_"); i >= 0 { // 이팜·심평원 '_(1.06mg/1정)' 꼬리 (괄호가 빠진 '_35mg/1정)' 도)
		head, tail = head[:i], head[i:]
	}
	// 도매 표기: '… 30T 한국BMS제약' → 포장 규격 토큰부터 끝까지는 업체·포장 정보
	toks := strings.Fields(reParen.ReplaceAllString(head, " "))
	for i, t := range toks {
		if rePackTok.MatchString(t) {
			if i+1 < len(toks) {
				f.maker = strings.Join(toks[i+1:], "")
			}
			toks = toks[:i]
			break
		}
	}
	head = unitSyn.Replace(strings.Join(toks, " "))
	// 용량
	hasUnit := false
	for _, m := range reNumUnit.FindAllStringSubmatch(head, -1) {
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		switch m[2] {
		case "g":
			v *= 1000
		case "mcg":
			v /= 1000
		}
		if m[2] != "" {
			hasUnit = true
		}
		f.nums = append(f.nums, v)
	}
	f.bareNums = len(f.nums) > 0 && !hasUnit
	for _, m := range reNumUnit.FindAllStringSubmatch(unitSyn.Replace(tail), -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && m[2] != "" {
			switch m[2] {
			case "g":
				v *= 1000
			case "mcg":
				v /= 1000
			}
			f.extra = append(f.extra, v)
		}
	}
	// 어간: 숫자·단위 제거 후 한글·영문만, 제형어는 떼어 낸다
	var b strings.Builder
	for _, r := range reUnitWord.ReplaceAllString(reNumUnit.ReplaceAllString(head, " "), " ") {
		switch {
		case unicode.Is(unicode.Hangul, r):
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteString(latinKo[r])
		}
	}
	stem := b.String()
	for _, w := range formWords {
		if i := strings.LastIndex(stem, w); i > 0 && i+len(w) == len(stem) {
			f.form, stem = w, stem[:i]
			break
		}
	}
	if f.form == "" { // 제형어가 중간에 있는 경우 ('바라크루드정' + 숫자 뒤 꼬리)
		for _, w := range formWords {
			if i := strings.Index(stem, w); i > 0 {
				f.form, stem = w, stem[:i]
				break
			}
		}
	}
	f.stem = stem
	return f
}

func gramsOf(stem string) []string {
	r := []rune(stem)
	if len(r) < 2 {
		if len(r) == 1 {
			return []string{string(r)}
		}
		return nil
	}
	out := make([]string, 0, len(r)-1)
	for i := 0; i+1 < len(r); i++ {
		out = append(out, string(r[i:i+2]))
	}
	return out
}

func (s *Service) buildNameIndex() *nameIndex {
	ix := &nameIndex{exact: map[string][]int{}, byGram: map[string][]int{}, codes: map[string]bool{}, byCode: map[string]int{}}
	prices := map[string][]float64{}
	addPrice := func(c string, p float64) {
		if p > 0 {
			prices[c] = append(prices[c], p)
		}
	}
	for code, p := range s.latestPurchasePrices("9999-12-31") {
		addPrice(code, p)
	}
	if rows, err := s.DB.Query(`SELECT product_code, raw_unit_price FROM (SELECT product_code, raw_unit_price,
		ROW_NUMBER() OVER (PARTITION BY product_code ORDER BY date DESC, id DESC) AS rn
		FROM purchase_line WHERE kind = 'purchase' AND raw_unit_price > 0 AND factor <> 1) WHERE rn = 1`); err == nil {
		for rows.Next() {
			var c string
			var p float64
			if rows.Scan(&c, &p) == nil {
				addPrice(c, p)
			}
		}
		rows.Close()
	}
	for code, p := range s.initialPrices() {
		addPrice(code, p)
	}
	byCode := ix.byCode
	lastSeen := map[string]string{}
	if rows, err := s.DB.Query(`SELECT product_code, MAX(d) FROM (SELECT product_code, MAX(date) AS d FROM purchase_line GROUP BY product_code
		UNION ALL SELECT product_code, MAX(date) FROM dispense_line GROUP BY product_code) GROUP BY product_code`); err == nil {
		for rows.Next() {
			var c, d string
			if rows.Scan(&c, &d) == nil {
				lastSeen[c] = d
			}
		}
		rows.Close()
	}
	add := func(code, name, company string, pharmacy bool, master float64) {
		if i, ok := byCode[code]; ok {
			e := &ix.entries[i]
			if master > 0 {
				e.prices = append(e.prices, master)
			}
			if !pharmacy {
				e.inMaster = true
			}
			if e.company == "" {
				e.company = makerKey(company)
			}
			return
		}
		e := nameEntry{code: code, name: name, company: makerKey(company), feat: featuresOf(name), prices: append([]float64(nil), prices[code]...),
			pharmacy: pharmacy, inMaster: !pharmacy, lastSeen: lastSeen[code]}
		if master > 0 {
			e.prices = append(e.prices, master)
		}
		i := len(ix.entries)
		ix.entries = append(ix.entries, e)
		byCode[code] = i
		ix.exact[exactKey(name)] = append(ix.exact[exactKey(name)], i)
		seen := map[string]bool{}
		for _, g := range gramsOf(e.feat.stem) {
			if !seen[g] {
				seen[g] = true
				ix.byGram[g] = append(ix.byGram[g], i)
			}
		}
	}
	if rows, err := s.DB.Query("SELECT code, name, company FROM product WHERE name <> ''"); err == nil {
		for rows.Next() {
			var c, n, co string
			if rows.Scan(&c, &n, &co) == nil {
				ix.codes[c] = true
				add(c, n, co, true, 0)
			}
		}
		rows.Close()
	}
	// 지금 급여목록만 (목록에서 빠졌지만 남겨 둔 행은 약국 품목으로 이미 들어 있다)
	if rows, err := s.DB.Query("SELECT code, name, company, IFNULL(price, 0) FROM drug_master WHERE name <> '' AND " + sqlCurrentMaster); err == nil {
		for rows.Next() {
			var c, n, co string
			var p float64
			if rows.Scan(&c, &n, &co, &p) == nil {
				add(c, n, co, false, p)
			}
		}
		rows.Close()
	}
	return ix
}

func (ix *nameIndex) has(code string) bool { return ix.codes[code] }

// nameOf : 사전에 있는 약품명 (약국 품목 → 급여목록 순 — 처음 넣은 이름)
func (ix *nameIndex) nameOf(code string) string {
	if i, ok := ix.byCode[code]; ok {
		return ix.entries[i].name
	}
	return ""
}

// numsMatch : 질의 용량이 후보 용량에 모두 있는가 (중복 횟수까지: 40/10/10 ≠ 40/20/10)
func numsMatch(q, c []float64) bool {
	used := make([]bool, len(c))
	for _, a := range q {
		ok := false
		for j, b := range c {
			if !used[j] && math.Abs(a-b) <= 0.011*math.Max(a, b) {
				used[j], ok = true, true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func bestGap(price float64, cands []float64) float64 {
	g := 1.0
	for _, p := range cands {
		if x := priceGap(p, price); x < g {
			g = x
		}
	}
	return g
}

// score 는 질의 이름과 후보의 일치 점수 (1 이상이면 강한 일치)와 근거.
func score(q nameFeat, price float64, e *nameEntry) (float64, []string) {
	var why []string
	cs := e.feat.stem
	var sc float64
	switch {
	case q.stem == "" || cs == "":
		return 0, nil
	case q.stem == cs:
		sc = 1
	case len([]rune(q.stem)) >= 2 && strings.Contains(cs, q.stem):
		sc = 0.85 // 업체 접두가 붙은 이름 ('부광아데포비어' ⊃ '아데포비어')
		if q.maker != "" && strings.HasPrefix(cs, firstRunes(q.maker, 2)) {
			sc += 0.1
		}
	case len([]rune(cs)) >= 2 && strings.HasPrefix(q.stem, cs):
		sc = 0.8
	default:
		sc = domain.Similarity(q.stem, cs) * 0.9
	}
	if sc < 0.5 {
		return sc, nil
	}
	// 용량
	if len(q.nums) > 0 {
		all := append(append([]float64(nil), e.feat.nums...), e.feat.extra...)
		switch {
		case len(q.nums) >= 2 && len(e.feat.nums) >= 2 && len(q.nums) != len(e.feat.nums):
			sc -= 0.6 // 복합제 성분 수가 다름 (5/160 ≠ 5/160/12.5)
		case numsMatch(q.nums, all):
			sc += 0.1
			why = append(why, "용량 일치")
		case len(all) > 0:
			if price > 0 && len(e.prices) > 0 && bestGap(price, e.prices) <= 0.03 {
				sc -= 0.25 // 단가가 정확히 같으면 표기 차이(염 기준 15.87mg ↔ 15mg)일 가능성이 크다
			} else {
				sc -= 0.6 // 다른 용량은 다른 약
			}
		default:
			sc -= 0.05
		}
	}
	// 제형
	if q.form != "" && e.feat.form != "" {
		switch {
		case q.form == e.feat.form:
			sc += 0.05
		case releaseKind(q.form) != releaseKind(e.feat.form):
			sc -= 0.5 // 서방·장용·구강붕해는 다른 약 (쿠에타핀정 ≠ 쿠에타핀서방정)
		case formGroups[q.form] == formGroups[e.feat.form]:
			sc -= 0.15
		default:
			sc -= 0.4
		}
	}
	// 단가
	if price > 0 && len(e.prices) > 0 {
		switch g := bestGap(price, e.prices); {
		case g <= 0.03:
			sc += 0.15
			why = append(why, "단가 일치")
		case g <= 0.1:
			sc += 0.08
			why = append(why, "단가 비슷")
		case g > 0.5:
			sc -= 0.15
		}
	}
	// 업체 (도매 표기 끝의 '유한', '한미약품', '유유(동아에스티)' …)
	if q.maker != "" && e.company != "" {
		mk := makerKey(q.maker)
		if mk != "" && (strings.Contains(e.company, mk) || strings.Contains(mk, e.company) || firstRunes(mk, 2) == firstRunes(e.company, 2)) {
			sc += 0.1
			why = append(why, "업체 일치")
		}
	}
	// 약국이 실제로 사고 판 약을 우선 (같은 이름의 다른 회사 제품·양도양수 전 코드보다)
	if e.pharmacy {
		sc += 0.15
	}
	return sc, why
}

// makerKey : '(주)한미약품' → '한미', '한국쿄와기린(엘지화학)' → '한국쿄와기린엘지화학'
func makerKey(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	for _, w := range []string{"주식회사", "(주)", "㈜", "제약", "약품", "바이오", "파마", "헬스케어", "코리아"} {
		s = strings.ReplaceAll(s, w, "")
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Hangul, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sameDrug : 같은 약의 다른 코드인가 (양도양수·포장 변경). 업체 접두('환인쿠에타핀')는 떼고 비교한다.
// 두 코드가 모두 지금 급여목록에 있고 업체가 다르면 같은 성분의 다른 회사 제품(제네릭)이다 — 묶지 않는다.
// (진짜 양도양수면 옛 코드는 목록에서 빠진다)
func sameDrug(a, b *nameEntry) bool {
	if a.inMaster && b.inMaster && a.company != "" && b.company != "" && a.company != b.company {
		return false
	}
	if identityStem(a) != identityStem(b) || a.feat.form != b.feat.form {
		return false
	}
	eq := func(x, y []float64) bool { return len(x) == len(y) && numsMatch(x, y) }
	return eq(a.feat.nums, b.feat.nums) && (len(a.feat.extra) == 0 || len(b.feat.extra) == 0 || eq(a.feat.extra, b.feat.extra))
}

func identityStem(e *nameEntry) string {
	st := e.feat.stem
	if e.company != "" && strings.HasPrefix(st, e.company) && len([]rune(st)) > len([]rune(e.company))+1 {
		return strings.TrimPrefix(st, e.company)
	}
	return st
}

// releaseKind : 방출 형태 (서방·장용·구강붕해/속붕·일반)
func releaseKind(form string) string {
	switch {
	case strings.Contains(form, "서방"):
		return "sr"
	case strings.Contains(form, "장용"):
		return "ec"
	case strings.Contains(form, "구강붕해"), strings.Contains(form, "속붕"):
		return "odt"
	}
	return ""
}

// preferCurrent : 같은 약의 두 코드 중 대표 — 약국이 최근에 사고 판 코드(재고 흐름이 기록되는 코드) → 현재 급여목록 코드 →
// 그 외에는 점수가 높은 쪽(a: 이미 대표인 쪽, 목록이 점수 순이라 점수가 같거나 높다)
func preferCurrent(a, b *nameEntry) *nameEntry {
	if a.lastSeen != b.lastSeen {
		if b.lastSeen > a.lastSeen {
			return b
		}
		return a
	}
	if a.inMaster != b.inMaster {
		if a.inMaster {
			return a
		}
		return b
	}
	return a
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func (ix *nameIndex) match(name string, firstToken bool, price float64) (string, []Candidate) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	// 1) 이름이 그대로 같으면 (여럿이면 단가로)
	if list := ix.exact[exactKey(name)]; len(list) > 0 {
		if len(list) == 1 {
			return ix.entries[list[0]].code, nil
		}
		found := ""
		for _, i := range list {
			if price > 0 && bestGap(price, ix.entries[i].prices) <= 0.05 {
				if found != "" && found != ix.entries[i].code {
					found = ""
					break
				}
				found = ix.entries[i].code
			}
		}
		if found != "" {
			return found, nil
		}
	}
	q := featuresOf(name)
	grams := gramsOf(q.stem)
	if len(grams) == 0 {
		return "", nil
	}
	hits := map[int]int{}
	for _, g := range grams {
		for _, i := range ix.byGram[g] {
			hits[i]++
		}
	}
	need := int(math.Ceil(float64(len(grams)) * 0.6))
	type scored struct {
		i   int
		sc  float64
		why []string
	}
	var list []scored
	for i, n := range hits {
		if n < need {
			continue
		}
		sc, why := score(q, price, &ix.entries[i])
		if sc >= 0.5 {
			list = append(list, scored{i, sc, why})
		}
	}
	sort.Slice(list, func(a, b int) bool {
		if list[a].sc != list[b].sc {
			return list[a].sc > list[b].sc
		}
		return ix.entries[list[a].i].code < ix.entries[list[b].i].code
	})
	// 같은 약이 코드 여러 개로 있으면(양도양수·포장 변경) 한 묶음으로 보고, 약국이 최근에 사고 판 코드 → 급여목록 코드를 대표로 쓴다
	type group struct {
		rep    *nameEntry
		top    scored
		prices []float64 // 묶음 안 모든 코드의 단가
	}
	var groups []group
	for _, sc := range list {
		e := &ix.entries[sc.i]
		merged := false
		for gi := range groups {
			if sameDrug(groups[gi].rep, e) {
				groups[gi].rep = preferCurrent(groups[gi].rep, e)
				groups[gi].prices = append(groups[gi].prices, e.prices...)
				merged = true
				break
			}
		}
		if !merged {
			groups = append(groups, group{rep: e, top: sc, prices: append([]float64(nil), e.prices...)})
		}
	}
	var cands []Candidate
	for _, g := range groups {
		src := "master"
		if g.rep.pharmacy {
			src = "pharmacy"
		}
		p := 0.0
		if len(g.rep.prices) > 0 {
			p = g.rep.prices[0]
		}
		cands = append(cands, Candidate{Code: g.rep.code, Name: g.rep.name, Score: math.Round(g.top.sc*100) / 100, UnitPrice: p, Reason: strings.Join(g.top.why, " · "), Source: src})
		if len(cands) == 5 {
			break
		}
	}
	if len(list) == 0 {
		return "", nil
	}
	// 2) 자동 확정: 강한 일치 + 다른 약과 점수 차이 + (단가가 있으면 10% 이내)
	top := groups[0].top
	second := 0.0
	if len(groups) > 1 {
		second = groups[1].top.sc
	}
	e := *groups[0].rep
	gp := groups[0].prices
	priceOK := price <= 0 || len(gp) == 0 || bestGap(price, gp) <= 0.1
	if price > 0 && len(gp) == 0 {
		// 후보에 단가 정보가 아예 없으면(사입 기록 없는 약) 이름이 똑같고 경쟁 후보가 없을 때만 확정
		priceOK = identityStem(&e) == q.stem && second < 0.6
	}
	if top.sc >= 1.0 && top.sc-second >= 0.149 && priceOK {
		return e.code, nil
	}
	return "", cands
}

func priceGap(a, b float64) float64 {
	if a <= 0 || b <= 0 {
		return 1
	}
	return math.Abs(a-b) / b
}

// SearchProducts 는 화면의 약품 선택용 검색 (이름·코드 부분 일치). 약국 품목 다음에 급여목록을 찾는다.
func (s *Service) SearchProducts(q string, limit int) ([]Candidate, error) {
	q = strings.TrimSpace(q)
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if q == "" {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT code, name, src FROM (
		SELECT code, name, 0 AS src FROM product WHERE code LIKE ?1 OR name LIKE ?2
		UNION ALL
		SELECT code, name, 1 AS src FROM drug_master WHERE (code LIKE ?1 OR name LIKE ?2) AND code NOT IN (SELECT code FROM product)
	) ORDER BY src, length(name) LIMIT ?3`, q+"%", "%"+q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		var src int
		if err := rows.Scan(&c.Code, &c.Name, &src); err != nil {
			return nil, err
		}
		c.Source = map[int]string{0: "pharmacy", 1: "master"}[src]
		out = append(out, c)
	}
	return out, rows.Err()
}

// namesAgree : 두 이름이 같은 약을 가리키는가 (어간 일치·포함 또는 유사도 0.6 이상)
func namesAgree(a, b string) bool {
	fa, fb := featuresOf(a), featuresOf(b)
	if fa.stem == "" || fb.stem == "" {
		return true // 판단 불가
	}
	if fa.stem == fb.stem || strings.Contains(fa.stem, fb.stem) || strings.Contains(fb.stem, fa.stem) {
		return true
	}
	return domain.Similarity(fa.stem, fb.stem) >= 0.6
}
