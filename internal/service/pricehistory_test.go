package service

import (
	"fmt"
	"testing"
	"time"

	"dpr/internal/crawler"
)

func histItem(code, eff string, price int, pay string) crawler.DgamtItem {
	return crawler.DgamtItem{MdsCd: code, ItmNm: "가나정", MnfEntpNm: "가나제약", NomNm: "1", Unit: "정", MxCprc: fmt.Sprint(price), AdtStaDd: eff, PayTpNm: pay}
}

// 약가기준정보의 적용시작일별 상한가에서 지난 1년 가격 변경을 뽑는다
func TestPriceHistoryFromDgamt(t *testing.T) {
	items := []crawler.DgamtItem{
		histItem("900000010", "20240101", 1200, "급여"), // 1년보다 오래된 변경의 '전' 가격으로만 쓰인다
		histItem("900000010", "20260601", 1000, "급여"), // 인하 (1200 → 1000)
		histItem("900000010", "20261101", 900, "급여"),  // 인하 (1000 → 900)
		histItem("900000010", "20270101", 850, "급여"),  // 아직 시행 전 → 넣지 않는다 (실제 고시가 맡는다)
		histItem("900000020", "20250101", 500, "급여"),
		histItem("900000020", "20260301", 500, "급여"), // 가격 그대로 (다른 항목만 바뀐 행) → 변경 아님
		histItem("900000020", "20260901", 550, "급여"), // 인상
		histItem("900000030", "20250101", 300, "급여"),
		histItem("900000030", "20260401", 300, "삭제"), // 삭제
		histItem("900000030", "20260801", 280, "급여"), // 다시 등재 → 변경 아님
		histItem("900000040", "20200101", 700, "급여"),
		histItem("900000040", "20250101", 650, "급여"), // 1년보다 오래됨 → 넣지 않는다
		histItem("코드아님", "20260601", 1, "급여"),
	}
	got := priceHistoryFromDgamt(items, "2025-11-22", "2026-11-22")
	want := []string{"2026-06-01|900000010|1200→1000", "2026-09-01|900000020|500→550", "2026-11-01|900000010|1000→900"}
	if len(got) != len(want) {
		t.Fatalf("%d건: %+v", len(got), got)
	}
	for i, c := range got {
		if s := fmt.Sprintf("%s|%s|%g→%g", c.Eff, c.Code, c.Before, c.After); s != want[i] {
			t.Errorf("%d: %s, 기대 %s", i, s, want[i])
		}
	}
}

// 이력은 '가격 이력' 고시로 들어가고, 실제 고시가 같은 품목·시행일을 다루면 실제 고시가 우선한다
func TestStorePriceHistory(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, s.loc)
	items := []crawler.DgamtItem{
		histItem("900000010", "20240101", 1200, "급여"),
		histItem("900000010", "20260601", 1000, "급여"),
		histItem("900000010", "20261101", 900, "급여"),
		histItem("900000020", "20250101", 500, "급여"),
		histItem("900000020", "20260901", 550, "급여"),
	}
	total, downs, changed, err := s.storePriceHistory(items, now)
	if err != nil || total != 3 || downs != 2 || !changed {
		t.Fatalf("처음: %d %d %v %v", total, downs, changed, err)
	}
	if _, _, changed, _ := s.storePriceHistory(items, now); changed {
		t.Fatal("내용이 같으면 다시 넣지 않는다")
	}
	var notices, rows int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notice WHERE source = 'history'").Scan(&notices)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM price_change c JOIN notice n ON n.id = c.notice_id WHERE n.source = 'history'").Scan(&rows)
	if notices != 3 || rows != 3 {
		t.Fatalf("고시 %d, 행 %d", notices, rows)
	}
	// 지난 회차로 보인다 (재고가 없어도 '전체 인하 품목'에)
	if r := rankRow(t, s, "2026-11-01", "900000010", false); r == nil || r.PriceBefore != 1000 || r.PriceAfter != 900 {
		t.Fatalf("순위: %+v", r)
	}
	// 실제 고시가 같은 품목·시행일을 다루면 (여기서는 정정: 1000 → 950) 실제 고시가 이긴다
	notice := map[string][][]any{
		"별지3_변경('26.11.01.시행)": {{"별지3"}, noticeHdr5, {"", "900000010", "가나정", "가나제약", "1", "정", 950, "", "", "내복", ""}},
		"변경대비표": {{"변경대비표"}, {},
			{"연번", "투여", "분류", "제품코드", "제품명", "제약사명", "규격", "단위", "상한금액", "변경 전", "변경 후", "비  고", "전문/일반", "시행일"},
			{1, "내복", 229, "900000010", "가나정", "가나제약", "1", "정", 950, 1000, 950, "상한금액", "전문", "2026-11-01"}},
	}
	mustImport(t, s, ImportRequest{Kind: "notice", FileName: "고시(2026-900호)_별표1 목록표.xlsx", DataBase64: xlsxB64(t, notice)})
	if r := rankRow(t, s, "2026-11-01", "900000010", false); r == nil || r.PriceAfter != 950 {
		t.Fatalf("실제 고시가 우선해야 함: %+v", r)
	}
	// 다음 달 목록을 받아 이력을 다시 만들면, 실제 고시가 있는 (품목, 시행일)은 이력 행을 만들지 않는다
	items = append(items, histItem("900000020", "20261115", 520, "급여"))
	if total, _, changed, err := s.storePriceHistory(items, now); err != nil || !changed || total != 3 {
		t.Fatalf("다시: %d %v %v", total, changed, err)
	}
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM price_change c JOIN notice n ON n.id = c.notice_id WHERE n.source = 'history' AND c.product_code = '900000010' AND c.effective_date = '2026-11-01'").Scan(&rows)
	if rows != 0 {
		t.Fatal("실제 고시가 있는 품목·시행일에 이력 행이 또 생김")
	}
	// 목록(고시 화면)에 시행일 순으로 섞여 보인다
	list, err := s.ListNotices()
	if err != nil || len(list) != 4 {
		t.Fatalf("고시 목록 %d %v", len(list), err)
	}
}
