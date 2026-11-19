package domain

// 테스트 데이터는 합성 데이터입니다.

import (
	"testing"
	"time"
)

func TestNormalizeProductCode(t *testing.T) {
	cases := map[string]string{
		"987654400":   "987654400", // 9자리 EDI
		"12345670":    "012345670", // 엑셀 숫자 변환으로 앞 0 소실
		"698765430.0": "698765430",
		"12345670.0":  "012345670",
		"2000461":     "2000461", // 이팜 내부코드
		" 987654320 ": "987654320",
		"":            "",
		"abc":         "",
		"9.8765432E8": "987654320",
		"12.5":        "",
	}
	for in, want := range cases {
		if got := NormalizeProductCode(in); got != want {
			t.Errorf("NormalizeProductCode(%q) = %q, want %q", in, got, want)
		}
	}
	if !IsEDICode("987654400") || IsEDICode("2000461") {
		t.Error("IsEDICode")
	}
}

func TestNormalizeSupplierName(t *testing.T) {
	cases := map[string]string{
		"(주) 가람도매":    "가람도매",
		"나래도매(주)":     "나래도매",
		"다솜도매 유한책임회사": "다솜도매",
		"(유)라온도매":     "라온도매",
		"(주)마루도매네트워크": "마루도매네트워크",
		"바다메디칼 주식회사":  "바다메디칼",
	}
	for in, want := range cases {
		if got := NormalizeSupplierName(in); got != want {
			t.Errorf("NormalizeSupplierName(%q) = %q, want %q", in, got, want)
		}
	}
	if GuessSupplierKind("약국간거래") != "other" || GuessSupplierKind("가람도매") != "wholesaler" {
		t.Error("GuessSupplierKind")
	}
}

func TestProductNameKey(t *testing.T) {
	efarm := ProductNameKey("누리정500마이크로그램(누리성분)_(0.5mg/1정)", false)
	wholesale := ProductNameKey("누리정500mcg(PTP) 30T 가나제약", true)
	if efarm != "누리정500mcg" || wholesale != efarm {
		t.Fatalf("키 불일치: %q vs %q", efarm, wholesale)
	}
	a := ProductNameKey("[파리에트정20mg(PTP) 56T 한국콜마", true)
	b := ProductNameKey("파리에트정20밀리그람(라베프라졸나트륨)_(20mg/1정)", false)
	if a != b {
		t.Fatalf("파리에트 키 불일치: %q vs %q", a, b)
	}
	if n, ok := ParsePackSize("30T"); !ok || n != 30 {
		t.Fatalf("ParsePackSize 30T = %v %v", n, ok)
	}
	for spec, want := range map[string]float64{"20P": 20, "28S": 28, "10S*5EA": 50, "20T*16EA": 320, "10ML*30P": 30, "1G*50P": 50, "120ML": 120, "1PEN": 1} {
		if n, ok := ParsePackSize(spec); !ok || n != want {
			t.Errorf("ParsePackSize %s = %v %v, want %v", spec, n, ok, want)
		}
	}
	if n, ok := ParsePackSize("60C"); !ok || n != 60 {
		t.Fatalf("ParsePackSize 60C = %v %v", n, ok)
	}
	if s := Similarity("누리정500mcg", "누리정500mcg"); s != 1 {
		t.Fatalf("Similarity identical = %v", s)
	}
}

func TestQty(t *testing.T) {
	if QtyFromFloat(47.5).String() != "47.5" || Units(210).String() != "210" {
		t.Fatal("Qty 표기")
	}
	if QtyFromFloat(0.25) != 250 {
		t.Fatal("0.25 → 250 milli")
	}
}

