package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"dpr/internal/platform"
)

// 두 엔진을 함께 쓰는 ReadLotPhoto (가상 자료). 원본 그림 좌표로 줄을 만들고,
// Windows 쪽 가짜 결과는 화면 캡처 확대(3배) 좌표로, PaddleOCR 쪽은 원본 좌표로 돌려준다.
func engineOCR(rows [][]string, scale float64) platform.OCRResult {
	xs := []float64{4, 60, 84, 150, 178, 208, 240, 272, 300, 344}
	var words [][]any
	for i, cells := range rows {
		y := 4 + float64(i)*14
		for j, c := range cells {
			if c == "" {
				continue
			}
			w := float64(len([]rune(c))) * 4.2
			words = append(words, []any{c, xs[j] * scale, y * scale, w * scale})
		}
	}
	r := fakeOCR(words...)
	for i := range r.Lines {
		for j := range r.Lines[i].Words {
			r.Lines[i].Words[j].H = 9 * scale
		}
	}
	r.Width, r.Height = int(400*scale), int(100*scale)
	return r
}

func withEngines(t *testing.T, win, paddle func() ([]platform.OCRResult, error)) {
	t.Helper()
	oldW, oldP := ocrRunner, paddleRunner
	t.Cleanup(func() { ocrRunner, paddleRunner = oldW, oldP })
	ocrRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		res, err := win()
		if err != nil {
			return nil, err
		}
		out := make([]platform.OCRResult, len(paths))
		for i := range paths {
			out[i] = res[0]
		}
		return out, nil
	}
	paddleRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) { return paddle() }
}

var (
	engRow1   = []string{"2026-10-16", "사입", "(주)가람도매", "4.00", "19,200", "120.00", "76,800", "포함", "20290531", "0031201"}
	engRow2W  = []string{"2026-09-01", "사입", "(주)나래도매", "5.00", "19,200", "150.00", "96,000", "포함", "20280630", "0052209"}
	engRow2P  = []string{"2026-09-01", "사입", "(주)나래도매", "5.00", "19,200", "150.00", "96,000", "포함", "20280630", "0052289"}
	engReturn = []string{"2026-08-07", "반품", "(주)다솜도매", "-1.00", "19,200", "-30.00", "-19,200", "비과세", "20290531", "0031201"}
	engRow4   = []string{"2026-05-14", "사입", "(주)다솜도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280331", "0045107"}
)

