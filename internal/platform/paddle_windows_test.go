//go:build windows && amd64

package platform

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// onnxruntime 후보(프로그램에 든 것 · Windows 에 들어 있는 것)를 하나씩 열어 모델 두 개가 올라가는지, 점검 그림을 읽는지 기록한다.
// 실패로 치지 않는다 — 어느 PC 에서 어느 쪽이 되는지 알아 두려는 것이다 (CI: windows-latest).
// 이름에 'OCR' 을 넣지 않았다: 글자 인식·서명 확인 단계(-run "…|OCR|…")와 따로 돌린다.
func TestPaddleRuntimes(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "sample.png")
	if err := os.WriteFile(img, ocrSample, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range ortCandidates() {
		rt, err := openORT(c)
		if err != nil {
			t.Logf("%s: 열지 못함 — %v", c.name, err)
			continue
		}
		t.Logf("%s: 열림 (버전 %s)", c.name, rt.version)
		det, err := rt.newSession(paddleDetModel, paddleInput, paddleOutput, 2)
		if err != nil {
			t.Logf("  검출 모델을 올리지 못함 — %v", err)
			continue
		}
		rec, err := rt.newSession(paddleRecModel, paddleInput, paddleOutput, 2)
		if err != nil {
			t.Logf("  글자 모델을 올리지 못함 — %v", err)
			continue
		}
		eng := &paddleEngine{rt: rt, det: det, rec: rec, dict: paddleDict()}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		start := time.Now()
		res := eng.read(ctx, img)
		cancel()
		found := 0
		for _, l := range res.Lines {
			for _, lot := range []string{"LOT-1001", "LOT-1002", "LOT-1003"} {
				if l.Text == lot {
					found++
				}
			}
		}
		t.Logf("  점검 그림: 글줄 %d, 제조번호 %d/3, %dms, 오류 %q", len(res.Lines), found, time.Since(start).Milliseconds(), res.Error)
	}
}
