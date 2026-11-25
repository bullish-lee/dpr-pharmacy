//go:build !windows

package platform

import "context"

// OCR : Windows 기본 OCR 은 Windows 에서만 쓸 수 있다.
func OCR(ctx context.Context, paths []string) ([]OCRResult, error) {
	return nil, ErrOCRUnavailable
}

// OCRSelfTest : Windows 가 아니면 쓸 수 없다.
func OCRSelfTest(ctx context.Context) OCRCheck {
	return OCRCheck{Problem: ErrOCRUnavailable.Error() + " (Windows 전용 기능)"}
}