func TestLotOCRTwoEngines(t *testing.T) {
	s := lotTestService(t)
	// Windows 는 넷째 줄을 놓쳤고 둘째 줄 제조번호를 PaddleOCR 과 다르게 읽었다
	win := engineOCR([][]string{engRow1, engRow2W, engReturn, nil}, 3)
	pad := engineOCR([][]string{engRow1, engRow2P, engReturn, engRow4}, 1)
	pad.Engine = platform.PaddleEngine
	withEngines(t,
		func() ([]platform.OCRResult, error) { return []platform.OCRResult{win}, nil },
		func() ([]platform.OCRResult, error) { return []platform.OCRResult{pad}, nil })
	got, err := s.ReadLotPhoto(context.Background(), LotPhotoRequest{ImageBase64: tinyPNG(t), Code: "900000010"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("rows=%v skipped=%d note=%q", lotRowTexts(got.Rows), got.Skipped, got.Note)
	if len(got.Rows) != 3 || got.Skipped != 1 {
		t.Fatalf("줄 3개(반품 1줄 뺌)여야 함: %v skipped=%d", lotRowTexts(got.Rows), got.Skipped)
	}
	r1, r2, r4 := got.Rows[0], got.Rows[1], got.Rows[2]
	if len(r1.Unsure) != 0 || r1.Alt != nil || r1.Lot != "0031201" {
		t.Errorf("두 엔진이 같게 읽은 줄은 확인 필요가 없어야 함: %+v", r1)
	}
	if r2.Lot != "0052209" || r2.Alt["lot"] != "0052289" || !isUnsure(r2, "lot") || isUnsure(r2, "expiry") {
		t.Errorf("제조번호만 갈린 줄: 바탕 값 + 다른 후보 + 제조번호만 확인 필요여야 함: %+v", r2)
	}
	if r4.Lot != "0045107" || !slices.Equal(r4.Unsure, lotCompareFields) {
		t.Errorf("한 엔진만 찾은 줄은 모든 칸 확인 필요: %+v", r4)
	}
}

// Windows 글자 인식이 안 되는 PC: PaddleOCR 만으로 읽고 안내한다. 둘 다 안 되면 Windows 쪽 오류를 돌려준다.
func TestLotOCRPaddleOnly(t *testing.T) {
	s := lotTestService(t)
	pad := engineOCR([][]string{engRow1, engRow2P, engReturn, engRow4}, 1)
	winErr := errors.New("windows ocr 없음")
	withEngines(t,
		func() ([]platform.OCRResult, error) { return nil, winErr },
		func() ([]platform.OCRResult, error) { return []platform.OCRResult{pad}, nil })
	got, err := s.ReadLotPhoto(context.Background(), LotPhotoRequest{ImageBase64: tinyPNG(t), Code: "900000010"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 3 || got.Skipped != 1 || !strings.Contains(got.Note, "PaddleOCR") {
		t.Fatalf("PaddleOCR 만으로 3줄 + 안내: %v skipped=%d note=%q", lotRowTexts(got.Rows), got.Skipped, got.Note)
	}
	withEngines(t,
		func() ([]platform.OCRResult, error) { return nil, winErr },
		func() ([]platform.OCRResult, error) { return nil, platform.ErrPaddleUnavailable })
	if _, err := s.ReadLotPhoto(context.Background(), LotPhotoRequest{ImageBase64: tinyPNG(t), Code: "900000010"}); !errors.Is(err, winErr) {
		t.Fatalf("둘 다 안 되면 Windows 오류: %v", err)
	}
}

// 사입현황(DB)으로 바로잡은 칸(날짜·도매·수량)에는 다른 후보를 남기지 않는다
func TestLotOCRAltDroppedAfterAlign(t *testing.T) {
	r := LotPhotoRow{Date: "2026-03-25", Qty: 30, Lot: "A1", Alt: map[string]string{"date": "2026-03-26", "qty": "3", "lot": "A7", "supplier": "나래도매", "supplierId": "2"}}
	for _, k := range []string{"date", "supplier", "supplierId", "qty"} {
		delete(r.Alt, k) // alignWithPurchases 의 apply 와 같은 처리
	}
	dropSameAlt(&r)
	if len(r.Alt) != 1 || r.Alt["lot"] != "A7" {
		t.Fatalf("제조번호 후보만 남아야 함: %v", r.Alt)
	}
	r.Lot = "A7"
	dropSameAlt(&r)
	if r.Alt != nil {
		t.Fatalf("지금 값과 같은 후보는 지움: %v", r.Alt)
	}
}

// 두 엔진이 같은 값을 읽어도, 둘 다 '확인 필요'로 둔 칸(같은 판독 규칙이 열을 약하게 짐작함)은 그대로 둔다
func TestCompareKeepsSharedDoubt(t *testing.T) {
	a := LotPhotoRow{Date: "2026-10-16", Expiry: "2025-03-31", Lot: "0031201", Unsure: []string{"expiry"}}
	b := LotPhotoRow{Date: "2026-10-16", Expiry: "2025-03-31", Lot: "0031201", Unsure: []string{"expiry"}}
	got := compareLotRow(a, b)
	if !isUnsure(got, "expiry") || isUnsure(got, "lot") {
		t.Fatalf("둘 다 흐리게 본 유효기간은 확인 필요로 남아야 함: %+v", got)
	}
	b.Unsure = nil
	if got := compareLotRow(a, b); isUnsure(got, "expiry") {
		t.Fatalf("한쪽이라도 확실하게 읽었고 값이 같으면 믿음: %+v", got)
	}
}

// 두 엔진이 다르게 읽은 제조번호는 옆 줄의 확실한 제조번호(연속 로트)로 맞추지 않는다 — 사용자가 두 후보 중 고른다
func TestUnifyLotsSkipsEngineDisagreement(t *testing.T) {
	rows := []LotPhotoRow{
		{Lot: "LOT-1001", Expiry: "2028-06-30"},
		{Lot: "LOT-1002", Expiry: "2028-06-30", Unsure: []string{"lot"}, Alt: map[string]string{"lot": "LOT-1007"}},
		{Lot: "LOT-1009", Expiry: "", Unsure: []string{"lot", "expiry"}, Alt: map[string]string{"expiry": "2029-01-31"}},
	}
	unifyLots(rows)
	if rows[1].Lot != "LOT-1002" {
		t.Errorf("두 후보가 있는 제조번호를 바꿈: %q", rows[1].Lot)
	}
	if rows[2].Expiry != "" {
		t.Errorf("두 후보가 있는 유효기간을 채움: %q", rows[2].Expiry)
	}
}

// 보조 엔진이 실행되지 않으면 이번 실행 동안은 다시 부르지 않는다 (사진마다 시간 초과를 기다리지 않게)
func TestPaddleOffAfterFailure(t *testing.T) {
	s := lotTestService(t)
	calls := 0
	win := engineOCR([][]string{engRow1}, 3)
	withEngines(t,
		func() ([]platform.OCRResult, error) { return []platform.OCRResult{win}, nil },
		func() ([]platform.OCRResult, error) { calls++; return nil, platform.ErrPaddleUnavailable })
	for i := 0; i < 3; i++ {
		if _, err := s.ReadLotPhoto(context.Background(), LotPhotoRequest{ImageBase64: tinyPNG(t), Code: "900000010"}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("보조 엔진을 %d번 부름 (1번이어야 함)", calls)
	}
}
