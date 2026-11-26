package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dpr/internal/platform"
)

// 보조 글자 인식(PaddleOCR)은 기본으로 끈다 — 가짜 글자 인식 결과로 시험하는 테스트가 이 PC 의 실제 엔진에 흔들리지 않게.
// 실제 엔진을 쓰는 시험은 useEngines 로 직접 켠다.
func TestMain(m *testing.M) {
	paddleRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		return nil, platform.ErrPaddleUnavailable
	}
	os.Exit(m.Run())
}

// paddleAvailable : 이 PC 에서 PaddleOCR 로 점검 그림을 실제로 읽어 본다
func paddleAvailable(t *testing.T) error {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.png")
	if err := os.WriteFile(p, platform.OCRSample(), 0o600); err != nil {
		return err
	}
	_, err := platform.PaddleOCR(context.Background(), []string{p})
	return err
}
