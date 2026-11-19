package ingest

// 테스트 데이터는 모두 합성 데이터입니다. (도매명·약품명·코드·수량·단가·제조번호·날짜 모두 가상 값, 고시 표는 양식만 본뜸)

import (
	"bytes"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/korean"

	"dpr/internal/domain"
	"dpr/internal/sheet"
)

// 이팜 사입현황 내보내기와 같은 모양 (제목 행, 빈 열, 꼬리말 포함)
func efarmPurchaseSheet() sheet.Sheet {
	return sheet.Sheet{Name: "Sheet", Rows: [][]string{
		{"사입현황 20260202 ~ 20261028"},
		{"일자", "약품명", "약품코드", "거래처", "구분", "", "거래처구분", "단위", "단위수량", "수량", "", "단가", "합계", "", "재고단위", "재고수량", "제약회사"},
		{"20260312", "보람정500마이크로그램(보람성분)_(0.5mg/1정)", "900000020", "(주) 가람도매", "사입", "", "도매상", "정", "30", "3", "", "27300", "81900", "", "정", "233", "보람제약"},
		{"20260520", "하늘정10밀리그램", "900000030", "(주)나래도매", "반품", "", "도매상", "정", "30", "-3", "", "51000", "-153000", "", "정", "0", "하늘제약"},
		{"20260815", "별빛정2밀리그램_(2mg/1정)", "900000040", "라온도매(주)", "폐기", "", "도매상", "정", "1", "-0.75", "", "40", "-30", "", "정", "0", ""},
		{"", "", "", "", "", "1 / 1"},
		{"", "", "", "", "", "", "", "", "", "", "", "", "", "출력일자 : 2026년 10월 28일"},
	}}
}

func TestPurchaseAutoMapAndParse(t *testing.T) {
	sh := efarmPurchaseSheet()
	tb := DetectTable(sh, PurchaseSchema, nil)
	if tb.HeaderRow != 1 || len(tb.Missing) != 0 {
		t.Fatalf("헤더 탐지 실패: row=%d missing=%v", tb.HeaderRow, tb.Missing)
	}
	if tb.RangeFrom != "2026-02-02" || tb.RangeTo != "2026-10-28" {
		t.Fatalf("제목 기간: %s~%s", tb.RangeFrom, tb.RangeTo)
	}
	if tb.Mapping["pack_qty"] != 9 || tb.Mapping["pack_size"] != 8 || tb.Mapping["price"] != 11 {
		t.Fatalf("매핑: %v", tb.Mapping)
	}
	recs, issues := ParsePurchases(tb)
	if len(recs) != 3 || len(issues) != 0 {
		t.Fatalf("레코드 %d, 이슈 %v", len(recs), issues)
	}
	if recs[0].Qty != domain.Units(90) || recs[0].UnitPrice != 910 || recs[0].Kind != "purchase" {
		t.Fatalf("보람정 사입: %+v", recs[0])
	}
	if recs[1].Qty != domain.Units(-90) || recs[1].Kind != "return" {
		t.Fatalf("반품: %+v", recs[1])
	}
	if recs[2].Qty != domain.QtyFromFloat(-0.75) || recs[2].Kind != "disposal" {
		t.Fatalf("폐기 3/4정: %+v", recs[2])
	}
}

// 이팜 약품별 사입현황(제조번호·유효기간 포함) 모양: 단위수량 열 없이 수량·총수량
func TestPurchaseWithLotColumns(t *testing.T) {
	sh := sheet.Sheet{Name: "사입", Rows: [][]string{
		{"일자", "구분", "거래처", "약품코드", "수량", "단가", "총수량", "합계액", "부가세", "유효기간", "제조번호"},
		{"2026-10-16", "사입", "다솜도매(주)", "900000010", "7", "19,200", "210", "134,400", "포함", "20290531", "LOT-1003"},
		{"2026-05-14", "사입", "(주)나래도매", "900000010", "2", "19,200", "60", "38,400", "포함", "20280630", "LOT-1001"},
	}}
	tb := DetectTable(sh, PurchaseSchema, nil)
	recs, _ := ParsePurchases(tb)
	if len(recs) != 2 {
		t.Fatalf("레코드 수 %d", len(recs))
	}
	r := recs[0]
	if r.Qty != domain.Units(210) || r.PackSize != 30 || r.UnitPrice != 640 || r.Lot != "LOT-1003" || r.Expiry != "2029-05-31" {
		t.Fatalf("제조번호 포함 사입: %+v", r)
	}
}

