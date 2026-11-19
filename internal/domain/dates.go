package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 날짜는 DB·JSON 모두 'YYYY-MM-DD' 문자열로 다루고, 계산할 때만 time.Time(UTC 자정)으로 바꾼다.
const DateLayout = "2006-01-02"

func ParseDate(s string) (time.Time, error) {
	return time.Parse(DateLayout, strings.TrimSpace(s))
}

func MustDate(s string) time.Time {
	t, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return t
}

func FormatDate(t time.Time) string { return t.Format(DateLayout) }

// ParseFlexibleDate 는 엑셀/이팜에서 오는 다양한 날짜 표기를 'YYYY-MM-DD' 로 바꾼다.
//
//	"20260302" | "2026-03-02" | "2026.03.02" | "2026/03/02" | 엑셀 일련번호("46327") | "2026-11-01 00:00:00"
func ParseFlexibleDate(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", false
	}
	if i := strings.IndexAny(s, " T"); i > 0 {
		s = s[:i]
	}
	digits := strings.NewReplacer("-", "", ".", "", "/", "").Replace(s)
	if len(digits) == 8 {
		if t, err := time.Parse("20060102", digits); err == nil {
			return FormatDate(t), true
		}
	}
	// 엑셀 날짜 일련번호 (1900 시스템). 2000~2100년 범위만 허용해 오인 방지.
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 36526 && f < 73051 {
		base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
		return FormatDate(base.AddDate(0, 0, int(f))), true
	}
	return "", false
}

// AddMonths 는 월을 더하되 말일을 넘지 않게 자른다 (1/31 + 1개월 = 2/28).
func AddMonths(t time.Time, months int) time.Time {
	y, m, d := t.Date()
	first := time.Date(y, m+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, time.UTC)
}

// ShortKoreanDate : '26.11.01 → 2026-11-01 (고시 시트명 표기)
func ShortKoreanDate(yy, mm, dd string) (string, error) {
	y, err1 := strconv.Atoi(yy)
	m, err2 := strconv.Atoi(mm)
	d, err3 := strconv.Atoi(dd)
	if err1 != nil || err2 != nil || err3 != nil {
		return "", fmt.Errorf("날짜 형식 오류: %s.%s.%s", yy, mm, dd)
	}
	t := time.Date(2000+y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Day() != d || int(t.Month()) != m {
		return "", fmt.Errorf("존재하지 않는 날짜: %s.%s.%s", yy, mm, dd)
	}
	return FormatDate(t), nil
}
