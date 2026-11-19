package ingest

import (
	"fmt"
	"regexp"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/sheet"
)

// 복지부 「약제 급여 목록 및 급여 상한금액표」 일부개정 고시의 '별표1 목록표' 엑셀 파서.
//
// 실제 구조 (제2026-219호):
//
//	별지1_신설('26.11.01.시행)            ← 신설, 시행일
//	별지3_변경('26.11.01.시행)            ← 변경 후 상한금액만 있음
//	별지5_삭제('26.11.01.시행)~'27.04.30  ← 삭제, 경과조치(유예) 종료일
//	별지6_삭제('26.11.01.시행)~유예기간없음
//	변경대비표                            ← 변경 전/후, 비고(상한금액/상한금액(인상)/제품명/별도합의금액), 시행일
//	붙임1(…)                              ← 신설 품목의 향후 상한금액 조정(조정후, 시행일)
//
// 시트명·열 순서가 바뀌어도 동작하도록 '키워드 + 헤더 이름'으로 해석한다.

type PriceChange struct {
	Code           string  `json:"code"`
	Name           string  `json:"name"`
	Company        string  `json:"company"`
	Spec           string  `json:"spec"`
	Unit           string  `json:"unit"`
	IngredientCode string  `json:"ingredientCode"`
	Ingredient     string  `json:"ingredient"`
	Category       string  `json:"category"`
	Route          string  `json:"route"`
	Kind           string  `json:"kind"`   // new | change | delete
	Detail         string  `json:"detail"` // new | delete | price_down | price_up | same | agreed_price | name_change | unknown | other
	PriceBefore    float64 `json:"priceBefore"`
	PriceAfter     float64 `json:"priceAfter"`
	EffectiveDate  string  `json:"effectiveDate"`
	GraceUntil     string  `json:"graceUntil"`
	Flexible       bool    `json:"flexible"` // 약가유연계약 (표시가 ≠ 실제가)
	Sheet          string  `json:"sheet"`
	Note           string  `json:"note"`
}

type NoticeSheetInfo struct {
	Sheet         string `json:"sheet"`
	Role          string `json:"role"` // new | change | delete | compare | scheduled | ignored
	EffectiveDate string `json:"effectiveDate"`
	GraceUntil    string `json:"graceUntil"`
	Items         int    `json:"items"`
}

type NoticeParse struct {
	NoticeNo string            `json:"noticeNo"`
	Changes  []PriceChange     `json:"changes"`
	Sheets   []NoticeSheetInfo `json:"sheets"`
	Warnings []string          `json:"warnings"`
}

var (
	reEffective = regexp.MustCompile(`'?(\d{2})\.(\d{1,2})\.(\d{1,2})\.?\s*시행`)
	reGrace     = regexp.MustCompile(`~\s*'?(\d{2})\.(\d{1,2})\.(\d{1,2})`)
	reNoticeNo  = regexp.MustCompile(`(\d{4})\s*-\s*(\d{1,4})\s*호`)
	reCategory  = regexp.MustCompile(`^\d{3}\s+\S`)
)

// NoticeNoFrom 은 파일명·제목에서 고시 번호('2026-219')를 찾는다.
func NoticeNoFrom(texts ...string) string {
	for _, t := range texts {
		if m := reNoticeNo.FindStringSubmatch(t); m != nil {
			return m[1] + "-" + m[2]
		}
	}
	return ""
}

type colIdx map[string]int

func headerIndex(rows [][]string, must ...string) (int, colIdx) {
	for i := 0; i < len(rows) && i < headerScanRows; i++ {
		idx := colIdx{}
		for j, c := range rows[i] {
			if n := NormalizeHeader(c); n != "" {
				if _, dup := idx[n]; !dup {
					idx[n] = j
				}
			}
		}
		ok := true
		for _, m := range must {
			if _, has := idx[m]; !has {
				ok = false
				break
			}
		}
		if ok {
			return i, idx
		}
	}
	return -1, nil
}

func (c colIdx) get(row []string, keys ...string) string {
	for _, k := range keys {
		if j, ok := c[k]; ok {
			return strings.TrimSpace(sheet.Cell(row, j))
		}
	}
	return ""
}

func sheetRole(name string) string {
	n := strings.ReplaceAll(name, " ", "")
	switch {
	case strings.Contains(n, "대비"):
		return "compare"
	case strings.Contains(n, "붙임"): // '붙임1(…금번신설품목…)' 처럼 '신설'을 포함할 수 있어 먼저 판별
		return "scheduled"
	case strings.Contains(n, "신설"):
		return "new"
	case strings.Contains(n, "삭제"):
		return "delete"
	case strings.Contains(n, "변경"):
		return "change"
	}
	return "ignored"
}

type compareRow struct {
	code, name, note, before, after, effective string
	beforeNum, afterNum                        float64
	hasBefore, hasAfter                        bool
}

