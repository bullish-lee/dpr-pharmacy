//go:build windows

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OCR 은 이미지 파일들을 Windows 기본 OCR 로 읽는다. 한 번의 PowerShell 실행으로 모두 처리한다.
//
// 막히지 않게 한 것:
//   - PowerShell 을 PATH 가 아니라 실제 위치로 찾는다 (PowerShellCommand).
//   - 스크립트 파일을 만들지 않고 -EncodedCommand 로 넘긴다 (임시 폴더 스크립트·실행 정책에 막히지 않음).
//   - 한국어 글자 인식이 없으면 다른 언어로라도 읽는다 (숫자·날짜·영문 제조번호는 읽힘, Warn = no_korean).
//   - 보안 정책(스마트 앱 컨트롤·AppLocker)이 PowerShell 을 제한 모드로 돌리면 그렇다고 알려 준다.
//
// 그래도 안 되면 ErrOCRUnavailable 로 감싼 오류 — 화면은 사진 옆 표에 직접 입력하게 한다.
func OCR(ctx context.Context, paths []string) ([]OCRResult, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		// Windows.Storage 는 '\' 로 된 절대 경로만 받는다
		if a, err := filepath.Abs(filepath.FromSlash(p)); err == nil {
			p = a
		}
		abs[i] = p
	}
	pj, _ := json.Marshal(abs)
	cmd, err := PowerShellCommand(ctx, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encodedCommand(ocrScript))
	if err != nil {
		return nil, fmt.Errorf("%w — %v. 사진 옆 표에 '줄 추가'로 직접 입력할 수 있습니다", ErrOCRUnavailable, err)
	}
	cmd.Env = append(os.Environ(), "DPR_OCR_PATHS="+string(pj))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("글자 인식이 시간 안에 끝나지 않았습니다: %w", ctx.Err())
		}
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		// 보안 프로그램·정책이 PowerShell 실행을 막은 경우 등
		return nil, fmt.Errorf("%w — PowerShell 실행 실패: %v %s. 사진 옆 표에 '줄 추가'로 직접 입력할 수 있습니다", ErrOCRUnavailable, err, msg)
	}
	res, err := parseOCROutput(out.Bytes())
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("%w — 글자 인식 결과를 읽지 못했습니다: %v %s", ErrOCRUnavailable, err, msg)
	}
	return res, checkOCRResults(res)
}

// OCRSelfTest 는 점검 그림으로 글자 인식이 이 PC 에서 되는지 확인한다 — Windows 글자 인식과 보조 글자 인식(PaddleOCR) 둘 다.
func OCRSelfTest(ctx context.Context) OCRCheck {
	c := windowsOCRSelfTest(ctx)
	c.Paddle = paddleSelfTest(ctx)
	return c
}

func windowsOCRSelfTest(ctx context.Context) OCRCheck {
	var c OCRCheck
	p, err := PowerShellPath()
	if err != nil {
		c.Problem = err.Error() + " — 사진 옆 표에 직접 입력하세요."
		return c
	}
	c.PowerShell = p
	dir, err := os.MkdirTemp("", "dpr-ocrcheck-")
	if err != nil {
		c.Problem = err.Error()
		return c
	}
	defer os.RemoveAll(dir)
	img := filepath.Join(dir, "sample.png")
	if err := os.WriteFile(img, ocrSample, 0o600); err != nil {
		c.Problem = err.Error()
		return c
	}
	start := time.Now()
	res, err := OCR(ctx, []string{img})
	c.Ms = time.Since(start).Milliseconds()
	if err != nil {
		c.Problem = err.Error()
		return c
	}
	summarizeCheck(&c, res)
	return c
}
