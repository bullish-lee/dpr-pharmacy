package service

// 테스트 데이터는 모두 합성 데이터입니다. (도매명·약품명·코드·수량·단가·제조번호·날짜 모두 가상 값)

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"dpr/internal/domain"
)

func xlsxB64(t *testing.T, sheets map[string][][]any) string {
	t.Helper()
	f := excelize.NewFile()
	first := true
	for name, rows := range sheets {
		if first {
			_ = f.SetSheetName("Sheet1", name)
			first = false
		} else {
			_, _ = f.NewSheet(name)
		}
		for i, row := range rows {
			cell, _ := excelize.CoordinatesToCellName(1, i+1)
			_ = f.SetSheetRow(name, cell, &row)
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func mustImport(t *testing.T, s *Service, req ImportRequest) *ImportResult {
	t.Helper()
	res, err := s.Import(req)
	if err != nil {
		t.Fatalf("Import(%s): %v", req.Kind, err)
	}
	if res.NeedsMapping {
		t.Fatalf("Import(%s) 매핑 필요: %+v", req.Kind, res.Preview)
	}
	return res
}

// 가상 품목 두 개(누리정·보람정)로 사입·조제·초기재고·고시 흐름을 재현한 합성 데이터.
// 도매: 가람도매·나래도매·다솜도매, 약품코드 900000010·900000020, 제조번호 LOT-xxxx 는 모두 가상 값이다.
func seedSample(t *testing.T, s *Service) {
	purchaseHdr := []any{"일자", "약품명", "약품코드", "거래처", "구분", "", "거래처구분", "단위", "단위수량", "수량", "", "단가", "합계", "", "재고단위", "재고수량", "제약회사", "유효기간", "제조번호"}
	nuri, boram := "누리정250마이크로그램(누리성분)_(0.25mg/1정)", "보람정500마이크로그램(보람성분)_(0.5mg/1정)"
	p := [][]any{
		{"사입현황 20260202 ~ 20261028"}, purchaseHdr,
		{"20260312", boram, "900000020", "(주) 가람도매", "사입", "", "도매상", "정", 30, 3, "", 27300, 81900, "", "정", 233, "", "20281130", "LOT-2001"},
		{"20260325", nuri, "900000010", "(주) 가람도매", "사입", "", "도매상", "정", 30, 1, "", 19200, 19200, "", "정", 0, "", "", ""},
		{"20260514", nuri, "900000010", "(주)나래도매", "사입", "", "도매상", "정", 30, 2, "", 19200, 38400, "", "정", 0, "", "20280630", "LOT-1001"},
		{"20260514", nuri, "900000010", "(주)나래도매", "사입", "", "도매상", "정", 30, 2, "", 19200, 38400, "", "정", 0, "", "20290228", "LOT-1002"},
		{"20260514", nuri, "900000010", "(주)나래도매", "사입", "", "도매상", "정", 30, 1, "", 19200, 19200, "", "정", 0, "", "20280630", "LOT-1001"},
		{"20260826", boram, "900000020", "(주) 가람도매", "사입", "", "도매상", "정", 30, 5, "", 27300, 136500, "", "정", 233, "", "20281130", "LOT-2002"},
		{"20261007", boram, "900000020", "(주) 가람도매", "사입", "", "도매상", "정", 30, 3, "", 27300, 81900, "", "정", 233, "", "20280930", "LOT-2003"},
		{"20261016", nuri, "900000010", "다솜도매(주)", "사입", "", "도매상", "정", 30, 7, "", 19200, 134400, "", "정", 180, "", "20290531", "LOT-1003"},
		{"", "", "", "", "", "1 / 1"},
	}
	mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "사입현황.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": p})})
	d := [][]any{
		{"조제현황 20260202 ~ 20261028"},
		{"일자", "약품명", "약품코드", "단위", "", "단위수량", "투약량", "일수", "횟수", "", "총수량"},
		{"20260602", nuri, "900000010", "정", "", 1, 1, 60, 1, "", 60},
		{"20261019", nuri, "900000010", "정", "", 1, 0.5, 120, 1, "", 60},
		{"20261023", nuri, "900000010", "정", "", 1, 1, 90, 1, "", 90},
		{"20261009", boram, "900000020", "정", "", 1, 1, 90, 1, "", 90},
		{"20261022", boram, "900000020", "정", "", 1, 1, 150, 1, "", 150},
	}
	mustImport(t, s, ImportRequest{Kind: "dispense", FileName: "조제현황.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": d})})
	initial := map[string][][]any{
		"시트1": {{"보관위치", "급여구분", "약품코드", "약품명", "재고단위", "기본수량", "사입단가", "낱알 재고", "재고금액"},
			{"B-2", "급여", "900000020", boram, "정", 30, 910, 23, 20930}},
		"시트2": {{"위치", "순번", "제품명", "제품규격", "수량", "매출단가", "매출금액"},
			{"B-2", 41, "보람정500mcg(PTP) 30T 보람제약", "30T", 4, 27300, 109200},
			{"", 99, "없는약품정 30T 어딘가제약", "30T", 1, 9999, 9999}},
	}
	res := mustImport(t, s, ImportRequest{Kind: "initial_stock", FileName: "초기재고.xlsx", DataBase64: xlsxB64(t, initial), BaselineDate: "2026-02-02"})
	if res.Unresolved != 1 || res.Products != 1 {
		t.Fatalf("초기재고: products=%d unresolved=%d %s", res.Products, res.Unresolved, res.Message)
	}
	hdr := []any{"주성분코드", "제품코드", "제품명", "업체명", "규격", "단위", "상한금액", "퇴장방지", "", "투여", "비고"}
	notice := map[string][][]any{
		"별지3_변경('26.11.01.시행)": {{"별지3"}, hdr,
			{"", "900000010", nuri, "누리제약(주)", "1", "정", 590, "", "", "내복", ""},
			{"", "900000020", boram, "보람제약(주)", "1", "정", 760, "", "", "내복", ""}},
		"변경대비표": {{"변경대비표"}, {},
			{"연번", "투여", "분류", "제품코드", "제품명", "제약사명", "규격", "단위", "상한금액", "변경 전", "변경 후", "비  고", "전문/일반", "시행일"},
			{1, "내복", 229, "900000010", nuri, "누리제약", "1", "정", 590, 640, 590, "상한금액", "전문", "2026-11-01"},
			{2, "내복", 229, "900000020", boram, "보람제약", "1", "정", 760, 910, 760, "상한금액", "전문", "2026-11-01"}},
	}
	nres := mustImport(t, s, ImportRequest{Kind: "notice", FileName: "약제 급여 목록 및 급여 상한금액표 일부개정고시(2026-999호)_별표1 목록표.xlsx", DataBase64: xlsxB64(t, notice)})
	if nres.NoticeID == 0 {
		t.Fatalf("고시 가져오기 실패: %+v", nres)
	}
}

