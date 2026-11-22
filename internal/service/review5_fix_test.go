package service

// 업무 논리 검토(5차)에서 나온 문제의 회귀 테스트 — 합성 자료만 쓴다

import (
	"fmt"
	"testing"
	"time"

	"dpr/internal/domain"
	"dpr/internal/ingest"
)

var purchaseHdr5 = []any{"일자", "약품명", "약품코드", "거래처", "구분", "", "거래처구분", "단위", "단위수량", "수량", "", "단가", "합계", "", "재고단위", "재고수량", "제약회사", "유효기간", "제조번호"}
var noticeHdr5 = []any{"주성분코드", "제품코드", "제품명", "업체명", "규격", "단위", "상한금액", "퇴장방지", "", "투여", "비고"}

func rankRow(t *testing.T, s *Service, eff, code string, heldOnly bool) *RankRow {
	t.Helper()
	rk, err := s.Ranking(eff, heldOnly)
	if err != nil {
		t.Fatal(err)
	}
	for i := range rk.Rows {
		if rk.Rows[i].Code == code {
			return &rk.Rows[i]
		}
	}
	return nil
}

// 같은 약이 두 번 인하되면 두 번째 회차 인하폭은 (첫 인하 후 − 두 번째 인하 후) — 예전에는 첫 인하 전 매입가로 계산해 손실이 두 번 잡혔다
func TestDoubleCutNotCountedTwice(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)
	nuri := "누리정250마이크로그램(누리성분)_(0.25mg/1정)"
	notice := map[string][][]any{
		"별지3_변경('26.12.01.시행)": {{"별지3"}, noticeHdr5, {"", "900000010", nuri, "누리제약(주)", "1", "정", 550, "", "", "내복", ""}},
		"변경대비표": {{"변경대비표"}, {},
			{"연번", "투여", "분류", "제품코드", "제품명", "제약사명", "규격", "단위", "상한금액", "변경 전", "변경 후", "비  고", "전문/일반", "시행일"},
			{1, "내복", 229, "900000010", nuri, "누리제약", "1", "정", 550, 590, 550, "상한금액", "전문", "2026-12-01"}},
	}
	mustImport(t, s, ImportRequest{Kind: "notice", FileName: "고시(2026-1000호)_별표1 목록표.xlsx", DataBase64: xlsxB64(t, notice)})
	first := rankRow(t, s, "2026-11-01", "900000010", true)
	second := rankRow(t, s, "2026-12-01", "900000010", true)
	if first == nil || second == nil {
		t.Fatal("두 회차 모두 순위에 있어야 함")
	}
	if first.Delta != 50 || second.Delta != 40 || second.PriceSource != "ceiling" || second.CurrentUnitPrice != 590 {
		t.Fatalf("1차 %+v / 2차 인하폭 %.0f(%s, 현재가 %.0f)", first.Delta, second.Delta, second.PriceSource, second.CurrentUnitPrice)
	}
	if second.GrossLoss != domain.RoundWon(second.Stock.Float()*40) {
		t.Fatalf("2차 손실 %d (재고 %s)", second.GrossLoss, second.Stock)
	}
}