func TestParseGS1(t *testing.T) {
	// 괄호 표기 판독 문자열 (제조번호·유효기간·일련번호는 가상 값)
	g, ok := ParseGS1("(01)08809876543213(17)300615(10)31007-2(21)31007-2000123")
	if !ok || g.GTIN != "08809876543213" || g.Expiry != "2030-06-15" || g.Lot != "31007-2" || g.Serial != "31007-2000123" {
		t.Fatalf("괄호 표기 파싱 실패: %+v", g)
	}
	if len(g.Warnings) != 0 {
		t.Fatalf("경고 없어야 함: %v", g.Warnings)
	}
	// 스캐너 원문 (GS 구분자, ]d2 접두)
	raw := "]d201088098765432131730061510" + "31007-2" + "\x1d" + "21" + "31007-2000123"
	g2, ok := ParseGS1(raw)
	if !ok || g2.Lot != "31007-2" || g2.Serial != "31007-2000123" || g2.Expiry != "2030-06-15" {
		t.Fatalf("원문 파싱 실패: %+v", g2)
	}
	// GS 를 '~' 로 보내는 스캐너
	g3, ok := ParseGS1("0108809876543213173006151031007-2~2131007-2000117")
	if !ok || g3.Serial != "31007-2000117" {
		t.Fatalf("대체 구분자 파싱 실패: %+v", g3)
	}
	// 일련번호가 앞에 오는 순서
	g4, ok := ParseGS1("010880987654321321ABC123\x1d1730061510LOT9")
	if !ok || g4.Serial != "ABC123" || g4.Lot != "LOT9" || g4.Expiry != "2030-06-15" {
		t.Fatalf("순서 변형 파싱 실패: %+v", g4)
	}
	if _, ok := ParseGS1("8809876543213"); ok {
		t.Fatal("EAN-13 단독은 GS1 요소 문자열이 아님")
	}
	if !GTINCheckOK("08809876543213") || GTINCheckOK("08809876543214") {
		t.Fatal("체크디지트")
	}
	if EDIPrefixFromGTIN("08809876543213") != "98765432" || EDIPrefixFromGTIN("08809876544012") != "98765440" {
		t.Fatal("EDI 접두 추출")
	}
	if d, ok := ParseYYMMDD("290400"); !ok || d.Day() != 30 {
		t.Fatal("DD=00 → 말일")
	}
}

func TestDocumentQty(t *testing.T) {
	r := DefaultCompRule()
	// 예시: 가람도매 300 × 30% = 90, 나래도매 (100+50) × 30% = 45
	if q := DocumentQty(r, Units(300), 0); q != Units(90) {
		t.Fatalf("300×30%% = %v", q)
	}
	if q := DocumentQty(r, Units(150), 0); q != Units(45) {
		t.Fatalf("150×30%% = %v", q)
	}
	// float 함정: 90 × 0.3 = 26.999… → 정수 연산이라 27
	if q := DocumentQty(r, Units(90), 0); q != Units(27) {
		t.Fatalf("90×30%% = %v", q)
	}
	// 반품 차감
	if q := DocumentQty(r, Units(100), Units(20)); q != Units(24) {
		t.Fatalf("(100-20)×30%% = %v", q)
	}
	r.NetReturns = false
	if q := DocumentQty(r, Units(100), Units(20)); q != Units(30) {
		t.Fatalf("반품 미차감 = %v", q)
	}
	// 반올림 방식: 25 × 30% = 7.5
	r = DefaultCompRule()
	if DocumentQty(r, Units(25), 0) != Units(7) {
		t.Fatal("floor")
	}
	r.Rounding = "round"
	if DocumentQty(r, Units(25), 0) != Units(8) {
		t.Fatal("round")
	}
	r.Rounding = "ceil"
	if DocumentQty(r, Units(24), 0) != Units(8) { // 7.2 → 8
		t.Fatal("ceil")
	}
}

