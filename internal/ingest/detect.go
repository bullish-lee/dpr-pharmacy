package ingest

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/sheet"
)

const headerScanRows = 20

// Mapping 은 필드 키 → 열 번호(0부터). 사용자가 지정하거나 자동 매핑으로 채워진다.
type Mapping map[string]int

// Table 은 헤더가 확정된 시트 1개.
type Table struct {
	Sheet     string     `json:"sheet"`
	HeaderRow int        `json:"headerRow"` // 0부터
	Headers   []string   `json:"headers"`
	Mapping   Mapping    `json:"mapping"`
	Missing   []string   `json:"missing"` // 못 찾은 필수 필드 키
	Signature string     `json:"signature"`
	RangeFrom string     `json:"rangeFrom"` // 제목 행의 'YYYYMMDD ~ YYYYMMDD'
	RangeTo   string     `json:"rangeTo"`
	Title     string     `json:"title"`
	Rows      [][]string `json:"-"` // 헤더 아래 데이터 행
}

func (t *Table) Get(row []string, key string) string {
	idx, ok := t.Mapping[key]
	if !ok {
		return ""
	}
	return sheet.Cell(row, idx)
}

func (t *Table) Has(key string) bool { _, ok := t.Mapping[key]; return ok }

// autoMap 은 헤더 행 하나를 스키마에 맞춰 매핑한다. (정확 일치 → 부분 일치 순)
func autoMap(schema Schema, header []string) Mapping {
	norm := make([]string, len(header))
	for i, h := range header {
		norm[i] = NormalizeHeader(h)
	}
	m := Mapping{}
	used := map[int]bool{}
	// 1차: 동의어 정확 일치 (동의어 순서 = 우선순위)
	for _, f := range schema.Fields {
		for _, syn := range f.Synonyms {
			s := NormalizeHeader(syn)
			found := -1
			for i, h := range norm {
				if !used[i] && h != "" && h == s {
					found = i
					break
				}
			}
			if found >= 0 {
				m[f.Key] = found
				used[found] = true
				break
			}
		}
	}
	// 2차: 남은 필드는 '헤더가 동의어를 포함' (3글자 이상 동의어만, 오매칭 방지)
	for _, f := range schema.Fields {
		if _, ok := m[f.Key]; ok {
			continue
		}
		for _, syn := range f.Synonyms {
			s := NormalizeHeader(syn)
			if len([]rune(s)) < 3 {
				continue
			}
			found := -1
			for i, h := range norm {
				if !used[i] && h != "" && strings.Contains(h, s) {
					found = i
					break
				}
			}
			if found >= 0 {
				m[f.Key] = found
				used[found] = true
				break
			}
		}
	}
	return m
}

func missingRequired(schema Schema, m Mapping) []string {
	var miss []string
	for _, f := range schema.Fields {
		if _, ok := m[f.Key]; f.Required && !ok {
			miss = append(miss, f.Key)
		}
	}
	return miss
}

// HeaderSignature 는 양식 식별용 해시 (헤더 이름 집합).
func HeaderSignature(header []string) string {
	var parts []string
	for _, h := range header {
		if n := NormalizeHeader(h); n != "" {
			parts = append(parts, n)
		}
	}
	sum := sha1.Sum([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:8])
}

var rangeRe = regexp.MustCompile(`(\d{4}[-./]?\d{2}[-./]?\d{2})\s*~\s*(\d{4}[-./]?\d{2}[-./]?\d{2})`)

func findTitleRange(rows [][]string, headerRow int) (title, from, to string) {
	for i := 0; i < headerRow && i < len(rows); i++ {
		for _, c := range rows[i] {
			if m := rangeRe.FindStringSubmatch(c); m != nil {
				f, ok1 := domain.ParseFlexibleDate(m[1])
				t, ok2 := domain.ParseFlexibleDate(m[2])
				if ok1 && ok2 {
					return c, f, t
				}
			}
		}
	}
	return "", "", ""
}

// DetectTable 은 시트 하나에서 헤더 행을 찾고 매핑한다.
// override 가 있으면 그 매핑(과 헤더 행)을 그대로 쓴다.
func DetectTable(sh sheet.Sheet, schema Schema, override *Override) *Table {
	best, bestScore := -1, 0
	var bestMap Mapping
	if override != nil && override.HeaderRow >= 0 && override.HeaderRow < len(sh.Rows) {
		best = override.HeaderRow
		bestMap = Mapping{}
		for k, v := range override.Mapping {
			bestMap[k] = v
		}
	} else {
		for i := 0; i < len(sh.Rows) && i < headerScanRows; i++ {
			m := autoMap(schema, sh.Rows[i])
			score := len(m) * 10
			for _, f := range schema.Fields {
				if _, ok := m[f.Key]; ok && f.Required {
					score += 5
				}
			}
			if score > bestScore {
				best, bestScore, bestMap = i, score, m
			}
		}
	}
	if best < 0 {
		return &Table{Sheet: sh.Name, HeaderRow: -1, Missing: missingRequired(schema, Mapping{})}
	}
	header := sh.Rows[best]
	t := &Table{Sheet: sh.Name, HeaderRow: best, Headers: header, Mapping: bestMap,
		Missing: missingRequired(schema, bestMap), Signature: HeaderSignature(header), Rows: sh.Rows[best+1:]}
	t.Title, t.RangeFrom, t.RangeTo = findTitleRange(sh.Rows, best)
	return t
}

// Override 는 사용자가 화면에서 지정한 시트·헤더 행·열 매핑.
type Override struct {
	Sheet     string  `json:"sheet"`
	HeaderRow int     `json:"headerRow"`
	Mapping   Mapping `json:"mapping"`
}

// Preview 는 매핑 화면에 보여줄 시트 요약.
type Preview struct {
	Sheet     string     `json:"sheet"`
	HeaderRow int        `json:"headerRow"`
	Headers   []string   `json:"headers"`
	Mapping   Mapping    `json:"mapping"`
	Missing   []string   `json:"missing"`
	Sample    [][]string `json:"sample"` // 헤더 위아래 몇 행
	RowCount  int        `json:"rowCount"`
	Signature string     `json:"signature"`
}

func MakePreview(sh sheet.Sheet, t *Table) Preview {
	p := Preview{Sheet: sh.Name, HeaderRow: t.HeaderRow, Headers: t.Headers, Mapping: t.Mapping,
		Missing: t.Missing, RowCount: len(sh.Rows), Signature: t.Signature}
	start := 0
	if t.HeaderRow > 3 {
		start = t.HeaderRow - 3
	}
	for i := start; i < len(sh.Rows) && i < start+12; i++ {
		row := sh.Rows[i]
		if len(row) > 30 {
			row = row[:30]
		}
		p.Sample = append(p.Sample, row)
	}
	return p
}
