//go:build windows

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OCR 은 이미지 파일들을 Windows 기본 OCR(한국어)로 읽는다. 한 번의 PowerShell 실행으로 모두 처리한다.
func OCR(ctx context.Context, paths []string) ([]OCRResult, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "dpr-ocr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	script := filepath.Join(dir, "ocr.ps1")
	// PowerShell 5.1 은 BOM 없는 UTF-8 스크립트의 한글 주석을 잘못 읽을 수 있어 BOM 을 붙인다
	if err := os.WriteFile(script, append([]byte{0xEF, 0xBB, 0xBF}, ocrScript...), 0o600); err != nil {
		return nil, err
	}
	args := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script}
	for _, p := range paths {
		// Windows.Storage 는 '\' 로 된 절대 경로만 받는다
		if abs, err := filepath.Abs(filepath.FromSlash(p)); err == nil {
			p = abs
		}
		args = append(args, p)
	}
	cmd := hidden(exec.CommandContext(ctx, "powershell.exe", args...))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("글자 인식(Windows OCR) 실행 실패: %v %s", err, msg)
	}
	raw := bytes.TrimSpace(bytes.TrimPrefix(out.Bytes(), []byte{0xEF, 0xBB, 0xBF}))
	var res []OCRResult
	if err := json.Unmarshal(raw, &res); err != nil {
		// 결과가 하나면 ConvertTo-Json 이 배열을 벗기는 경우가 있다
		var one OCRResult
		if err2 := json.Unmarshal(raw, &one); err2 != nil {
			return nil, fmt.Errorf("글자 인식 결과를 읽지 못했습니다: %v", err)
		}
		res = []OCRResult{one}
	}
	for _, r := range res {
		if r.Error == "no_korean" {
			return nil, fmt.Errorf("%w — Windows 설정 → 시간 및 언어 → 언어에서 '한국어'를 추가해 주세요", ErrOCRUnavailable)
		}
	}
	return res, nil
}
