//go:build windows

package service

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"dpr/internal/platform"
)

// 실제 Windows 글자 인식으로 가상 사입현황 표 그림을 읽어 사입 근거 줄을 만든다 (PATH 를 비운 채로).
// 한국어 글자 인식이 있는 PC(DPR_OCR_REQUIRE_KO=1)에서만 끝까지 확인한다.
func TestReadLotPhotoRealOCR(t *testing.T) {
	if os.Getenv("DPR_OCR_REQUIRE_KO") != "1" {
		t.Skip("한국어 글자 인식이 있는 Windows 에서만 (DPR_OCR_REQUIRE_KO=1)")
	}
	t.Setenv("PATH", "")
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	res, err := s.ReadLotPhoto(ctx, LotPhotoRequest{Code: "900000010", ImageBase64: base64.StdEncoding.EncodeToString(platform.OCRSample())})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res.Rows {
		got = append(got, r.Date+" "+r.SupplierName+" "+r.Lot+" "+r.Expiry)
	}
	t.Logf("note=%q\n%s", res.Note, strings.Join(got, "\n"))
	want := map[string]string{"LOT-1001": "가람도매", "LOT-1002": "나래도매", "LOT-1003": "다솜도매"}
	found := 0
	for _, r := range res.Rows {
		if sup, ok := want[r.Lot]; ok {
			found++
			if r.SupplierName != sup {
				t.Errorf("%s: 도매 %q, 기대 %q", r.Lot, r.SupplierName, sup)
			}
		}
	}
	if found != 3 {
		t.Fatalf("제조번호 3개 중 %d개 읽음", found)
	}
}