// 반품한 약은 그 도매 몫이 아니다 — 예전에는 다 반품한 도매에 실물보상을 권하고 보상액이 손실보다 커졌다
func TestFIFOReturnsInPositions(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	name := "테스트정_(1mg/1정)"
	p := [][]any{
		{"사입현황 20260801 ~ 20261027"}, purchaseHdr5,
		{"20261001", name, "600000010", "비도매", "사입", "", "도매상", "정", 10, 6, "", 10000, 60000, "", "정", 0, "", "", ""},
		{"20261020", name, "600000010", "가도매", "사입", "", "도매상", "정", 10, 10, "", 10000, 100000, "", "정", 0, "", "", ""},
		{"20261025", name, "600000010", "가도매", "반품", "", "도매상", "정", 10, -10, "", 10000, -100000, "", "정", 0, "", "", ""},
		{"", "", "", "", "", "1 / 1"},
	}
	mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "사입현황.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": p})})
	notice := map[string][][]any{
		"별지3_변경('26.11.01.시행)": {{"별지3"}, noticeHdr5, {"", "600000010", name, "제약", "1", "정", 900, "", "", "내복", ""}},
		"변경대비표": {{"변경대비표"}, {},
			{"연번", "투여", "분류", "제품코드", "제품명", "제약사명", "규격", "단위", "상한금액", "변경 전", "변경 후", "비  고", "전문/일반", "시행일"},
			{1, "내복", 229, "600000010", name, "제약", "1", "정", 900, 1000, 900, "상한금액", "전문", "2026-11-01"}},
	}
	mustImport(t, s, ImportRequest{Kind: "notice", FileName: "고시(2026-998호)_별표1 목록표.xlsx", DataBase64: xlsxB64(t, notice)})
	d, err := s.ProductDetail("2026-11-01", "600000010")
	if err != nil {
		t.Fatal(err)
	}
	if d.Decision.Stock != domain.Units(60) {
		t.Fatalf("재고 %s", d.Decision.Stock)
	}
	for _, p := range d.Positions {
		switch p.SupplierName {
		case "가도매":
			if p.EstimatedHeld != 0 || p.Method == "physical" {
				t.Fatalf("다 반품한 가도매: %+v", p.SupplierPosition)
			}
		case "비도매":
			if p.EstimatedHeld != domain.Units(60) || p.Method != "physical" {
				t.Fatalf("비도매: %+v", p.SupplierPosition)
			}
		}
	}
	if d.Decision.Recovery > d.Decision.GrossLoss {
		t.Fatalf("보상액 %d 이 손실 %d 보다 큼", d.Decision.Recovery, d.Decision.GrossLoss)
	}
}

// 변경대비표가 없는 고시: 변경 전 가격은 과거 고시 → 시행일 전 급여목록 → (참고) 사입 단가. 사입 단가로는 인상·인하를 정하지 않는다
func TestNoticeBeforePriceFallback(t *testing.T) {
	setup := func(t *testing.T, unitPrice int, masterDate string) *Service {
		s, err := NewTemp(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		name := "테스트정_(1mg/1정)"
		p := [][]any{{"사입현황 20260801 ~ 20261027"}, purchaseHdr5,
			{"20261001", name, "600000010", "가도매", "사입", "", "도매상", "정", 10, 10, "", unitPrice * 10, unitPrice * 100, "", "정", 0, "", "", ""}, {"", "", "", "", "", "1 / 1"}}
		mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "사입현황.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": p})})
		if masterDate != "" {
			recs := []ingest.MasterRec{{Code: "600000010", Name: name, Spec: "1", Unit: "정", Price: 1000}}
			for i := 0; i < 5; i++ {
				recs = append(recs, ingest.MasterRec{Code: fmt.Sprintf("6000001%02d", i), Name: "다른약", Spec: "1", Unit: "정", Price: 10})
			}
			if _, _, _, err := s.storeDrugMaster("급여목록", "m", masterDate, recs, 0); err != nil {
				t.Fatal(err)
			}
		}
		// 변경대비표 시트가 없는 파일 — 변경 후 990 만 있다
		notice := map[string][][]any{"별지3_변경('26.11.01.시행)": {{"별지3"}, noticeHdr5, {"", "600000010", name, "제약", "1", "정", 990, "", "", "내복", ""}}}
		mustImport(t, s, ImportRequest{Kind: "notice", FileName: "고시(2026-997호)_별표1 목록표.xlsx", DataBase64: xlsxB64(t, notice)})
		return s
	}
	change := func(s *Service) (detail, src string, before float64) {
		_ = s.DB.QueryRow("SELECT detail, before_source, IFNULL(price_before, 0) FROM price_change").Scan(&detail, &src, &before)
		return
	}
	t.Run("급여목록이 있으면 그 가격", func(t *testing.T) {
		s := setup(t, 1000, "2026-10-01")
		if d, src, b := change(s); d != "price_down" || src != "master" || b != 1000 {
			t.Fatalf("%s %s %.0f", d, src, b)
		}
		if r := rankRow(t, s, "2026-11-01", "600000010", true); r == nil || r.Delta != 10 {
			t.Fatalf("순위: %+v", r)
		}
	})
	t.Run("시행일 뒤 목록은 쓰지 않음", func(t *testing.T) {
		s := setup(t, 950, "2026-11-01")
		if d, src, _ := change(s); d != "unknown" || src != "purchase" {
			t.Fatalf("%s %s", d, src)
		}
	})
	t.Run("사입 단가로는 인상으로 정하지 않음", func(t *testing.T) {
		s := setup(t, 950, "") // 예전: 950 < 990 → '인상'
		if d, src, _ := change(s); d != "unknown" || src != "purchase" {
			t.Fatalf("%s %s", d, src)
		}
		if r := rankRow(t, s, "2026-11-01", "600000010", false); r != nil {
			t.Fatalf("매입가가 변경 후 가격보다 낮으면 손실이 없어 순위에 넣지 않음: %+v", r)
		}
	})
	t.Run("손실 가능성이 있으면 확인 필요로 순위에", func(t *testing.T) {
		s := setup(t, 1000, "")
		if d, _, _ := change(s); d != "unknown" {
			t.Fatalf("%s", d)
		}
		r := rankRow(t, s, "2026-11-01", "600000010", true)
		if r == nil || r.Recommendation != "check_price" || r.Delta != 0 || r.UnitNote == "" {
			t.Fatalf("확인 필요: %+v", r)
		}
	})
}