// ParseNotice 는 목록표 시트들을 해석해 가격 변동 목록을 만든다.
func ParseNotice(sheets []sheet.Sheet, fileName string) (*NoticeParse, error) {
	res := &NoticeParse{NoticeNo: NoticeNoFrom(fileName)}
	compare := map[string][]compareRow{} // code → rows
	type sched struct {
		code, name, company, spec, unit, effective, ref string
		price                                           float64
		priceOK                                         bool
		priceText                                       string // 조정후 원문 (빈칸·'별도합의' 판단)
	}
	var scheduled []sched

	for _, sh := range sheets {
		role := sheetRole(sh.Name)
		info := NoticeSheetInfo{Sheet: sh.Name, Role: role}
		if m := reEffective.FindStringSubmatch(sh.Name); m != nil {
			info.EffectiveDate, _ = domain.ShortKoreanDate(m[1], m[2], m[3])
		}
		if role == "delete" {
			if m := reGrace.FindStringSubmatch(sh.Name); m != nil {
				info.GraceUntil, _ = domain.ShortKoreanDate(m[1], m[2], m[3])
			}
		}
		if res.NoticeNo == "" && len(sh.Rows) > 0 {
			res.NoticeNo = NoticeNoFrom(strings.Join(sh.Rows[0], " "))
		}
		switch role {
		case "new", "change", "delete":
			h, idx := headerIndex(sh.Rows, "제품코드", "상한금액")
			if h < 0 {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: 제품코드/상한금액 헤더를 찾지 못함", sh.Name))
				info.Role = "ignored"
				break
			}
			if info.EffectiveDate == "" {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: 시트명에서 시행일을 찾지 못함 (화면에서 지정 필요)", sh.Name))
			}
			category, ingredient := "", ""
			for _, row := range sh.Rows[h+1:] {
				codeCell := idx.get(row, "제품코드")
				code := domain.NormalizeProductCode(codeCell)
				first := strings.TrimSpace(sheet.Cell(row, 0))
				if !domain.IsEDICode(code) {
					if codeCell != "" && idx.get(row, "주성분코드") != "" {
						ingredient = codeCell // 성분명 행
					} else if reCategory.MatchString(first) {
						category = first
					}
					continue
				}
				price, _ := ParseNumber(idx.get(row, "상한금액"))
				note := idx.get(row, "비고")
				pc := PriceChange{Code: code, Name: idx.get(row, "제품명"), Company: idx.get(row, "업체명", "제약사명"),
					Spec: idx.get(row, "규격"), Unit: idx.get(row, "단위"), IngredientCode: idx.get(row, "주성분코드"),
					Ingredient: ingredient, Category: category, Route: idx.get(row, "투여"), Kind: role,
					EffectiveDate: info.EffectiveDate, GraceUntil: info.GraceUntil, Sheet: sh.Name, Note: note,
					Flexible: strings.Contains(note, "유연계약")}
				switch role {
				case "new":
					pc.Detail, pc.PriceAfter = "new", price
				case "delete":
					pc.Detail, pc.PriceBefore = "delete", price
				case "change":
					pc.Detail, pc.PriceAfter = "unknown", price
				}
				res.Changes = append(res.Changes, pc)
				info.Items++
			}
		case "compare":
			h, idx := headerIndex(sh.Rows, "제품코드", "변경전", "변경후")
			if h < 0 {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: 변경 전/후 헤더를 찾지 못함", sh.Name))
				info.Role = "ignored"
				break
			}
			for _, row := range sh.Rows[h+1:] {
				code := domain.NormalizeProductCode(idx.get(row, "제품코드"))
				if !domain.IsEDICode(code) {
					continue
				}
				cr := compareRow{code: code, name: idx.get(row, "제품명"), note: idx.get(row, "비고"),
					before: idx.get(row, "변경전"), after: idx.get(row, "변경후")}
				cr.effective, _ = domain.ParseFlexibleDate(idx.get(row, "시행일"))
				cr.beforeNum, cr.hasBefore = ParseNumber(cr.before)
				cr.afterNum, cr.hasAfter = ParseNumber(cr.after)
				compare[code] = append(compare[code], cr)
				info.Items++
			}
		case "scheduled":
			h, idx := headerIndex(sh.Rows, "제품코드", "조정후")
			if h < 0 {
				info.Role = "ignored"
				break
			}
			for _, row := range sh.Rows[h+1:] {
				code := domain.NormalizeProductCode(idx.get(row, "제품코드"))
				if !domain.IsEDICode(code) {
					continue
				}
				text := idx.get(row, "조정후")
				price, ok := ParseNumber(text)
				eff, _ := domain.ParseFlexibleDate(idx.get(row, "시행일"))
				scheduled = append(scheduled, sched{code: code, name: idx.get(row, "제품명"), company: idx.get(row, "제약사명", "업체명"),
					spec: idx.get(row, "규격"), unit: idx.get(row, "단위"), effective: eff, ref: idx.get(row, "구분"),
					price: price, priceOK: ok && price > 0, priceText: text})
				info.Items++
			}
		}
		res.Sheets = append(res.Sheets, info)
	}

	// 변경 품목에 변경대비표(변경 전/후·비고) 결합
	newPrice := map[string]PriceChange{}
	for i := range res.Changes {
		pc := &res.Changes[i]
		if pc.Kind == "new" {
			newPrice[pc.Code] = *pc
		}
		if pc.Kind != "change" {
			continue
		}
		rows := compare[pc.Code]
		var matched []compareRow
		for _, r := range rows {
			if r.effective == "" || pc.EffectiveDate == "" || r.effective == pc.EffectiveDate {
				matched = append(matched, r)
			}
		}
		classifyChange(pc, matched)
	}
	// 붙임: 신설 품목의 향후 상한금액 조정 → 변경(예정)으로 추가
	for _, s := range scheduled {
		pc := PriceChange{Code: s.code, Name: s.name, Company: s.company, Spec: s.spec, Unit: s.unit,
			Kind: "change", PriceAfter: s.price, EffectiveDate: s.effective, Sheet: "붙임(" + s.ref + ")",
			Note: "신설 품목 상한금액 조정 예정"}
		if base, ok := newPrice[s.code]; ok {
			pc.PriceBefore, pc.Ingredient, pc.IngredientCode, pc.Category, pc.Route = base.PriceAfter, base.Ingredient, base.IngredientCode, base.Category, base.Route
			switch {
			case !s.priceOK && strings.Contains(strings.ReplaceAll(s.priceText, " ", ""), "별도합의"):
				// 약가유연계약: 조정후 가격이 공개되지 않음 → 손실을 계산하지 않고 실제가 확인
				pc.Detail, pc.Flexible, pc.PriceAfter = "agreed_price", true, 0
				pc.Note = "약가유연계약: 실제 상한금액(별도합의)은 요양기관업무포털에서 확인 필요 — " + strings.TrimSpace(s.priceText)
			case !s.priceOK:
				// 조정후 칸이 비었거나 숫자가 아님 → 0원으로 보면 전액 손실로 잘못 계산된다
				pc.Detail, pc.PriceAfter = "unknown", 0
				pc.Note = "조정후 가격을 읽지 못함 (" + strings.TrimSpace(s.priceText) + ") — 고시 원문 확인 필요"
			case s.price < base.PriceAfter:
				pc.Detail = "price_down"
			case s.price > base.PriceAfter:
				pc.Detail = "price_up"
			default:
				pc.Detail = "same"
			}
		} else {
			pc.Detail = "unknown"
		}
		res.Changes = append(res.Changes, pc)
	}
	if len(res.Changes) == 0 {
		return res, fmt.Errorf("고시 목록표에서 품목을 찾지 못했습니다 (시트: %d개)", len(sheets))
	}
	return res, nil
}

