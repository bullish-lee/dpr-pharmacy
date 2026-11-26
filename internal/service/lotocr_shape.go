package service

// 사입정보 사진 판독 — 표 머리가 없어도 열을 정하는 부분.
//
// 화면 캡처를 본문 줄만 잘라 올리면 머리(사입일자·거래처·유효기간·제조번호)가 없다. 머리가 반쯤 잘리거나
// 일부 이름만 읽히는 일도 흔하다. 그래서 머리는 '있으면 쓰는 힌트'로만 두고, 열은 내용의 모양으로도 정한다.
//
//  1. 줄마다 OCR 단어를 칸(토큰)으로 묶는다 — 쪼개진 날짜·숫자, 띄어 쓴 도매 이름만 붙인다.
//  2. 칸마다 모양으로 종류를 정한다: 날짜(2026-10-16) · 8자리 날짜(20290531) · 구분(사입·반품) · 과세(포함·비과세)
//     · 소수 수량(60.00) · 금액(38,400) · 코드(제조번호) · 글자(도매 이름).
//     한 줄을 통째로 이어 붙이지 않는다 — 금액과 유효기간이 붙어 숫자가 섞이거나, 도매 이름의 '이'·'기'가 숫자로 바뀌지 않게.
//  3. 날짜가 있는 줄들에서 가로 위치가 겹치는 칸을 한 열로 모으고, 열의 종류는 다수결로 정한다.
//     그래서 한두 줄에서 글자가 틀리거나 칸이 비어도(유효기간·제조번호가 빈 사입) 옆 칸 값이 밀려 들어오지 않는다.
//  4. 열의 역할: 왼쪽 날짜 열 = 사입일자, 그 오른쪽의 (미래) 날짜 열 = 유효기간, 유효기간 옆 코드 열 = 제조번호,
//     소수 열의 처음·끝 = 수량·총수량, 날짜 오른쪽 글자 열 = 도매.
//  5. 머리를 찾았으면 머리 열을 그대로 쓰고, 머리에 없는 역할만 위 추정으로 채운다.

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type tokKind int

const (
	tkOther tokKind = iota
	tkDate          // 2026-10-16 (구분 기호가 있는 날짜)
	tkDate8         // 20290531
	tkKind          // 사입·반품·재고조정…
	tkTax           // 포함·별도·비과세…
	tkDec           // 60.00 (소수 둘째 자리 수량)
	tkAmt           // 38,400 · 590 (금액)
	tkCode          // 제조번호 모양 (영문+숫자, 또는 쉼표 없는 4자리 이상 숫자)
	tkText          // 글자 (도매 이름 등)
	tkKinds
)

// lotTok : 한 줄 안의 칸 하나 (붙어 있는 OCR 단어들)
type lotTok struct {
	t      string
	x0, x1 float64
	kind   tokKind
	neg    bool   // 음수 (반품·취소)
	date   string // tkDate·tkDate8 의 YYYY-MM-DD
	digits bool   // tkCode 가 숫자뿐
}

func (t lotTok) cx() float64 { return (t.x0 + t.x1) / 2 }

var (
	purchaseKinds = []string{"사입", "매입", "입고", "구입"}
	taxWords      = map[string]bool{"포함": true, "별도": true, "비과세": true, "과세": true, "면세": true, "영세": true}

	shapeDashDate  = regexp.MustCompile(`^(20\d{2})[-.](\d{1,2})[-.](\d{1,2})$`)
	shapeDate8     = regexp.MustCompile(`^20\d{6}$`)
	shapeAmount    = regexp.MustCompile(`^-?\d{1,3}([,.]\d{3})+$|^-?\d{1,3}$|^-\d+$`)
	shapeShortDate = regexp.MustCompile(`^[^\d\s]{1,2}(\d{2})-(\d{2})-(\d{2})$`) // 앞의 20 을 한글로 읽음: 꼬26-01-05
)