// 다시 확정하면 판정에서 빠진 도매의 '예정' 행을 지운다(완료 행은 남김). 금액 0 은 0원 (예: 도매 거절) — 다시 계산은 Recalc 로만
func TestSettlementStaleAndZero(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)
	const eff, code = "2026-11-01", "900000010"
	sw := supplierID(t, s, "다솜도매")
	swRow := func() *SettlementView {
		list, _ := s.ListSettlements(SettlementFilter{Code: code})
		for i := range list {
			if list[i].SupplierID == sw {
				return &list[i]
			}
		}
		return nil
	}
	if n, err := s.ConfirmDecision(eff, code); err != nil || n == 0 {
		t.Fatalf("%d %v", n, err)
	}
	v := swRow()
	if v == nil {
		t.Fatal("다솜도매 보상 행이 없음")
	}
	if err := s.UpdateSettlement(SettlementInput{ID: v.ID, Method: v.Method, Qty: v.Qty.Float(), Amount: 0, Status: "planned", Memo: "도매 거절"}); err != nil {
		t.Fatal(err)
	}
	if x := swRow(); x == nil || x.Amount != 0 {
		t.Fatalf("0원으로 저장해야 함: %+v", x)
	}
	if err := s.UpdateSettlement(SettlementInput{ID: v.ID, Method: v.Method, Qty: v.Qty.Float(), Recalc: true, Status: "planned"}); err != nil {
		t.Fatal(err)
	}
	if x := swRow(); x == nil || x.Amount == 0 {
		t.Fatalf("Recalc 면 다시 계산: %+v", x)
	}
	// 도매를 비활성으로 바꾸고 다시 확정 → 예정 행을 지운다
	setActive := func(on bool) {
		if _, err := s.SaveSupplier(SupplierInput{ID: sw, Name: "다솜도매", Kind: "wholesaler", Active: on}); err != nil {
			t.Fatal(err)
		}
	}
	setActive(false)
	if _, err := s.ConfirmDecision(eff, code); err != nil {
		t.Fatal(err)
	}
	if x := swRow(); x != nil {
		t.Fatalf("비활성 도매의 예정 행이 남음: %+v", x)
	}
	// 완료 행은 다시 확정해도 남는다
	setActive(true)
	if _, err := s.ConfirmDecision(eff, code); err != nil {
		t.Fatal(err)
	}
	v = swRow()
	if v == nil {
		t.Fatal("다시 활성화하면 예정 행이 다시 생겨야 함")
	}
	if err := s.SetSettlementStatus(v.ID, "done"); err != nil {
		t.Fatal(err)
	}
	setActive(false)
	if _, err := s.ConfirmDecision(eff, code); err != nil {
		t.Fatal(err)
	}
	if x := swRow(); x == nil || x.Status != "done" {
		t.Fatalf("완료 행이 지워짐: %+v", x)
	}
}