func TestCompWindow(t *testing.T) {
	eff := MustDate("2026-11-01")
	check := func(name string, r CompRule, base time.Time, wantFrom, wantTo string) {
		t.Helper()
		f, to := CompWindow(r, base, nil)
		if FormatDate(f) != wantFrom || FormatDate(to) != wantTo {
			t.Errorf("%s: got %s~%s want %s~%s", name, FormatDate(f), FormatDate(to), wantFrom, wantTo)
		}
	}
	r := DefaultCompRule() // 달력 월 3개월
	check("달력 3개월", r, eff, "2026-08-01", "2026-10-31")
	r.Lookback = 2
	check("달력 2개월", r, eff, "2026-09-01", "2026-10-31")
	check("달력 2개월, 월중 시행", r, MustDate("2026-11-11"), "2026-09-01", "2026-10-31")
	r.CutoffDay = 15
	check("15일 마감 2주기", r, eff, "2026-08-16", "2026-10-15")
	check("15일 마감 2주기, 11/20 시행", r, MustDate("2026-11-20"), "2026-09-16", "2026-11-15")
	r.ExtendToAnchor = true
	check("15일 마감 + 기준일 전날까지", r, eff, "2026-08-16", "2026-10-31")
	r = DefaultCompRule()
	r.CutoffDay = 31 // 30일까지 있는 달은 말일로
	check("31일 마감 = 말일", r, eff, "2026-08-01", "2026-10-31")
	r = CompRule{RateBP: 3000, WindowMode: "rolling_month", Lookback: 2, Anchor: "effective_date", Rounding: "floor"}
	check("롤링 2개월", r, MustDate("2026-11-20"), "2026-09-20", "2026-11-19")
	r = CompRule{RateBP: 3000, WindowMode: "rolling_day", Lookback: 60, Anchor: "effective_date", Rounding: "floor"}
	check("롤링 60일", r, eff, "2026-09-02", "2026-10-31")
	// 고시일 기준
	r = DefaultCompRule()
	r.Anchor = "notice_date"
	notice := MustDate("2026-10-27")
	f, to := CompWindow(r, eff, &notice)
	if FormatDate(f) != "2026-07-01" || FormatDate(to) != "2026-09-30" {
		t.Errorf("고시일 기준: %s~%s", FormatDate(f), FormatDate(to))
	}
	if FormatDate(NextSettlementDate(eff, 0)) != "2026-11-30" || FormatDate(NextSettlementDate(MustDate("2026-11-26"), 25)) != "2026-12-25" {
		t.Error("NextSettlementDate")
	}
	if err := DefaultCompRule().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFIFOAttribution(t *testing.T) {
	lots := []PurchaseLot{
		{Date: "2026-10-15", SupplierID: 1, Qty: Units(300)}, // 가람도매
		{Date: "2026-05-10", SupplierID: 2, Qty: Units(100)}, // 나래도매
		{Date: "2026-03-20", SupplierID: 3, Qty: Units(50)},  // 다솜도매
	}
	got := FIFOAttribution(Units(200), lots)
	if got[1] != Units(200) || got[2] != 0 {
		t.Fatalf("최근 사입 우선 배분: %v", got)
	}
	got = FIFOAttribution(Units(420), lots)
	if got[1] != Units(300) || got[2] != Units(100) || got[3] != Units(20) || got[0] != 0 {
		t.Fatalf("배분: %v", got)
	}
	got = FIFOAttribution(Units(500), lots)
	if got[0] != Units(50) {
		t.Fatalf("기록 이전 재고(초기재고) = %v", got[0])
	}
}

func TestDecide_실물검토(t *testing.T) {
	ga, na := DefaultCompRule(), DefaultCompRule()
	na.Lookback = 2
	positions := []SupplierPosition{
		{SupplierID: 1, SupplierName: "가람도매", Rule: &ga, Purchased: Units(300)},
		{SupplierID: 2, SupplierName: "나래도매", Rule: &na, Purchased: Units(150)},
	}
	// 1단계: 재고 240 > 서류보상 90+45=135 → 실물보상 검토
	d := Decide(Units(240), 50, positions)
	if d.Recommendation != "physical_review" || d.TotalDocQty != Units(135) {
		t.Fatalf("품목 판정: %+v", d)
	}
	// 2단계: 바코드 결과 가람 200 (> 서류 90 → 실물), 나래 40 (≤ 서류 45 → 서류)
	h1, h2 := Units(200), Units(40)
	positions[0].ScannedHeld, positions[1].ScannedHeld = &h1, &h2
	d = Decide(Units(240), 50, positions)
	if d.Positions[0].Method != "physical" || d.Positions[1].Method != "document" {
		t.Fatalf("도매 판정: %+v", d.Positions)
	}
	// 회수: 200(실물) + 45(서류) = 245 → 245×50 = 12,250 / 손실 240×50 = 12,000 → 순이익 250
	if d.RecoverQty != Units(245) || d.GrossLoss != 12000 || d.Recovery != 12250 || d.NetLoss != -250 {
		t.Fatalf("금액: %+v", d)
	}
}

func TestDecide_서류유리(t *testing.T) {
	r := DefaultCompRule()
	// 재고 120 ≤ 서류보상 500×30% = 150 → 서류보상
	positions := []SupplierPosition{{SupplierID: 9, SupplierName: "라온도매", Rule: &r, Purchased: Units(500)}}
	d := Decide(Units(120), 100, positions)
	if d.Recommendation != "document" || d.TotalDocQty != Units(150) {
		t.Fatalf("서류 판정: %+v", d)
	}
	none := Decide(Units(0), 100, nil)
	if none.Recommendation != "no_stock" {
		t.Fatal("재고 없음")
	}
	if Decide(Units(10), 0, nil).Recommendation != "no_loss" {
		t.Fatal("인하폭 0")
	}
}

func TestUnitFactorAndDelta(t *testing.T) {
	if c := DetectUnitFactor(500, 500); c.Factor != 1 || c.Status != "ok" {
		t.Fatalf("동일 단위: %+v", c)
	}
	if c := DetectUnitFactor(30000, 500); c.Factor != 60 || c.Status != "auto_pack" {
		t.Fatalf("60관 1상자: %+v", c)
	}
	if c := DetectUnitFactor(150, 100); c.Status != "check" {
		t.Fatalf("애매한 비율: %+v", c)
	}
	d := ComputePriceDelta(500, 0, 500, 480, 1)
	if d.Delta != 20 || d.PriceSource != "purchase" {
		t.Fatalf("사입가 기준 인하폭: %+v", d)
	}
	d = ComputePriceDelta(0, 0, 2000, 1850, 1)
	if d.Delta != 150 || d.PriceSource != "notice" {
		t.Fatalf("고시가 대체: %+v", d)
	}
	if ComputePriceDelta(400, 0, 500, 480, 1).Delta != 0 {
		t.Fatal("싸게 산 경우 손실 0")
	}
}

func TestMatchLot(t *testing.T) {
	recs := []LotRecord{
		{SupplierID: 1, Lot: "31007-2", Expiry: "2030-06-15"},
		{SupplierID: 2, Lot: "30112-1", Expiry: "2029-11-20"},
		{SupplierID: 2, Lot: "31001-1", Expiry: "2030-01-10"},
		{SupplierID: 3, Lot: "30112-1", Expiry: "2029-11-20"},
	}
	if m := MatchLot("31007-2", "2030-06-15", recs); m.Status != "unique" || m.SupplierIDs[0] != 1 {
		t.Fatalf("unique: %+v", m)
	}
	if m := MatchLot("30112-1", "2029-11-20", recs); m.Status != "ambiguous" || len(m.SupplierIDs) != 2 {
		t.Fatalf("ambiguous: %+v", m)
	}
	if m := MatchLot("99999", "", recs); m.Status != "none" {
		t.Fatalf("none: %+v", m)
	}
	if m := MatchLot("310072", "2030-06-15", recs); m.Status != "unique" {
		t.Fatalf("하이픈 차이 허용: %+v", m)
	}
	if m := MatchLot("31007-2", "2031-01-01", recs); m.Status != "none" {
		t.Fatalf("유효기간 불일치: %+v", m)
	}
}

func TestParseFlexibleDate(t *testing.T) {
	cases := map[string]string{"20260302": "2026-03-02", "2026-11-01 00:00:00": "2026-11-01", "2026.03.02": "2026-03-02", "46327": "2026-11-01"}
	for in, want := range cases {
		if got, ok := ParseFlexibleDate(in); !ok || got != want {
			t.Errorf("ParseFlexibleDate(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := ParseFlexibleDate("합계"); ok {
		t.Error("문자열은 날짜 아님")
	}
	if d, _ := ShortKoreanDate("26", "11", "01"); d != "2026-11-01" {
		t.Error("ShortKoreanDate")
	}
}