func classifyChange(pc *PriceChange, rows []compareRow) {
	if len(rows) == 0 {
		pc.Detail = "unknown" // 변경 전 가격은 서비스 계층에서 과거 고시/사입가로 보완
		return
	}
	var notes []string
	detail := ""
	for _, r := range rows {
		note := strings.ReplaceAll(r.note, " ", "")
		switch {
		case strings.Contains(note, "제품명"):
			if detail == "" {
				detail = "name_change"
			}
			notes = append(notes, fmt.Sprintf("제품명 변경: %s → %s", r.before, r.after))
		case strings.Contains(note, "별도합의") || strings.Contains(r.after, "별도합의"):
			pc.Flexible = true
			if detail == "" || detail == "name_change" {
				detail = "agreed_price"
			}
			if r.hasBefore {
				pc.PriceBefore = r.beforeNum
			}
			notes = append(notes, "약가유연계약: 실제 상한금액(별도합의)은 요양기관업무포털에서 확인 필요 — "+r.after)
		case strings.Contains(note, "상한금액"):
			if r.hasBefore {
				pc.PriceBefore = r.beforeNum
			}
			if r.hasAfter {
				pc.PriceAfter = r.afterNum
			}
			switch {
			case strings.Contains(note, "인상") || (r.hasBefore && r.hasAfter && r.afterNum > r.beforeNum):
				detail = "price_up"
			case r.hasBefore && r.hasAfter && r.afterNum < r.beforeNum:
				detail = "price_down"
			default:
				detail = "same"
			}
		default:
			if detail == "" {
				detail = "other"
			}
			notes = append(notes, fmt.Sprintf("%s: %s → %s", r.note, r.before, r.after))
		}
	}
	if detail == "" {
		detail = "other"
	}
	pc.Detail = detail
	if len(notes) > 0 {
		if pc.Note != "" {
			notes = append([]string{pc.Note}, notes...)
		}
		pc.Note = strings.Join(notes, " / ")
	}
}