// 지난 회차의 재고 기준일 뒤에 넣은 실계수는 그 회차 계산에 쓰지 않고 '나중 실계수'로 보여 준다 (재고 화면에는 오늘 날짜로 반영)
func TestLaterCountShownNotUsed(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)
	before := rankRow(t, s, "2026-11-01", "900000010", true)
	if before == nil {
		t.Fatal("순위에 없음")
	}
	today := s.Today()
	if _, err := s.SaveCount(CountInput{Code: "900000010", Qty: 150, AsOf: today, Timing: "end", Source: "손실 순위"}); err != nil {
		t.Fatal(err)
	}
	after := rankRow(t, s, "2026-11-01", "900000010", true)
	if after.Stock != before.Stock || after.CountQty != nil {
		t.Fatalf("지난 회차 계산은 그대로: %s → %s (실계수 %v)", before.Stock, after.Stock, after.CountQty)
	}
	if after.LaterCountQty == nil || *after.LaterCountQty != domain.Units(150) || after.LaterCountDate != today {
		t.Fatalf("나중 실계수: %v %s", after.LaterCountQty, after.LaterCountDate)
	}
	st, err := s.stockMap(today, true)
	if err != nil || st["900000010"].Qty != domain.Units(150) {
		t.Fatalf("재고 화면(오늘): %s %v", st["900000010"].Qty, err)
	}
}

// 월 사용량은 조제 기록이 있는 기간(최대 6개월)으로 나눈다 — 3개월 치만 올렸는데 6으로 나누면 절반으로 보였다
func TestMonthlyUseCoverage(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res, err := s.DB.Exec("INSERT INTO import_batch(kind, filename, sha256, range_from, range_to, row_count, created_at) VALUES('dispense', 'd.xlsx', 'x', '', '', 0, ?)", time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	bid, _ := res.LastInsertId()
	now := s.NowKST()
	for i := 0; i < 90; i++ { // 최근 90일, 하루 10개
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		if _, err := s.DB.Exec("INSERT INTO dispense_line(batch_id, date, product_code, qty) VALUES(?, ?, '600000010', ?)", bid, d, int64(domain.Units(10))); err != nil {
			t.Fatal(err)
		}
	}
	got := s.monthlyUse()["600000010"]
	if got < 290 || got > 320 { // 한 달 ≈ 304개 (6으로 나누면 150)
		t.Fatalf("월 사용량 %.1f", got)
	}
}

// 실물보상 완료는 '도매 앱에 등록했다고 표시한' 수량으로 기록한다 — 판정의 보유량에는 안 찍은 재고의 추정분이 섞일 수 있다
func TestFinishPhysicalUsesRegisteredQty(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)
	const eff, code = "2026-11-01", "900000010"
	sw := supplierID(t, s, "다솜도매")
	if _, err := s.FinishPhysical(eff, code, sw); err == nil {
		t.Fatal("등록한 바코드가 없으면 완료로 기록하지 않는다")
	}
	sc, err := s.RecordScan(ScanInput{EffectiveDate: eff, ProductCode: code, Raw: "(01)08809000000117(17)290531(10)LOT-1003(21)SN10000009"})
	if err != nil || sc.Status != "added" || sc.Unit.SupplierID != sw {
		t.Fatalf("스캔: %+v %v", sc, err)
	}
	if err := s.SetScanRegistered(sc.Unit.ID, true); err != nil {
		t.Fatal(err)
	}
	d, _ := s.ProductDetail(eff, code)
	var held domain.Qty
	for _, p := range d.Positions {
		if p.SupplierID == sw {
			held = p.HeldUsed
		}
	}
	st, err := s.FinishPhysical(eff, code, sw)
	if err != nil {
		t.Fatal(err)
	}
	if st.Qty != sc.Unit.Qty || st.Qty >= held || st.Status != "done" || st.Method != "physical" {
		t.Fatalf("완료 수량 %s (등록 %s, 판정 보유량 %s) %s/%s", st.Qty, sc.Unit.Qty, held, st.Method, st.Status)
	}
	if want := float64(domain.RoundWon(st.Qty.Float() * d.Row.Delta)); st.Amount != want {
		t.Fatalf("금액 %.0f, 기대 %.0f", st.Amount, want)
	}
	// 다시 확정해도 완료 행은 그대로
	if _, err := s.ConfirmDecision(eff, code); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListSettlements(SettlementFilter{EffectiveDate: eff, Code: code})
	for _, v := range list {
		if v.SupplierID == sw && (v.Qty != st.Qty || v.Status != "done") {
			t.Fatalf("완료 행이 바뀜: %+v", v)
		}
	}
}