func TestDispenseParse(t *testing.T) {
	sh := sheet.Sheet{Name: "Sheet", Rows: [][]string{
		{"조제현황 20260202 ~ 20261028"},
		{"일자", "약품명", "약품코드", "단위", "", "단위수량", "투약량", "일수", "횟수", "", "총수량"},
		{"20260602", "누리정250마이크로그램", "900000010", "정", "", "1", "0.5", "120", "1", "", "60"},
		{"20260611", "푸른정", "090000050", "정", "", "1", "0.5", "75", "1", "", "37.5"},
		{"", "", "", "", "", "", "", "", "", "출력일자 : 2026년 10월 28일"},
	}}
	tb := DetectTable(sh, DispenseSchema, nil)
	recs, _ := ParseDispenses(tb)
	if len(recs) != 2 || recs[1].Qty != domain.QtyFromFloat(37.5) || recs[0].Code != "900000010" {
		t.Fatalf("조제: %+v", recs)
	}
}

func TestStockParse_여러양식(t *testing.T) {
	// 시트1: 코드 + 낱알 재고, '30관 1상자' 단위
	s1 := sheet.Sheet{Name: "시트1", Rows: [][]string{
		{"보관위치", "급여구분", "약품코드", "약품명", "재고단위", "기본수량", "사입단가", "낱알 재고", "재고금액"},
		{"B-2 / 5", "급여", "900000020", "보람정500마이크로그램(보람성분)_(0.5mg/1정)", "정", "30", "910", "23", "20930"},
		{"", "급여", "900000060", "새봄점안액1%(새봄성분)(1회용)_(4mg/0.4mL)", "30관 1상자", "1", "12600", "3", "37800"},
		{"", "급여", "900000070", "온빛캡슐110밀리그램", "캡슐", "1", "980", "", "0"},
	}}
	tb := DetectTable(s1, StockSchema, nil)
	recs, _ := ParseStock(tb)
	if len(recs) != 3 || recs[0].Qty != domain.Units(23) || recs[1].Qty != domain.Units(90) || recs[1].UnitPrice != 420 || recs[2].Qty != 0 {
		t.Fatalf("시트1: %+v", recs)
	}
	// 시트2: 제품명 + 제품규격 + 포장 개수
	s2 := sheet.Sheet{Name: "시트2", Rows: [][]string{
		{"위치", "순번", "제품명", "제품규격", "수량", "매출단가", "매출금액"},
		{"B-2", "41", "보람정500mcg(PTP) 30T 보람제약", "30T", "4", "27300", "109200"},
	}}
	tb = DetectTable(s2, StockSchema, nil)
	recs, _ = ParseStock(tb)
	if len(recs) != 1 || recs[0].Qty != domain.Units(120) || recs[0].UnitPrice != 910 || recs[0].Code != "" {
		t.Fatalf("시트2: %+v", recs)
	}
	// 시트3: 앞 0 이 사라진 숫자 코드
	s3 := sheet.Sheet{Name: "시트3", Rows: [][]string{
		{"", "약품코드", "약품명", "", "", "단가", "재고", "재고금액"},
		{"3", "90000080", "단비정10mg", "5", "1호기", "350", "12", "4200"},
	}}
	tb = DetectTable(s3, StockSchema, nil)
	recs, _ = ParseStock(tb)
	if len(recs) != 1 || recs[0].Code != "090000080" || recs[0].Qty != domain.Units(12) {
		t.Fatalf("시트3: %+v", recs)
	}
}

func TestOverrideMapping(t *testing.T) {
	sh := sheet.Sheet{Name: "기타프로그램", Rows: [][]string{
		{"Date", "Item", "Vendor", "Count"},
		{"2026-09-15", "900000020", "가람도매", "45"},
	}}
	tb := DetectTable(sh, PurchaseSchema, nil)
	if len(tb.Missing) == 0 {
		t.Fatal("영문 헤더는 자동 매핑이 안 되어야 함 → 매핑 필요")
	}
	tb = DetectTable(sh, PurchaseSchema, &Override{HeaderRow: 0, Mapping: Mapping{"date": 0, "code": 1, "supplier": 2, "total_qty": 3}})
	recs, _ := ParsePurchases(tb)
	if len(recs) != 1 || recs[0].Qty != domain.Units(45) || recs[0].Kind != "purchase" {
		t.Fatalf("수동 매핑: %+v", recs)
	}
}

