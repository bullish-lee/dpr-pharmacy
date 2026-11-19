package domain

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// 약품코드 규칙
//   - 9자리: 건강보험 EDI 코드(= 고시 '제품코드'). 고시와 매칭하는 키.
//   - 8자리: 엑셀이 숫자로 저장하면서 앞자리 0 이 사라진 EDI 코드 (12345670 → 012345670).
//   - 7자리: 이팜 내부코드(비급여·위생용품, 예: 2000461). 고시 매칭 대상 아님.
const EDICodeLen = 9

// NormalizeProductCode 는 엑셀 셀 문자열을 약품코드로 정규화한다. 해석 불가면 "".
func NormalizeProductCode(raw string) string {
	s := strings.TrimSpace(norm.NFKC.String(raw))
	if s == "" {
		return ""
	}
	// "698765430.0", "6.987e8" 같은 숫자 표기 처리
	if strings.ContainsAny(s, ".eE") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f <= 0 || f != math.Trunc(f) {
			return ""
		}
		s = strconv.FormatInt(int64(f), 10)
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	switch {
	case len(s) == EDICodeLen:
		return s
	case len(s) == EDICodeLen-1:
		return "0" + s
	case len(s) >= 4 && len(s) <= 13:
		return s
	}
	return ""
}

// IsEDICode 는 고시와 매칭 가능한 9자리 보험코드인지.
func IsEDICode(code string) bool {
	if len(code) != EDICodeLen {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var (
	corpMarkers = regexp.MustCompile(`\(주\)|\(유\)|\(재\)|\(사\)|㈜|주식회사|유한책임회사|유한회사`)
	spaces      = regexp.MustCompile(`\s+`)
)

// NormalizeSupplierName : '(주) 가나약품' / '가나약품(주)' / '가나약품 주식회사' → '가나약품'
func NormalizeSupplierName(raw string) string {
	s := norm.NFKC.String(raw)
	s = corpMarkers.ReplaceAllString(s, "")
	return strings.TrimSpace(spaces.ReplaceAllString(s, ""))
}

// PurchaseKind : 이팜 사입현황 '구분' → 내부 종류.
// 수량 부호는 원본을 그대로 쓴다(반품·폐기·차용출고는 원본이 음수).
func PurchaseKind(raw string) string {
	switch strings.ReplaceAll(strings.TrimSpace(raw), " ", "") {
	case "사입", "매입", "입고", "구입":
		return "purchase"
	case "반품", "사입반품", "매입반품", "반품출고", "반출":
		return "return"
	case "폐기":
		return "disposal"
	case "재고조정":
		return "adjust"
	case "차용입고":
		return "borrow_in"
	case "차용출고":
		return "borrow_out"
	}
	return "other"
}

// GuessSupplierKind : 도매 보상 대상이 될 수 없는 거래처(약국간 거래, 재고정리, 타 약국) 분류.
func GuessSupplierKind(normalized string) string {
	for _, hint := range []string{"약국간거래", "재고정리", "약국"} {
		if strings.Contains(normalized, hint) {
			return "other"
		}
	}
	return "wholesaler"
}

// ── 약품명 매칭 키 ────────────────────────────────────────────────

var (
	unitAliases = []struct {
		re   *regexp.Regexp
		repl string
	}{
		{regexp.MustCompile(`마이크로그램|마이크로그람|㎍|μg|µg`), "mcg"},
		{regexp.MustCompile(`밀리그램|밀리그람|㎎`), "mg"},
		{regexp.MustCompile(`밀리리터|㎖`), "ml"},
		{regexp.MustCompile(`그램|그람`), "g"},
		{regexp.MustCompile(`캅셀|캅슐|켑슐|캡셀`), "캡슐"},
	}
	parenGroups = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)
)

// ProductNameKey 는 출처가 다른 약품명을 비교하기 위한 정규화 키.
//
//	이팜: '누리정500마이크로그램(누리성분)_(0.5mg/1정)' → '누리정500mcg'
//	도매: '누리정500mcg(PTP) 30T 가나제약'                    → '누리정500mcg' (firstToken)
func ProductNameKey(name string, firstToken bool) string {
	s := strings.ToLower(strings.TrimSpace(norm.NFKC.String(name)))
	s = strings.TrimLeft(s, "[")
	if i := strings.Index(s, "_("); i >= 0 {
		s = s[:i]
	}
	s = parenGroups.ReplaceAllString(s, " ")
	for _, a := range unitAliases {
		s = a.re.ReplaceAllString(s, a.repl)
	}
	s = strings.NewReplacer("[", " ", "]", " ").Replace(s)
	if firstToken {
		if f := strings.Fields(s); len(f) > 0 {
			s = f[0]
		} else {
			s = ""
		}
	}
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) || (r >= 'a' && r <= 'z') || (r >= '가' && r <= '힣') || r == '.' || r == '/' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var packSpec = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(t|c|p|s|정|캡슐|ea|개|포|관|병|매|ml|g|pen|v)\b`)

// packMul : '10S*5EA', '20T*16EA', '10ML*30P', '1G*50P' 처럼 곱으로 적은 포장
var packMul = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(t|c|s|p|정|캡슐|포|ml|g)\s*[*x×]\s*(\d+)\s*(ea|p|개|포|병|통)`)

var countUnit = map[string]bool{"t": true, "c": true, "s": true, "p": true, "정": true, "캡슐": true, "포": true}

// ParsePackSize : 도매 규격 문자열의 포장당 수량. '30T' → 30, '60C' → 60, '20P' → 20, '28S' → 28,
// '10S*5EA' → 50(좌약 낱개), '10ML*30P' → 30(포). 용량 단위(ml·g)를 곱한 경우는 용기 수만 센다.
func ParsePackSize(spec string) (float64, bool) {
	s := norm.NFKC.String(spec)
	if m := packMul.FindStringSubmatch(s); m != nil {
		a, err1 := strconv.ParseFloat(m[1], 64)
		b, err2 := strconv.ParseFloat(m[3], 64)
		if err1 == nil && err2 == nil && a > 0 && b > 0 {
			if countUnit[strings.ToLower(m[2])] {
				return a * b, true
			}
			return b, true
		}
	}
	m := packSpec.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	return f, err == nil && f > 0
}

// Similarity 는 룬 단위 레벤슈타인 거리 기반 유사도 (0~1).
func Similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 && len(rb) == 0 {
		return 1
	}
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	maxLen := max(len(ra), len(rb))
	return 1 - float64(prev[len(rb)])/float64(maxLen)
}
