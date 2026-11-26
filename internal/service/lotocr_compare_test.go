package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"dpr/internal/platform"
)

// 가상 자료 (실제 약국 자료 아님): 가람도매·나래도매·다솜도매, 제품코드 900000010

func cmpRow(date, sup string, supID int64, qty float64, expiry, lot string, unsure ...string) LotPhotoRow {
	r := LotPhotoRow{Code: "900000010", Date: date, SupplierRaw: sup, SupplierID: supID, Qty: qty, Expiry: expiry, Lot: lot, Unsure: unsure}
	if supID != 0 {
		r.SupplierName = sup
	}
	return r
}

func TestCompareLotRowsAgreeAndDiffer(t *testing.T) {
	win := []lotEngineRow{
		// 한국어 엔진이 흐리게 읽은 칸(유효기간·제조번호)이 있지만 다른 엔진과 같은 값
		{100, cmpRow("2026-11-14", "가람도매", 1, 30, "2027-10-31", "2400117", "expiry", "lot")},
		// 제조번호를 다르게 읽음 (0 ↔ O)
		{150, cmpRow("2026-10-02", "나래도매", 2, 100, "2027-09-30", "KA5O32")},
		// 도매를 못 맞춤, 수량이 다름
		{200, cmpRow("2026-09-18", "다숨도매", 0, 24, "2028-01-31", "B24X017", "supplier")},
	}
	pad := []lotEngineRow{
		{203, cmpRow("2026-09-18", "다솜도매", 3, 240, "2028-01-31", "B24X017")},
		{98, cmpRow("2026-11-14", "가람도매", 1, 30, "2027-10-31", "2400117")},
		{152, cmpRow("2026-10-02", "나래도매", 2, 100, "2027-09-30", "KA5032")},
	}
	got := compareLotRows(win, pad, 50)
	if len(got) != 3 {
		t.Fatalf("줄 %d개", len(got))
	}
	// 1) 모두 같음 → 확신, Alt 없음
	if r := got[0]; len(r.Unsure) != 0 || r.Alt != nil || r.Lot != "2400117" {
		t.Fatalf("같게 읽은 줄: %+v", r)
	}
	// 2) 제조번호만 다름 → 바탕(Windows) 값 + Alt 에 다른 쪽
	if r := got[1]; !reflect.DeepEqual(r.Unsure, []string{"lot"}) || r.Lot != "KA5O32" || !reflect.DeepEqual(r.Alt, map[string]string{"lot": "KA5032"}) {
		t.Fatalf("제조번호가 다른 줄: %+v", r)
	}
	// 3) 도매: 바탕은 흐림·다른 쪽은 확실 → 다른 쪽 값을 쓰고 바탕을 Alt 로. 수량: 둘 다 확실한데 다름 → 바탕 값 + Alt
	r := got[2]
	if r.SupplierID != 3 || r.SupplierName != "다솜도매" || r.Alt["supplier"] != "다숨도매" {
		t.Fatalf("도매: %+v", r)
	}
	if _, ok := r.Alt["supplierId"]; ok {
		t.Fatalf("등록 안 된 쪽에는 번호가 없어야 함: %+v", r.Alt)
	}
	if r.Qty != 24 || r.Alt["qty"] != "240" {
		t.Fatalf("수량: %+v", r)
	}
	if !reflect.DeepEqual(r.Unsure, []string{"supplier", "qty"}) {
		t.Fatalf("확인 필요: %v", r.Unsure)
	}
	// JSON: alt 는 다른 값이 있을 때만 — 없으면 지금 화면이 받는 모양 그대로
	j0, _ := json.Marshal(got[0])
	j1, _ := json.Marshal(got[1])
	if strings.Contains(string(j0), `"alt"`) || !strings.Contains(string(j1), `"alt":{"lot":"KA5032"}`) || !strings.Contains(string(j1), `"lot":"KA5O32"`) {
		t.Fatalf("JSON: %s / %s", j0, j1)
	}
}

func TestCompareLotRowsEmptyAndMissing(t *testing.T) {
	win := []lotEngineRow{
		// 유효기간을 못 읽음 (다른 엔진은 확실하게 읽음), 제조번호는 이쪽만 읽음(흐림)
		{100, cmpRow("2026-11-14", "가람도매", 1, 30, "", "LOT-1001", "expiry", "lot")},
		// 이 엔진만 찾은 줄
		{300, cmpRow("2026-07-21", "다솜도매", 3, 240, "2027-05-31", "LOT-1003")},
	}
	pad := []lotEngineRow{
		{104, cmpRow("2026-11-14", "가람도매", 1, 30, "2027-10-31", "")},
		// 다른 엔진만 찾은 줄
		{200, cmpRow("2026-09-18", "나래도매", 2, 100, "2027-09-30", "LOT-1002")},
	}
	got := compareLotRows(win, pad, 0) // 줄 높이를 모름 → 줄 간격에서 어림
	if len(got) != 3 {
		t.Fatalf("줄 %d개: %+v", len(got), got)
	}
	// 위에서 아래 순서
	if got[0].Lot != "LOT-1001" || got[1].Lot != "LOT-1002" || got[2].Lot != "LOT-1003" {
		t.Fatalf("순서: %v %v %v", got[0].Lot, got[1].Lot, got[2].Lot)
	}
	// 한쪽만 읽은 칸: 읽은 값을 쓰고 그 엔진의 확신을 따른다
	if r := got[0]; r.Expiry != "2027-10-31" || r.Lot != "LOT-1001" || !reflect.DeepEqual(r.Unsure, []string{"lot"}) || r.Alt != nil {
		t.Fatalf("한쪽만 읽은 칸: %+v", r)
	}
	// 한 엔진만 찾은 줄: 모든 칸 확인 필요
	for _, r := range got[1:] {
		if len(r.Unsure) != len(lotCompareFields) || r.Alt != nil || r.Code != "900000010" {
			t.Fatalf("한 엔진만 찾은 줄: %+v", r)
		}
	}
	// 한쪽이 한 줄도 못 읽음
	if got := compareLotRows(win, nil, 50); len(got) != 2 || len(got[0].Unsure) != 5 {
		t.Fatalf("한쪽이 빔: %+v", got)
	}
	if got := compareLotRows(nil, nil, 50); len(got) != 0 {
		t.Fatalf("둘 다 빔: %+v", got)
	}
}