// 고시 별표1 목록표의 양식(시트명·분류 행·성분명 행·변경대비표·붙임)만 본뜬 가상 품목
func noticeSheets() []sheet.Sheet {
	hdr := []string{"주성분코드", "제품코드", "제품명", "업체명", "규격", "단위", "상한금액", "퇴장방지", "", "투여", "비고"}
	return []sheet.Sheet{
		{Name: "별지1_신설('26.11.01.시행)", Rows: [][]string{
			{"별지1. 약제 급여 목록 및 급여 상한금액표 중 신설"}, hdr,
			{"113   항전간제"},
			{"999901ATB", "gaonamate   12.5mg"},
			{"999901ATB", "910000010", "가온정(가온성분)_(12.5mg/1정)", "가온제약(주)", "1", "정", "1520", "", "", "내복", "약가유연계약"},
		}},
		{Name: "별지2_신설('26.11.11.시행)", Rows: [][]string{
			{"별지2"}, hdr,
			{"999902ATB", "910000020", "나린정15밀리그램", "나린제약(주)", "1", "정", "450", "", "", "내복", ""},
		}},
		{Name: "별지3_변경('26.11.01.시행)", Rows: [][]string{
			{"별지3"}, hdr,
			{"131   안과용제"},
			{"", "910000030", "새봄점안액1%(새봄성분)(1회용)_(4mg/0.4mL)", "새봄제약(주)", "0.4", "mL/관", "350", "", "", "외용", ""},
			{"", "910000040", "온새정", "희망의약품센터", "1", "정", "3800", "", "", "내복", ""},
			{"", "910000050", "다온정(다온성분디.씨)_(0.4g/1정)", "주식회사다온사이언스", "1", "정", "120", "", "", "내복", ""},
			{"", "910000060", "한결정40밀리그램", "한결바이오(주)", "1", "정", "1700", "", "", "내복", "약가유연계약"},
		}},
		{Name: "별지5_삭제('26.11.01.시행)~'27.04.30", Rows: [][]string{
			{"별지5"}, hdr,
			{"999903ATB", "910000070", "미리내츄정5mg", "미리내제약(주)", "1", "정", "700", "", "", "내복", ""},
		}},
		{Name: "변경대비표", Rows: [][]string{
			{"[참고자료] 변경대비표"}, {},
			{"연번", "투여", "분류", "제품코드", "제품명", "제약사명", "규격", "단위", "상한금액", "변경 전", "변경 후", "비  고", "전문/일반", "시행일"},
			{"1", "내복", "113", "910000040", "온새정", "희망의약품센터", "1", "정", "3800", "3100", "3800", "상한금액(인상)", "전문", "46327"},
			{"3", "내복", "114", "910000050", "다온정", "다온사이언스", "1", "정", "120", "다온정(다온성분)_(0.4g/1정)", "다온정(다온성분디.씨)_(0.4g/1정)", "제품명", "전문", "46327"},
			{"8", "외용", "131", "910000030", "새봄점안액1%(1회용)", "새봄제약(주)", "0.4", "mL/관", "350", "365", "350", "상한금액", "전문", "2026-11-01 00:00:00"},
			{"11", "내복", "232", "910000060", "한결정40밀리그램", "한결바이오(주)", "1", "정", "1700", "1700", "1,700(별도합의상한금액 변동)", "상한금액(별도합의금액)", "전문", "46327"},
		}},
		{Name: "붙임1('26년11월_금번신설품목중가산종료등관련)", Rows: [][]string{
			{"[붙임1] 신설 품목의 상한금액 변경 사항"}, {},
			{"구분", "주성분코드", "제품코드", "제품명", "제약사명", "규격", "단위", "조정후", "시행일"},
			{"별지2", "999902ATB", "910000020", "나린정15밀리그램", "나린제약(주)", "1", "정", "340", "2027-11-11"},
		}},
	}
}

