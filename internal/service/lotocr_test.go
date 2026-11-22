package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"testing"

	"dpr/internal/platform"
)

// 합성 자료: 이팜 사입현황 화면 캡처를 Windows OCR 이 읽은 모양 (단어·위치). 숫자를 한글로 읽은 경우를 섞었다.
func fakeOCR(words ...[]any) platform.OCRResult {
	var r platform.OCRResult
	r.Width, r.Height = 1100, 200
	for _, w := range words {
		r.Lines = append(r.Lines, platform.OCRLine{Text: w[0].(string), Words: []platform.OCRWord{{Text: w[0].(string), X: w[1].(float64), Y: w[2].(float64), W: w[3].(float64), H: 13}}})
	}
	return r
}

func tinyPNG(t *testing.T) string {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 400, 100))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestLotOCRParseAndAlign(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s) // 900000010 사입: 2026-03-25 가람도매 30정 등 (가상 값)

	// 화면: 머리 (수량·총수량 머리는 OCR 이 놓침), 사입 2줄 + 반품 1줄 + 재고조정 1줄. 좌표는 원본(=OCR) 기준, 3배 확대본 기준이면 scale 로 나뉜다.
	res := fakeOCR(
		[]any{"차입", 10.0, 8.0, 30.0}, []any{"일자", 42.0, 8.0, 24.0}, []any{"구문", 115.0, 8.0, 22.0}, []any{"거래처", 165.0, 8.0, 34.0},
		[]any{"단가", 375.0, 8.0, 22.0}, []any{"합계액", 545.0, 8.0, 34.0}, []any{"부가세", 655.0, 8.0, 34.0}, []any{"유효기간", 725.0, 8.0, 50.0}, []any{"제조변호", 825.0, 8.0, 50.0},
		// 1) 2026-03-25 사입 (주)가람도매 1.00 / 30.00 유효 20280917 제조 AB12口4 (0 을 口 로 읽음)
		[]any{"2026-03-25", 10.0, 34.0, 76.0}, []any{"사입", 115.0, 34.0, 20.0}, []any{"(주)", 160.0, 34.0, 26.0}, []any{"가람도매", 190.0, 34.0, 50.0},
		[]any{"1.00", 305.0, 34.0, 24.0}, []any{"19,200", 380.0, 34.0, 36.0}, []any{"30.00", 470.0, 34.0, 30.0}, []any{"19,200", 550.0, 34.0, 36.0},
		[]any{"포함", 655.0, 34.0, 22.0}, []any{"2028口917", 725.0, 34.0, 50.0}, []any{"AB12口4", 825.0, 34.0, 46.0},
		// 2) 반품 → 뺀다
		[]any{"2026-02-10", 10.0, 54.0, 76.0}, []any{"반품", 115.0, 54.0, 20.0}, []any{"(주)가람도매", 160.0, 54.0, 70.0}, []any{"-2.00", 305.0, 54.0, 24.0},
		// 3) 날짜 앞의 20 을 '꼬' 로 읽은 줄, 같은 제조번호인데 한 글자 틀림, 유효기간은 못 읽음
		[]any{"꼬26-01-05", 10.0, 74.0, 76.0}, []any{"사입", 115.0, 74.0, 20.0}, []any{"(주)가람도매", 160.0, 74.0, 70.0},
		[]any{"1.00", 305.0, 74.0, 24.0}, []any{"30.00", 470.0, 74.0, 30.0}, []any{"AB1204", 825.0, 74.0, 46.0},
		// 4) 재고조정 → 뺀다
		[]any{"2025-12-29", 10.0, 94.0, 76.0}, []any{"재고조정재고정리", 115.0, 94.0, 90.0}, []any{"18.00", 305.0, 94.0, 30.0},
	)
	old := ocrRunner
	ocrRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		out := make([]platform.OCRResult, len(paths))
		for i := range paths {
			out[i] = res
		}
		return out, nil
	}
	defer func() { ocrRunner = old }()

	// 작은 이미지 → 화면 캡처로 보고 3배 확대 → 좌표를 3배로 본다 (가짜 결과는 확대본 좌표로 해석된다)
	got, err := s.ReadLotPhoto(context.Background(), LotPhotoRequest{ImageBase64: tinyPNG(t), Code: "900000010"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != "screenshot" || got.Skipped != 2 || len(got.Rows) != 2 {
		t.Fatalf("결과: mode=%s skipped=%d rows=%+v", got.Mode, got.Skipped, got.Rows)
	}
	garam := supplierID(t, s, "가람도매")
	r0, r1 := got.Rows[0], got.Rows[1]
	if r0.Date != "2026-03-25" || r0.SupplierID != garam || r0.Expiry != "2028-09-17" || r0.Lot != "AB1204" || r0.Qty != 30 {
		t.Fatalf("1줄: %+v", r0)
	}
	if r0.Matched != "date" {
		t.Fatalf("1줄은 사입현황과 같은 날짜로 맞춰야 함: %+v", r0)
	}
	// 3줄: 2026-01-05 (꼬 → 20), 제조번호가 1줄과 같으므로 유효기간도 같다고 채움 (확인 필요 표시)
	if r1.Date != "2026-01-05" || r1.Lot != "AB1204" || r1.Expiry != "2028-09-17" || !isUnsure(r1, "expiry") || r1.Qty != 30 {
		t.Fatalf("2줄: %+v", r1)
	}
}

func TestLotOCRHelpers(t *testing.T) {
	for in, want := range map[string]string{"2026-1 0-29": "2026-10-29", "꼬19-10-30": "2019-10-30", "20271120": "2027-11-20", "2O21O7O2": "2021-07-02", "0": "", "": ""} {
		if got, _ := parseOCRDate(in, false); got != want {
			t.Errorf("날짜 %q → %q, 기대 %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"ZZ55님日": "ZZ5598", `\/09999`: "V09999", "y이777": "Y01777", "31007-2": "31007-2"} {
		if got, _ := cleanLot(in); got != want {
			t.Errorf("제조번호 %q → %q, 기대 %q", in, got, want)
		}
	}
	if v, ok := parseOCRNumber("1 2口.口口"); !ok || v != 120 {
		t.Errorf("수량: %v %v", v, ok)
	}
	if textSimilarity("가람도매", "가람으매") < 0.5 {
		t.Error("비슷한 도매 이름")
	}
}

// 제조번호 맞추기는 흐리게 읽은 칸만, 유효기간이 어긋나지 않을 때만 한다.
// 실제 제조번호는 한 글자만 다른 것이 흔하다(LOT-1001·LOT-1002) — 확실하게 읽은 값을 합치면 서로 다른 도매의 로트가 섞인다.
func TestUnifyLotsKeepsSureLots(t *testing.T) {
	rows := []LotPhotoRow{
		{Lot: "LOT-1001", Expiry: "2028-06-30"},
		{Lot: "LOT-1001", Expiry: "2028-06-30"},
		{Lot: "LOT-1002", Expiry: "2029-02-28"},                          // 확실 — 한 글자 차이지만 다른 로트
		{Lot: "LOT-10O1", Expiry: "2028-06-30", Unsure: []string{"lot"}}, // 흐림 + 같은 유효기간 → LOT-1001 로
		{Lot: "LOT-1007", Expiry: "2029-02-28", Unsure: []string{"lot"}}, // 흐림 + LOT-1001 과 유효기간 다름 → LOT-1002 쪽만 후보
		{Lot: "LOT-1009", Expiry: "2030-01-31", Unsure: []string{"lot"}}, // 흐림 + 어느 쪽과도 유효기간이 다름 → 그대로
	}
	unifyLots(rows)
	want := []string{"LOT-1001", "LOT-1001", "LOT-1002", "LOT-1001", "LOT-1002", "LOT-1009"}
	for i, w := range want {
		if rows[i].Lot != w {
			t.Errorf("%d번째 줄: %q, 기대 %q", i, rows[i].Lot, w)
		}
	}
	if isUnsure(rows[2], "lot") {
		t.Error("확실한 줄에 표시가 생김")
	}
	for _, i := range []int{3, 4, 5} {
		if !isUnsure(rows[i], "lot") {
			t.Errorf("%d번째 줄: 맞춘 칸도 '확인 필요'로 남아야 함", i)
		}
	}
}