// 줄 간격보다 가까운 두 줄을 한 줄과 겹쳐 짝짓지 않는다 (가까운 짝부터, 한 번씩만)
func TestCompareLotRowsOneToOne(t *testing.T) {
	a := []lotEngineRow{{100, cmpRow("2026-11-14", "가람도매", 1, 30, "2027-10-31", "31007-1")}, {150, cmpRow("2026-11-14", "가람도매", 1, 30, "2027-10-31", "31007-2")}}
	b := []lotEngineRow{{128, cmpRow("2026-11-14", "가람도매", 1, 30, "2027-10-31", "31007-2")}}
	got := compareLotRows(a, b, 50)
	if len(got) != 2 {
		t.Fatalf("줄 %d개", len(got))
	}
	// 128 은 150 에 더 가깝다 → 둘째 줄과 짝, 첫 줄은 혼자
	if len(got[0].Unsure) != 5 || len(got[1].Unsure) != 0 {
		t.Fatalf("짝: %+v", got)
	}
}

// 같은 도매 이름을 읽었지만 등록된 거래처가 아니면 여전히 골라야 한다. 한쪽만 등록 거래처로 맞춘 경우는 다른 값으로 본다
func TestCompareLotRowSupplier(t *testing.T) {
	a := cmpRow("2026-11-14", "하늘 약품", 0, 30, "2027-10-31", "LOT-1001", "supplier")
	b := cmpRow("2026-11-14", "하늘약품", 0, 30, "2027-10-31", "lot-1001", "supplier")
	r := compareLotRow(a, b)
	if !reflect.DeepEqual(r.Unsure, []string{"supplier"}) || r.Alt != nil {
		t.Fatalf("%+v", r)
	}
	b = cmpRow("2026-11-14", "가람도매", 1, 30, "2027-10-31", "LOT-1001")
	r = compareLotRow(a, b)
	if r.SupplierID != 1 || r.Alt["supplier"] != "하늘 약품" || !reflect.DeepEqual(r.Unsure, []string{"supplier"}) {
		t.Fatalf("%+v", r)
	}
	// 둘 다 등록 거래처인데 서로 다름 → 다른 쪽 번호도 넘긴다
	a = cmpRow("2026-11-14", "나래도매", 2, 30, "2027-10-31", "LOT-1001")
	r = compareLotRow(a, b)
	if r.SupplierID != 2 || r.Alt["supplier"] != "가람도매" || r.Alt["supplierId"] != "1" {
		t.Fatalf("%+v", r)
	}
}

// 보조 엔진이 안 되면 ok=false 로 돌아오고 (오류로 막지 않는다), 되면 결과를 그대로 넘긴다
func TestPaddleReadDegrades(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := paddleRunner
	defer func() { paddleRunner = old }()
	img := []byte("가상 그림 내용")

	paddleRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		return nil, platform.ErrPaddleUnavailable
	}
	if _, ok := s.paddleRead(context.Background(), img, t.TempDir()); ok {
		t.Fatal("쓸 수 없는데 ok")
	}
	if !s.paddleOff.Load() {
		t.Fatal("엔진을 못 열었으면 이번 실행 동안은 끈다")
	}
	s.paddleOff.Store(false)
	paddleRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		return []platform.OCRResult{{Error: "그림을 읽지 못했습니다"}}, nil
	}
	if _, ok := s.paddleRead(context.Background(), img, t.TempDir()); ok {
		t.Fatal("그림 실패인데 ok")
	}
	var gotPath string
	paddleRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		if _, has := ctx.Deadline(); !has {
			return nil, errors.New("시간 제한이 없음")
		}
		gotPath = paths[0]
		if b, err := os.ReadFile(gotPath); err != nil || string(b) != string(img) {
			return nil, errors.New("올린 내용 그대로가 아님")
		}
		return []platform.OCRResult{{Engine: platform.PaddleEngine, Width: 40, Height: 20, Lines: []platform.OCRLine{{Text: "LOT-1001"}}}}, nil
	}
	res, ok := s.paddleRead(context.Background(), img, t.TempDir())
	if !ok || res.Engine != platform.PaddleEngine || len(res.Lines) != 1 || !strings.HasSuffix(gotPath, "paddle-src.img") {
		t.Fatalf("ok=%v %+v %s", ok, res, gotPath)
	}
}