func supplierID(t *testing.T, s *Service, name string) int64 {
	t.Helper()
	list, err := s.ListSuppliers(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range list {
		if v.Name == name {
			return v.ID
		}
	}
	t.Fatalf("거래처 %s 없음", name)
	return 0
}

func TestEndToEnd_Synthetic(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)

	// 재고: 누리 = 사입 390 − 조제 210 = 180, 보람 = 초기 23 + 120 + 사입 330 − 조제 240 = 233
	inv, err := s.Inventory("", true)
	if err != nil {
		t.Fatal(err)
	}
	stock := map[string]domain.Qty{}
	for _, r := range inv.Rows {
		stock[r.Code] = r.Qty
	}
	if stock["900000010"] != domain.Units(180) || stock["900000020"] != domain.Units(233) {
		t.Fatalf("재고: %v", stock)
	}

	// 나래도매만 2개월 기준으로 변경 (도매별 기준)
	narae := supplierID(t, s, "나래도매")
	r2 := domain.DefaultCompRule()
	r2.Lookback = 2
	if err := s.SaveRules([]RuleInput{{SupplierID: narae, ValidFrom: "2026-01-01", Rule: r2, Memo: "도매별 기준 예시"}}); err != nil {
		t.Fatal(err)
	}

	rk, err := s.Ranking("2026-11-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rk.Rows) != 2 {
		t.Fatalf("순위 행 %d", len(rk.Rows))
	}
	byCode := map[string]RankRow{}
	for _, r := range rk.Rows {
		byCode[r.Code] = r
	}
	rf := byCode["900000010"]
	// 인하폭 640−590 = 50 → 180 × 50 = 9,000
	if rf.Delta != 50 || rf.GrossLoss != 9000 || rf.Recommendation != "physical_review" {
		t.Fatalf("누리 순위: %+v", rf)
	}
	dx := byCode["900000020"]
	// 보람: 가람도매 3개월(8/1~10/31) 사입 240 × 30% = 72 < 233 → 실물 검토
	if dx.TotalDocQty != domain.Units(72) || dx.Delta != 150 {
		t.Fatalf("보람 순위: %+v", dx)
	}
	if rk.Rows[0].Code != "900000020" { // 233×150 = 34,950 > 9,000
		t.Fatalf("정렬: %s", rk.Rows[0].Code)
	}

	// 바코드: 다솜도매 사입분 (제조번호 LOT-1003)
	sc, err := s.RecordScan(ScanInput{EffectiveDate: "2026-11-01", ProductCode: "900000010", Raw: "(01)08809000000117(17)290531(10)LOT-1003(21)SN10000001"})
	if err != nil || sc.Status != "added" || sc.Unit.SupplierName != "다솜도매" || sc.Unit.Qty != domain.Units(30) {
		t.Fatalf("스캔1: %v %+v", err, sc)
	}
	dup, _ := s.RecordScan(ScanInput{EffectiveDate: "2026-11-01", ProductCode: "900000010", Raw: "(01)08809000000117(17)290531(10)LOT-1003(21)SN10000001"})
	if dup.Status != "duplicate" {
		t.Fatalf("중복 스캔: %+v", dup)
	}
	// 같은 제조번호를 가람도매도 공급했다는 근거 추가 → LOT-1001 은 확인 필요
	garam := supplierID(t, s, "가람도매")
	if _, err := s.AddLot(LotInput{Code: "900000010", SupplierID: garam, Lot: "LOT-1001", Expiry: "20280630", Qty: 30}); err != nil {
		t.Fatal(err)
	}
	amb, _ := s.RecordScan(ScanInput{EffectiveDate: "2026-11-01", ProductCode: "900000010", Raw: "01088090000001171728063010LOT-1001\x1d21SN10000002"})
	if amb.Status != "added" || amb.Unit.MatchStatus != "ambiguous" || len(amb.Unit.Candidates) != 2 {
		t.Fatalf("확인 필요 스캔: %+v", amb)
	}
	if err := s.ResolveScan(amb.Unit.ID, narae); err != nil {
		t.Fatal(err)
	}
	other, _ := s.RecordScan(ScanInput{EffectiveDate: "2026-11-01", ProductCode: "900000010", Raw: "(01)08809000000216(17)281130(10)LOT-2001(21)X1"})
	if other.Status != "other_product" {
		t.Fatalf("다른 약품 바코드: %+v", other)
	}

	d, err := s.ProductDetail("2026-11-01", "900000010")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Decision.Scanned {
		t.Fatal("스캔 반영 안 됨")
	}
	var dsPos *PositionView
	for i := range d.Positions {
		if d.Positions[i].SupplierName == "다솜도매" {
			dsPos = &d.Positions[i]
		}
	}
	// 다솜도매: 기간 사입 210 → 서류 63. 아직 다 찍지 않았다(180개 중 60개) → 안 찍은 120개는 FIFO 추정으로 채운다:
	// 보유 = max(찍은 30, min(추정 180, 30 + 120)) = 150 > 63 → 실물 유리 (예전에는 하나만 찍어도 30으로 보고 서류로 뒤집었다)
	if dsPos == nil || dsPos.DocQty != domain.Units(63) || dsPos.HeldUsed != domain.Units(150) || dsPos.Method != "physical" || dsPos.WindowFrom != "2026-08-01" {
		t.Fatalf("다솜도매 포지션: %+v", dsPos)
	}

	// 처리 결과 기록 → 완료 → 리포트
	n, err := s.ConfirmDecision("2026-11-01", "900000010")
	if err != nil || n == 0 {
		t.Fatalf("판정 확정: %d %v", n, err)
	}
	sts, _ := s.ListSettlements(SettlementFilter{EffectiveDate: "2026-11-01"})
	if err := s.SetSettlementStatus(sts[0].ID, "done"); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Report(SettlementFilter{})
	if err != nil || rep.Total.Count != len(sts) || rep.Total.Done != 1 {
		t.Fatalf("리포트: %+v %v", rep.Total, err)
	}

	// 실계수 입력 → 순위 재계산 (실계수 우선)
	if _, err := s.SaveCount(CountInput{Code: "900000020", Qty: 100, AsOf: "2026-10-31", Timing: "end"}); err != nil {
		t.Fatal(err)
	}
	rk, _ = s.Ranking("2026-11-01", true)
	for _, r := range rk.Rows {
		if r.Code == "900000020" && (r.Stock != domain.Units(100) || r.CountQty == nil || r.Estimate != domain.Units(233)) {
			t.Fatalf("실계수 반영: %+v", r)
		}
	}

	// 초기재고 미해결 행 → 사용자 지정
	un, _ := s.ListUnresolved()
	if len(un) != 1 {
		t.Fatalf("미해결 %d", len(un))
	}
	if err := s.ResolveUnresolved(un[0].ID, ""); err != nil {
		t.Fatal(err)
	}

	// 기준일 변경
	if err := s.SetBaseline("2026-03-02", "start"); err != nil {
		t.Fatal(err)
	}

	// 거래처 삭제: 사용 이력이 있으면 숨김
	if _, err := s.DeleteSupplier(garam); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListSuppliers(true)
	for _, v := range all {
		if v.ID == garam && !v.Deleted {
			t.Fatal("가람도매는 숨김 처리되어야 함")
		}
	}

	// 같은 기간 사입현황 재업로드 → 교체 (중복 없음)
	again := [][]any{{"사입현황 20261016 ~ 20261016"},
		{"일자", "약품명", "약품코드", "거래처", "구분", "단위수량", "수량", "단가"},
		{"20261016", "누리정", "900000010", "다솜도매(주)", "사입", 30, 7, 19200}}
	r, err := s.Import(ImportRequest{Kind: "purchase", FileName: "재업로드.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": again})})
	if err != nil || r.Replaced != 1 {
		t.Fatalf("기간 교체: %+v %v", r, err)
	}

	dash, err := s.Dashboard()
	if err != nil || len(dash.Upcoming) == 0 {
		t.Fatalf("대시보드: %v %+v", err, dash)
	}
	if _, err := s.Backup(); err != nil {
		t.Fatal(err)
	}
}

