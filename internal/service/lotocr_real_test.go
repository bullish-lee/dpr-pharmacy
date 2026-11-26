package service

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"dpr/internal/platform"
)

// 실제 사진으로 Windows OCR 판독을 돌려 본다 (Windows 에서만, 이미지가 있을 때만).
//
//	DPR_OCR_IMAGE=사진.jpg DPR_OCR_SUPPLIERS="도매1,도매2" DPR_OCR_CODE=제품코드 go test ./internal/service -run TestLotOCRReal -v
//	(DPR_OCR_PADDLE=1 이면 보조 엔진 PaddleOCR 도 쓴다. 맥에서는 DPR_ONNXRUNTIME_LIB=libonnxruntime.dylib 경로)
//
// 실제 거래처 이름·제품코드는 코드에 두지 않고 환경변수로 준다 (docs/SENSITIVE.md).
func TestLotOCRReal(t *testing.T) {
	path := os.Getenv("DPR_OCR_IMAGE")
	if path == "" {
		t.Skip("DPR_OCR_IMAGE 없음")
	}
	if os.Getenv("DPR_OCR_PADDLE") == "1" { // 보조 엔진(PaddleOCR)도 쓴다 (맥에서는 DPR_ONNXRUNTIME_LIB 필요)
		old := paddleRunner
		paddleRunner = platform.PaddleOCR
		defer func() { paddleRunner = old }()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, n := range strings.Split(os.Getenv("DPR_OCR_SUPPLIERS"), ",") {
		if n = strings.TrimSpace(n); n == "" {
			continue
		}
		if _, err := s.SaveSupplier(SupplierInput{Name: n, Kind: "wholesaler"}); err != nil {
			t.Fatal(err)
		}
	}
	code := os.Getenv("DPR_OCR_CODE")
	if code == "" {
		code = "900000010"
	}
	res, err := s.ReadLotPhoto(context.Background(), LotPhotoRequest{ImageBase64: base64.StdEncoding.EncodeToString(raw), Code: code})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mode=%s rows=%d skipped=%d note=%s", res.Mode, len(res.Rows), res.Skipped, res.Note)
	for _, r := range res.Rows {
		t.Logf("%-10s | %-12s (%s) | %6.1f | %-10s | %-8s | unsure=%v alt=%v", r.Date, r.SupplierName, r.SupplierRaw, r.Qty, r.Expiry, r.Lot, r.Unsure, r.Alt)
	}
}
