package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// GS1 바코드(의약품 일련번호 DataMatrix) 파서.
//
// 판독 결과 예 (가상 값):
//
//	(01)08809000000117(17)300615(10)31007-2(21)31007-2000123
//	01=GTIN-14, 17=유효기간(YYMMDD), 10=제조번호, 21=일련번호
//
// 입력 형태:
//  1. 괄호 표기 (화면 판독기 출력)
//  2. 스캐너 원문: ']d2' 접두 + GS(0x1D) 구분자
//  3. 키보드 스캐너가 GS 를 다른 문자로 보내는 경우: '~', '|', '^', '␝'
const gsChar = "\x1d"

var (
	gs1FixedLen = map[string]int{"00": 18, "01": 14, "02": 14, "11": 6, "13": 6, "15": 6, "16": 6, "17": 6, "20": 2}
	gs1VarMax   = map[string]int{"10": 20, "21": 20, "22": 20, "240": 30, "241": 30, "30": 8, "37": 8}
	parenAI     = regexp.MustCompile(`\((\d{2,4})\)([^(]*)`)
)

type GS1 struct {
	GTIN     string            `json:"gtin"`
	Expiry   string            `json:"expiry"` // YYYY-MM-DD, 없으면 ""
	Lot      string            `json:"lot"`
	Serial   string            `json:"serial"`
	Raw      string            `json:"raw"`
	Elements map[string]string `json:"elements"`
	Warnings []string          `json:"warnings"`
}

// EAN13 은 GTIN-14 의 앞 '0' 을 뗀 국내 표준코드(880...).
func (g GS1) EAN13() string {
	if len(g.GTIN) == 14 && strings.HasPrefix(g.GTIN, "0") {
		return g.GTIN[1:]
	}
	return g.GTIN
}

// Human 은 사람이 읽는 괄호 표기.
func (g GS1) Human() string {
	var b strings.Builder
	if g.GTIN != "" {
		b.WriteString("(01)" + g.GTIN)
	}
	if e := g.Elements["17"]; e != "" {
		b.WriteString("(17)" + e)
	}
	if g.Lot != "" {
		b.WriteString("(10)" + g.Lot)
	}
	if g.Serial != "" {
		b.WriteString("(21)" + g.Serial)
	}
	return b.String()
}

// ParseYYMMDD : GS1 날짜. DD=00 이면 해당 월 말일.
func ParseYYMMDD(s string) (time.Time, bool) {
	if len(s) != 6 {
		return time.Time{}, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return time.Time{}, false
		}
	}
	yy := int(s[0]-'0')*10 + int(s[1]-'0')
	mm := int(s[2]-'0')*10 + int(s[3]-'0')
	dd := int(s[4]-'0')*10 + int(s[5]-'0')
	if mm < 1 || mm > 12 {
		return time.Time{}, false
	}
	year := 2000 + yy
	if yy >= 70 {
		year = 1900 + yy
	}
	if dd == 0 {
		t := time.Date(year, time.Month(mm)+1, 0, 0, 0, 0, 0, time.UTC)
		return t, true
	}
	t := time.Date(year, time.Month(mm), dd, 0, 0, 0, 0, time.UTC)
	if t.Day() != dd {
		return time.Time{}, false
	}
	return t, true
}

func aiAt(s string, pos int) string {
	for size := 2; size <= 4; size++ {
		if pos+size > len(s) {
			break
		}
		ai := s[pos : pos+size]
		if _, ok := gs1FixedLen[ai]; ok {
			return ai
		}
		if _, ok := gs1VarMax[ai]; ok {
			return ai
		}
	}
	return ""
}