// realServiceExpected 는 실데이터 폴더의 expected_service.json (없으면 수용 검증 생략). 모든 항목은 선택:
//
//	{
//	  "effective_date": "2026-11-01",
//	  "ranking": [{"code": "<약품코드>", "stock": 0, "delta": 0, "gross_loss": 0, "total_doc_qty": 0, "recommendation": "document"}]
//	}
type realServiceExpected struct {
	EffectiveDate string `json:"effective_date"`
	Ranking       []struct {
		Code           string   `json:"code"`
		Stock          *float64 `json:"stock"`
		Delta          *float64 `json:"delta"`
		GrossLoss      *int64   `json:"gross_loss"`
		TotalDocQty    *float64 `json:"total_doc_qty"`
		Recommendation string   `json:"recommendation"`
	} `json:"ranking"`
}

// 실데이터 폴더(환경변수)가 있을 때만 실행. 기대값은 코드에 두지 않고 그 폴더의 expected_service.json 에 둔다.
func TestRealData_Pipeline(t *testing.T) {
	dir := os.Getenv("DPR_REALDATA")
	if dir == "" {
		t.Skip("DPR_REALDATA 미설정")
	}
	var exp realServiceExpected
	if data, err := os.ReadFile(filepath.Join(dir, "expected_service.json")); err == nil {
		if err := json.Unmarshal(data, &exp); err != nil {
			t.Fatalf("expected_service.json: %v", err)
		}
	}
	eff := exp.EffectiveDate
	if eff == "" {
		eff = "2026-11-01"
	}
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, f := range []struct{ kind, file string }{{"purchase", "purchase.xlsx"}, {"dispense", "dispense.xlsx"}, {"initial_stock", "initial.xlsx"}, {"notice", "notice.xlsx"}} {
		res := mustImport(t, s, ImportRequest{Kind: f.kind, Path: filepath.Join(dir, f.file)})
		t.Logf("[%s] %s (%dms)", f.kind, res.Message, res.DurationMs)
	}
	rk, err := s.Ranking(eff, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s 보유 인하 품목 %d / 전체 %d, 손실 %d원, 회수 %d원, 순손실 %d원", eff, rk.Totals.Held, rk.Totals.Items, rk.Totals.GrossLoss, rk.Totals.Recovery, rk.Totals.NetLoss)
	for i, r := range rk.Rows {
		if i < 10 {
			t.Logf("  %2d. %s %s 재고 %s × %.1f = %d원, 서류 %s, %s", i+1, r.Code, r.Name, r.Stock, r.Delta, r.GrossLoss, r.TotalDocQty, r.Recommendation)
		}
	}
	byCode := map[string]*RankRow{}
	for i := range rk.Rows {
		byCode[rk.Rows[i].Code] = &rk.Rows[i]
	}
	for _, e := range exp.Ranking {
		r := byCode[e.Code]
		if r == nil {
			t.Errorf("%s: 순위에 없음", e.Code)
			continue
		}
		if (e.Stock != nil && r.Stock != domain.QtyFromFloat(*e.Stock)) ||
			(e.Delta != nil && r.Delta != *e.Delta) ||
			(e.GrossLoss != nil && r.GrossLoss != *e.GrossLoss) ||
			(e.TotalDocQty != nil && r.TotalDocQty != domain.QtyFromFloat(*e.TotalDocQty)) ||
			(e.Recommendation != "" && r.Recommendation != e.Recommendation) {
			t.Errorf("%s: 기대와 다름: %+v", e.Code, *r)
		}
	}
	un, _ := s.ListUnresolved()
	t.Logf("초기재고 미해결 %d행", len(un))
	dl, _ := s.HeldDeletions(eff)
	t.Logf("보유 중인 삭제 품목 %d개", len(dl))
}

func TestGTINMasterImport(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	csv := "표준코드,제품코드(개정후),한글상품명,제품총수량\n8809000000117,900000010,누리정250마이크로그램,30\n"
	res, err := s.Import(ImportRequest{Kind: "gtin", FileName: "약가마스터_의약품표준코드.csv", DataBase64: base64.StdEncoding.EncodeToString([]byte(csv))})
	if err != nil || res.Rows != 1 {
		t.Fatalf("표준코드 가져오기: %+v %v", res, err)
	}
	r, err := s.RecordScan(ScanInput{EffectiveDate: "2026-11-01", Raw: "(01)08809000000117(17)290531(10)LOT-1003(21)A1"})
	if err != nil || r.Status != "added" || r.Unit.Code != "900000010" || r.Unit.Qty != domain.Units(30) {
		t.Fatalf("공식 매핑으로 스캔: %+v %v", r, err)
	}
}

func TestRangeGuard(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rows := [][]any{{"사입현황 20260202 ~ 20260331"}, {"일자", "약품명", "약품코드", "거래처", "구분", "단위수량", "수량", "단가"}}
	for i := 0; i < 30; i++ {
		rows = append(rows, []any{"20260315", "약품", 900000100 + i*10, "가람도매", "사입", 30, 1, 1200})
	}
	mustImport(t, s, ImportRequest{Kind: "purchase", FileName: "전체.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": rows})})
	one := [][]any{{"사입현황 20260202 ~ 20260331"}, {"일자", "약품명", "약품코드", "거래처", "구분", "단위수량", "수량", "단가"}, {"20260315", "약품", "900000190", "가람도매", "사입", 30, 1, 1200}}
	res, err := s.Import(ImportRequest{Kind: "purchase", FileName: "약품하나.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": one})})
	if err != nil || !res.NeedsConfirm {
		t.Fatalf("안전장치가 동작해야 함: %+v %v", res, err)
	}
	var n int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM purchase_line").Scan(&n)
	if n != 30 {
		t.Fatalf("기존 기록이 보존되어야 함: %d", n)
	}
	res, err = s.Import(ImportRequest{Kind: "purchase", FileName: "약품하나.xlsx", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": one}), Force: true})
	if err != nil || res.NeedsConfirm || res.Replaced != 30 {
		t.Fatalf("강제 교체: %+v %v", res, err)
	}
}