// numericForm : 숫자·기호·'숫자로 잘못 읽히는 글자'만으로 된 칸이면 숫자로 되돌린 문자열.
// 한글 음절(이·기·님·테)은 진짜 숫자보다 많으면 글자로 본다 — 도매 이름이 숫자가 되지 않게.
func numericForm(s string) (string, bool) {
	var b strings.Builder
	digits, hangul := 0, 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
			b.WriteRune(r)
		case r == '-' || r == '.' || r == ',':
			b.WriteRune(r)
		case r == '/':
			b.WriteRune('-')
		case unicode.IsSpace(r):
		default:
			d, ok := digitLookalike[r]
			if !ok {
				return "", false
			}
			if r >= 0xAC00 && r <= 0xD7A3 {
				hangul++
			}
			b.WriteString(d)
		}
	}
	if digits == 0 || hangul > digits {
		return "", false
	}
	return b.String(), true
}

func validDate(y, m, d string) (string, bool) {
	if len(m) == 1 {
		m = "0" + m
	}
	if len(d) == 1 {
		d = "0" + d
	}
	t, err := time.Parse("20060102", y+m+d)
	if err != nil {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

func hasNotPurchaseWord(s string) bool {
	for _, w := range notPurchase {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// isKindWord : 구분 칸의 글자 (사입·반품·재고조정재고정리…). 짧고 숫자가 없는 칸만.
func isKindWord(s string) (kind, purchase bool) {
	if len([]rune(s)) > 10 || strings.ContainsAny(s, "0123456789") {
		return false, false
	}
	if hasNotPurchaseWord(s) {
		return true, false
	}
	for _, w := range purchaseKinds {
		if s == w {
			return true, true
		}
	}
	return false, false
}

// classifyTok : 칸의 글자를 모양으로 분류한다
func classifyTok(text string) lotTok {
	s := strings.Join(strings.Fields(text), "")
	tok := lotTok{t: text}
	if s == "" {
		return tok
	}
	if k, _ := isKindWord(s); k {
		tok.kind = tkKind
		return tok
	}
	if taxWords[s] {
		tok.kind = tkTax
		return tok
	}
	if f, ok := numericForm(s); ok {
		switch {
		case shapeDashDate.MatchString(f):
			m := shapeDashDate.FindStringSubmatch(f)
			if d, ok := validDate(m[1], m[2], m[3]); ok {
				tok.kind, tok.date = tkDate, d
				return tok
			}
		case shapeDate8.MatchString(f):
			if d, ok := validDate(f[:4], f[4:6], f[6:8]); ok {
				tok.kind, tok.date = tkDate8, d
				return tok
			}
		case qtyLike.MatchString(f), commaDecimal.MatchString(f): // 120,00 = 소수점을 쉼표로 읽은 수량 (천 단위 쉼표 뒤는 세 자리)
			tok.kind, tok.neg = tkDec, strings.HasPrefix(f, "-")
			return tok
		case shapeAmount.MatchString(f):
			tok.kind, tok.neg = tkAmt, strings.HasPrefix(f, "-")
			return tok
		}
	}
	if m := shapeShortDate.FindStringSubmatch(s); m != nil {
		if d, ok := validDate("20"+m[1], m[2], m[3]); ok {
			tok.kind, tok.date = tkDate, d
			return tok
		}
	}
	// 코드: 영문·숫자·하이픈(+ 숫자로 잘못 읽히는 글자)이고 숫자가 있다
	digits, real, letters, odd := 0, 0, 0, 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
			real++
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			letters++
		case r == '-' || r == '/' || r == '\\':
		default:
			if _, ok := digitLookalike[r]; ok {
				digits++
			} else {
				odd++
			}
		}
	}
	n := len([]rune(s))
	if odd == 0 && real > 0 && n >= 3 && (letters > 0 || digits >= 4) {
		tok.kind, tok.digits = tkCode, letters == 0
		return tok
	}
	for _, r := range s {
		if unicode.IsLetter(r) {
			tok.kind = tkText
			return tok
		}
	}
	return tok
}

func isNumericShape(k tokKind) bool {
	return k == tkDate || k == tkDate8 || k == tkDec || k == tkAmt
}

// isNumericPiece : 숫자 칸의 조각 (숫자·기호·숫자로 읽히는 글자뿐)
func isNumericPiece(t lotTok) bool {
	if isNumericShape(t.kind) || (t.kind == tkCode && t.digits) {
		return true
	}
	s := strings.TrimSpace(t.t)
	if s == "-" {
		return true
	}
	_, ok := numericForm(s)
	return ok
}

// rowTokens : 한 줄의 OCR 단어(왼쪽→오른쪽)를 칸으로 묶는다.
//   - 숫자 조각: 붙이면 날짜·수량·금액 모양이 될 때만 (2026-1 + 0-29). 이미 온전한 두 칸(2.00, 19,200)은 붙이지 않는다.
//   - 글자: 한 칸 띄어 쓴 도매 이름 ('(주)' + '가람도매'). 구분·과세 낱말은 붙이지 않는다.
func rowTokens(line []ocrBox, medH float64) []lotTok {
	var out []lotTok
	for _, b := range line {
		cur := classifyTok(b.t)
		cur.x0, cur.x1 = b.x0, b.x1
		if n := len(out); n > 0 {
			prev := out[n-1]
			gap := b.x0 - prev.x1
			merge := false
			switch {
			case prev.kind == tkText && cur.kind == tkText:
				merge = gap < medH*1.3
			case isNumericPiece(prev) && isNumericPiece(cur) && gap < medH*0.9:
				m := classifyTok(prev.t + cur.t)
				both := isNumericShape(prev.kind) && isNumericShape(cur.kind)
				if gap < medH*0.45 {
					merge = isNumericShape(m.kind) || !both
				} else {
					merge = isNumericShape(m.kind) && !both
				}
			}
			if merge {
				sep := ""
				if prev.kind == tkText {
					sep = " "
				}
				m := classifyTok(prev.t + sep + cur.t)
				m.x0, m.x1 = prev.x0, math.Max(prev.x1, b.x1)
				out[n-1] = m
				continue
			}
		}
		out = append(out, cur)
	}
	return out
}

func hasDateTok(toks []lotTok) bool {
	for _, t := range toks {
		if t.kind == tkDate || t.kind == tkDate8 {
			return true
		}
	}
	return false
}

// lotCluster : 여러 줄에서 가로 위치가 겹치는 칸들 = 한 열
type lotCluster struct {
	toks   []lotTok
	n      [tkKinds]int
	typ    tokKind
	x0, x1 float64 // 다수 종류 칸들의 범위
	future int     // 오늘 이후 날짜 수
	past   int
	digits int // 숫자뿐인 코드 수
	short  int // 두 글자 이하 글자 칸 수
	hits   int // 등록된 거래처와 맞은 글자 칸 수
	role   string
}

func (c *lotCluster) cx() float64    { return (c.x0 + c.x1) / 2 }
func (c *lotCluster) share() float64 { return float64(c.n[c.typ]) / float64(len(c.toks)) }

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// clusterLotCols : 날짜가 있는 줄들의 칸을 열로 모은다 (가로 범위가 절반 이상 겹치면 같은 열).
// 숫자는 오른쪽, 글자는 왼쪽 정렬이라 가운데가 아니라 범위로 비교한다. 한 칸이 유난히 넓어도 열이 번지지 않게
// 열의 범위는 가운데값(왼쪽 끝·오른쪽 끝)으로 잡는다.
func clusterLotCols(rows [][]lotTok, sup func(string) (int64, string)) []*lotCluster {
	type acc struct {
		c        *lotCluster
		x0s, x1s []float64
		m0, m1   float64
	}
	var accs []*acc
	for _, row := range rows {
		for _, t := range row {
			if t.x1 <= t.x0 {
				continue
			}
			var best *acc
			bestOv := 0.0
			for _, a := range accs {
				ov := math.Min(t.x1, a.m1) - math.Max(t.x0, a.m0)
				if ov >= 0.5*math.Min(t.x1-t.x0, a.m1-a.m0) && ov > bestOv {
					best, bestOv = a, ov
				}
			}
			if best == nil {
				best = &acc{c: &lotCluster{}}
				accs = append(accs, best)
			}
			best.c.toks = append(best.c.toks, t)
			best.x0s, best.x1s = append(best.x0s, t.x0), append(best.x1s, t.x1)
			best.m0, best.m1 = median(best.x0s), median(best.x1s)
		}
	}
	today := time.Now().Format("2006-01-02")
	out := make([]*lotCluster, 0, len(accs))
	for _, a := range accs {
		c := a.c
		for _, t := range c.toks {
			c.n[t.kind]++
		}
		// 다수결 (같으면 앞의 종류)
		for _, k := range []tokKind{tkDate, tkDate8, tkKind, tkTax, tkDec, tkAmt, tkCode, tkText} {
			if c.n[k] > c.n[c.typ] || (c.typ == tkOther && c.n[k] > 0 && c.n[k] >= c.n[tkOther]) {
				c.typ = k
			}
		}
		c.x0, c.x1 = math.Inf(1), math.Inf(-1)
		for _, t := range c.toks {
			if t.kind == c.typ {
				c.x0, c.x1 = math.Min(c.x0, t.x0), math.Max(c.x1, t.x1)
			}
			switch t.kind {
			case tkDate, tkDate8:
				if t.date > today {
					c.future++
				} else {
					c.past++
				}
			case tkCode:
				if t.digits {
					c.digits++
				}
			case tkText:
				if len([]rune(strings.Join(strings.Fields(t.t), ""))) <= 2 {
					c.short++
				}
				if sup != nil && c.n[tkText]*2 >= len(c.toks) {
					if id, _ := sup(t.t); id != 0 {
						c.hits++
					}
				}
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].cx() < out[j].cx() })
	return out
}

// assignLotRoles : 열(왼쪽→오른쪽)에 역할을 준다. weak 는 근거가 약해 그 칸을 '확인 필요'로 둘 역할.
func assignLotRoles(cs []*lotCluster) (weak map[string]bool) {
	weak = map[string]bool{}
	isDate := func(c *lotCluster) bool { return c.typ == tkDate || c.typ == tkDate8 }
	var dates []int
	for i, c := range cs {
		if isDate(c) {
			dates = append(dates, i)
		}
	}
	// 사입일자: 지난 날짜가 모인 가장 왼쪽 날짜 열 (구분 기호가 있는 날짜 열이 먼저)
	di := -1
	for _, i := range dates {
		if cs[i].typ == tkDate && cs[i].future <= cs[i].past {
			di = i
			break
		}
	}
	if di < 0 && len(dates) > 0 && cs[dates[0]].future <= cs[dates[0]].past {
		di = dates[0]
	}
	// 유효기간: 사입일자 오른쪽의 미래 날짜 열. 없으면(유효기간이 지난 약) 오른쪽의 첫 날짜 열 — 확인 필요.
	ei := -1
	for k := len(dates) - 1; k >= 0; k-- {
		if i := dates[k]; i > di && cs[i].future > cs[i].past {
			ei = i
			break
		}
	}
	if ei < 0 {
		for _, i := range dates {
			if i > di {
				ei, weak["expiry"] = i, true
				break
			}
		}
	}
	if di >= 0 {
		cs[di].role = "date"
		weak["date"] = cs[di].share() < 0.6
	}
	if ei >= 0 {
		cs[ei].role = "expiry"
		weak["expiry"] = weak["expiry"] || cs[ei].share() < 0.6
	}
	ki := -1
	for i, c := range cs {
		if c.typ == tkKind && c.role == "" {
			if ki < 0 {
				c.role, ki = "kind", i
			} else {
				c.role = "skip"
			}
		}
	}
	// 수량: 소수 둘째 자리 열의 처음 = 수량, 끝 = 총수량 (이팜). 금액·과세 열은 읽지 않되 자리는 잡아 둔다.
	var decs []int
	for i, c := range cs {
		switch {
		case c.role != "":
		case c.typ == tkDec && i > di:
			decs = append(decs, i)
		case c.typ == tkAmt || c.typ == tkTax || c.typ == tkDec:
			c.role = "skip"
		}
	}
	for k, i := range decs {
		switch {
		case k == 0:
			cs[i].role = "qty"
		case k == len(decs)-1:
			cs[i].role = "total"
		default:
			cs[i].role = "skip"
		}
	}
	// 제조번호: 유효기간 바로 오른쪽(없으면 왼쪽)의 코드 열. 날짜처럼 생긴 제조번호(20250101) 열도 후보.
	isCode := func(i int) bool {
		return cs[i].role == "" && (cs[i].typ == tkCode || cs[i].typ == tkDate8)
	}
	li := -1
	if ei >= 0 {
		for i := ei + 1; i < len(cs) && li < 0; i++ {
			if isCode(i) {
				li = i
			}
		}
		for i := ei - 1; i > di && li < 0; i-- {
			if isCode(i) {
				li = i
			}
		}
	} else {
		// 유효기간 열이 없음: 날짜 오른쪽의 가장 오른쪽 코드 열 (쉼표 빠진 금액일 수도 있어 확인 필요)
		for i := len(cs) - 1; i > di && li < 0; i-- {
			if isCode(i) {
				li, weak["lot"] = i, true
			}
		}
	}
	if li >= 0 {
		cs[li].role = "lot"
		weak["lot"] = weak["lot"] || cs[li].share() < 0.5
	}
	// 도매: 등록된 거래처 이름이 가장 많이 맞는 글자 열, 없으면 날짜 오른쪽의 첫 글자 열.
	// 구분 낱말을 못 알아본 경우(두 글자뿐인 글자 열 + 바로 옆에 또 글자 열)는 구분으로 본다.
	isText := func(i int) bool { return i >= 0 && i < len(cs) && cs[i].role == "" && cs[i].typ == tkText }
	allShort := func(i int) bool { return cs[i].short == cs[i].n[tkText] }
	si := -1
	for i := range cs {
		if isText(i) && cs[i].hits > 0 && (si < 0 || cs[i].hits > cs[si].hits) {
			si = i
		}
	}
	if si < 0 {
		for i := di + 1; i < len(cs) && si < 0; i++ {
			if isText(i) {
				si = i
			}
		}
		if si >= 0 && ki < 0 && allShort(si) && isText(si+1) {
			cs[si].role, ki = "kind", si
			si++
		}
		for i := 0; i < len(cs) && si < 0; i++ {
			if isText(i) {
				si = i
			}
		}
	}
	if si >= 0 {
		cs[si].role = "supplier"
		for i := si + 1; isText(i); i++ {
			cs[i].role = "supplier"
		}
		for i := si - 1; i > di && isText(i); i-- {
			if allShort(i) && ki < 0 {
				cs[i].role, ki = "kind", i
				break
			}
			cs[i].role = "supplier"
		}
	}
	return weak
}

// colModel : 한 표의 열 배치 (머리에서 온 열 + 내용으로 추정한 열)
type colModel struct {
	cols      []ocrCol
	header    bool
	has       map[string]bool
	weak      map[string]bool // 근거가 약한 역할 → 그 칸은 '확인 필요'
	decimals  bool            // 수량을 소수 둘째 자리로 적는 표 (소수 열이 있음)
	lotDigits bool            // 제조번호 열이 대부분 숫자뿐 → 영문으로 읽힌 0·1 (O·I) 을 되돌린다
}

// buildColModel : 머리 열(없을 수 있음)과 날짜가 있는 줄들의 칸으로 열 배치를 만든다.
//   - 머리가 없으면 모든 열을 내용으로 정한다.
//   - 머리가 있으면 머리 열은 그대로 두고, 머리에 없는 역할(반쯤 잘린 머리, OCR 이 놓친 '수량' 등)만 더한다.
func buildColModel(hdr []ocrCol, data [][]lotTok, medH float64, sup func(string) (int64, string)) *colModel {
	m := &colModel{header: len(hdr) > 0, has: map[string]bool{}, weak: map[string]bool{}}
	cs := clusterLotCols(data, sup)
	weak := assignLotRoles(cs)
	cols := append([]ocrCol(nil), hdr...)
	for _, h := range hdr {
		m.has[h.key] = true
	}
	// 머리 열마다 가장 가까운 내용 열을 짝짓는다 (머리는 가운데, 숫자는 오른쪽 정렬이라 조금 떨어져도 같은 열)
	claimed := make([]bool, len(cs))
	for hi, h := range cols {
		best, bd := -1, medH*3
		for i, c := range cs {
			d := math.Max(0, math.Max(c.x0-h.x, h.x-c.x1))
			if d < bd {
				best, bd = i, d
			}
		}
		if best >= 0 {
			claimed[best] = true
			if cols[hi].x1 == 0 {
				cols[hi].x0, cols[hi].x1 = cs[best].x0, cs[best].x1
			}
		}
	}
	for i, c := range cs {
		if c.role == "lot" && c.n[tkCode] > 0 && c.digits*2 > c.n[tkCode] {
			m.lotDigits = true
		}
		if c.typ == tkDec {
			m.decimals = true
		}
		if claimed[i] {
			continue
		}
		role := c.role
		if role == "" {
			if m.header {
				continue
			}
			role = "skip"
		}
		if role != "skip" {
			if m.has[role] {
				continue // 머리에 이미 있는 역할은 머리 위치를 믿는다
			}
			if weak[role] {
				m.weak[role] = true
			}
		}
		cols = append(cols, ocrCol{key: role, x: c.cx(), x0: c.x0, x1: c.x1, inferred: true})
	}
	for _, c := range cols {
		m.has[c.key] = true
	}
	sort.SliceStable(cols, func(i, j int) bool { return cols[i].x < cols[j].x })
	m.cols = cols
	return m
}

// colEdge : i 번째 열과 다음 열의 경계
func (m *colModel) colEdge(i int, medH float64) float64 {
	a, b := m.cols[i], m.cols[i+1]
	edge := (a.x + b.x) / 2
	switch {
	case (a.inferred || b.inferred) && a.x1 > 0 && b.x1 > 0 && a.x1 < b.x0:
		edge = (a.x1 + b.x0) / 2 // 내용으로 잡은 열: 두 열 사이 빈 곳의 가운데
	case a.key == "kind" && !a.inferred:
		// '구분' 열은 좁고 다음의 거래처는 왼쪽 정렬이라, 구분 머리 바로 오른쪽부터는 거래처로 본다
		edge = math.Min(edge, a.x+medH*1.3)
	}
	return edge
}

// digitsOnlyLot : 숫자뿐인 제조번호 열에서 영문으로 읽힌 0·1 (O·I·L) 을 되돌린다 — 그렇게 해서 전부 숫자가 될 때만.
// 다른 영문이 있으면 진짜 영문 제조번호일 수 있어 그대로 둔다. pure 는 (되돌린 뒤) 숫자뿐인지.
func digitsOnlyLot(lot string) (fixed string, pure bool) {
	var b strings.Builder
	for _, r := range lot {
		switch {
		case r == 'O':
			b.WriteByte('0')
		case r == 'I' || r == 'L':
			b.WriteByte('1')
		case (r >= '0' && r <= '9') || r == '-':
			b.WriteRune(r)
		default:
			return lot, false
		}
	}
	return b.String(), true
}