func TestParseNotice(t *testing.T) {
	res, err := ParseNotice(noticeSheets(), "약제 급여 목록 및 급여 상한금액표 일부개정고시(2026-219호)_별표1 목록표.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if res.NoticeNo != "2026-219" {
		t.Fatalf("고시번호 %q", res.NoticeNo)
	}
	by := map[string]PriceChange{}
	for _, c := range res.Changes {
		by[c.Code+"|"+c.EffectiveDate] = c
	}
	sb := by["910000030|2026-11-01"]
	if sb.Detail != "price_down" || sb.PriceBefore != 365 || sb.PriceAfter != 350 || sb.Category != "131   안과용제" {
		t.Fatalf("새봄 인하: %+v", sb)
	}
	if c := by["910000040|2026-11-01"]; c.Detail != "price_up" || c.PriceBefore != 3100 {
		t.Fatalf("온새 인상: %+v", c)
	}
	if c := by["910000050|2026-11-01"]; c.Detail != "name_change" {
		t.Fatalf("다온 제품명: %+v", c)
	}
	if c := by["910000060|2026-11-01"]; c.Detail != "agreed_price" || !c.Flexible {
		t.Fatalf("한결 별도합의: %+v", c)
	}
	if c := by["910000070|2026-11-01"]; c.Kind != "delete" || c.GraceUntil != "2027-04-30" || c.PriceBefore != 700 {
		t.Fatalf("미리내 삭제: %+v", c)
	}
	if c := by["910000010|2026-11-01"]; c.Kind != "new" || !c.Flexible || c.Ingredient != "gaonamate   12.5mg" {
		t.Fatalf("가온 신설: %+v", c)
	}
	if c := by["910000020|2027-11-11"]; c.Detail != "price_down" || c.PriceBefore != 450 || c.PriceAfter != 340 {
		t.Fatalf("붙임 조정 예정: %+v", c)
	}
}

// 붙임의 조정후 칸이 비었거나 '별도합의'면 0원 인하로 보면 안 된다 (전액 손실로 계산됨)
func TestNoticeScheduledBlankPrice(t *testing.T) {
	sh := noticeSheets()
	for i := range sh {
		if strings.HasPrefix(sh[i].Name, "붙임1") {
			sh[i].Rows = append(sh[i].Rows,
				[]string{"별지1", "999901ATB", "910000010", "가온정", "가온제약(주)", "1", "정", "", "2027-11-11"},
				[]string{"별지2", "999902ATB", "910000020", "나린정15밀리그램", "나린제약(주)", "1", "정", "별도합의", "2027-12-01"})
		}
	}
	res, err := ParseNotice(sh, "일부개정고시(2026-219호)_별표1 목록표.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]PriceChange{}
	for _, c := range res.Changes {
		by[c.Code+"|"+c.EffectiveDate] = c
	}
	if c := by["910000010|2027-11-11"]; c.Detail != "unknown" || c.PriceAfter != 0 {
		t.Fatalf("조정후 빈칸은 미확인: %+v", c)
	}
	if c := by["910000020|2027-12-01"]; c.Detail != "agreed_price" || !c.Flexible {
		t.Fatalf("조정후 별도합의는 유연계약: %+v", c)
	}
	if c := by["910000020|2027-11-11"]; c.Detail != "price_down" || c.PriceAfter != 340 {
		t.Fatalf("숫자 조정후는 그대로: %+v", c)
	}
}

func TestReadXLSXAndCSV(t *testing.T) {
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "목록")
	_ = f.SetCellValue("목록", "A1", "약품코드")
	_ = f.SetCellValue("목록", "B1", "수량")
	_ = f.SetCellValue("목록", "A2", 90000080) // 숫자 셀 → 앞 0 소실 상황
	_ = f.SetCellValue("목록", "B2", 7.5)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	sheets, err := sheet.ReadBytes("재고.xlsx.xlsx", buf.Bytes())
	if err != nil || len(sheets) != 1 {
		t.Fatalf("xlsx 읽기: %v", err)
	}
	tb := DetectTable(sheets[0], StockSchema, nil)
	recs, _ := ParseStock(tb)
	if len(recs) != 1 || recs[0].Code != "090000080" || recs[0].Qty != domain.QtyFromFloat(7.5) {
		t.Fatalf("xlsx 재고: %+v", recs)
	}
	// CP949 CSV
	enc, _ := korean.EUCKR.NewEncoder().Bytes([]byte("약품코드,수량\n900000020,45\n"))
	sheets, err = sheet.ReadBytes("재고.csv", enc)
	if err != nil {
		t.Fatal(err)
	}
	tb = DetectTable(sheets[0], StockSchema, nil)
	recs, _ = ParseStock(tb)
	if len(recs) != 1 || recs[0].Code != "900000020" || recs[0].Qty != domain.Units(45) {
		t.Fatalf("CP949 CSV: %+v", recs)
	}
	if sheet.ColumnLetter(0) != "A" || sheet.ColumnLetter(27) != "AB" {
		t.Fatal("ColumnLetter")
	}
}