func parseRawGS1(s string) (map[string]string, []string) {
	el := map[string]string{}
	var warns []string
	pos := 0
	for pos < len(s) {
		if s[pos] == 0x1d {
			pos++
			continue
		}
		ai := aiAt(s, pos)
		if ai == "" {
			end := min(pos+6, len(s))
			warns = append(warns, fmt.Sprintf("해석할 수 없는 위치 %d: %q", pos, s[pos:end]))
			break
		}
		pos += len(ai)
		if n, ok := gs1FixedLen[ai]; ok {
			end := min(pos+n, len(s))
			el[ai] = s[pos:end]
			pos = end
			continue
		}
		end := strings.IndexByte(s[pos:], 0x1d)
		if end < 0 {
			end = len(s)
			if ai == "10" && len(s)-pos > gs1VarMax[ai] {
				warns = append(warns, "GS 구분자 없음: 제조번호/일련번호 경계 확인 필요")
			}
		} else {
			end += pos
		}
		val := s[pos:end]
		if len(val) > gs1VarMax[ai] {
			val = val[:gs1VarMax[ai]]
		}
		el[ai] = val
		pos = end
	}
	return el, warns
}

// ParseGS1 은 바코드 문자열을 해석한다. GTIN(01) 이 없으면 ok=false.
func ParseGS1(raw string) (GS1, bool) {
	s := strings.TrimSpace(raw)
	for _, p := range []string{"]d2", "]C1", "]Q3", "]e0"} {
		s = strings.TrimPrefix(s, p)
	}
	for _, sep := range []string{"~", "|", "^", "␝"} {
		s = strings.ReplaceAll(s, sep, gsChar)
	}
	var el map[string]string
	var warns []string
	if strings.HasPrefix(s, "(") {
		el = map[string]string{}
		for _, m := range parenAI.FindAllStringSubmatch(s, -1) {
			el[m[1]] = strings.TrimSpace(strings.ReplaceAll(m[2], gsChar, ""))
		}
	} else {
		el, warns = parseRawGS1(s)
	}
	gtin, ok := el["01"]
	if !ok {
		return GS1{Raw: raw}, false
	}
	g := GS1{GTIN: gtin, Lot: el["10"], Serial: el["21"], Raw: raw, Elements: el, Warnings: warns}
	if len(gtin) != 14 || !GTINCheckOK(gtin) {
		g.Warnings = append(g.Warnings, fmt.Sprintf("GTIN 검증 실패: %q", gtin))
	}
	if e, ok := el["17"]; ok {
		if t, ok := ParseYYMMDD(e); ok {
			g.Expiry = t.Format("2006-01-02")
		} else {
			g.Warnings = append(g.Warnings, fmt.Sprintf("유효기간 형식 이상: %q", e))
		}
	}
	return g, true
}

// GTINCheckOK : GS1 체크디지트 검증.
func GTINCheckOK(code string) bool {
	if l := len(code); l != 8 && l != 12 && l != 13 && l != 14 {
		return false
	}
	sum := 0
	for i := len(code) - 2; i >= 0; i-- {
		c := code[i]
		if c < '0' || c > '9' {
			return false
		}
		d := int(c - '0')
		if (len(code)-2-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	last := code[len(code)-1]
	if last < '0' || last > '9' {
		return false
	}
	return (10-sum%10)%10 == int(last-'0')
}

// EDIPrefixFromGTIN : 국내 표준코드(880+업체4+품목4+포장1+검증1)에서 EDI 코드 앞 8자리 추출.
//
//	예(가상 값): GTIN 08809876543213 → '98765432' ↔ 보험코드 987654320
//	             GTIN 08809876544012 → '98765440' ↔ 보험코드 987654400
//
// 후보가 여러 개일 수 있으므로 '추천'일 뿐이며, 사용자가 확인하면 gtin_map 에 학습된다.
func EDIPrefixFromGTIN(gtin string) string {
	ean := gtin
	if len(gtin) == 14 && strings.HasPrefix(gtin, "0") {
		ean = gtin[1:]
	}
	if len(ean) != 13 || !strings.HasPrefix(ean, "880") {
		return ""
	}
	for _, r := range ean {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return ean[3:11]
}
