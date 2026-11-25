package platform

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Windows 기본 OCR(Windows.Media.Ocr) — 무료·오프라인. 사입정보 사진 판독에 쓴다.

//go:embed ocr.ps1
var ocrScript []byte

// OCRWord : 인식한 단어 하나와 이미지 안의 위치 (픽셀)
type OCRWord struct {
	Text string  `json:"t"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

type OCRLine struct {
	Text  string    `json:"text"`
	Words []OCRWord `json:"words"`
}

// OCRResult : 이미지 한 장의 인식 결과
type OCRResult struct {
	Width  int       `json:"width"`
	Height int       `json:"height"`
	Angle  float64   `json:"angle"`
	Lines  []OCRLine `json:"lines"`
	Error  string    `json:"error,omitempty"`
	Warn   string    `json:"warn,omitempty"`   // no_korean: 한국어 글자 인식이 없어 다른 언어로 읽음
	Engine string    `json:"engine,omitempty"` // 쓴 인식 언어 (ko 등)
	Mode   string    `json:"mode,omitempty"`   // error = constrained 일 때 PowerShell 언어 모드
}

// ErrOCRUnavailable : 이 PC 에서 OCR 을 쓸 수 없음 (Windows 가 아니거나 한국어 OCR 없음)
var ErrOCRUnavailable = errors.New("이 PC 에서는 글자 인식(Windows OCR)을 쓸 수 없습니다")

// parseOCROutput : 스크립트 출력(JSON 배열, 결과가 하나면 객체일 수도 있음)을 읽는다.
func parseOCROutput(raw []byte) ([]OCRResult, error) {
	raw = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(raw), []byte{0xEF, 0xBB, 0xBF}))
	// 앞에 다른 출력이 섞여 있으면 마지막 JSON 부분만
	if i := bytes.LastIndexAny(raw, "\n"); i >= 0 && !json.Valid(raw) {
		raw = bytes.TrimSpace(raw[i+1:])
	}
	var res []OCRResult
	if err := json.Unmarshal(raw, &res); err != nil {
		var one OCRResult
		if err2 := json.Unmarshal(raw, &one); err2 != nil {
			return nil, err
		}
		res = []OCRResult{one}
	}
	return res, nil
}

// checkOCRResults : 스크립트가 알린 문제를 알아듣기 쉬운 오류로 바꾼다. 일부 이미지만 실패하면 나머지로 진행한다.
func checkOCRResults(res []OCRResult) error {
	if len(res) == 0 {
		return fmt.Errorf("%w — 글자 인식 결과가 비었습니다", ErrOCRUnavailable)
	}
	failed := 0
	for _, r := range res {
		switch r.Error {
		case "":
		case "constrained":
			return fmt.Errorf("%w — 이 PC 의 보안 정책(스마트 앱 컨트롤·AppLocker 등)이 PowerShell 을 제한 모드(%s)로 실행해 글자 인식을 쓸 수 없습니다. 사진 옆 표에 '줄 추가'로 직접 입력하세요", ErrOCRUnavailable, r.Mode)
		case "no_engine":
			return fmt.Errorf("%w — 이 PC 에 글자 인식 언어가 하나도 없습니다. Windows 설정 → 시간 및 언어 → 언어에서 '한국어'를 추가하거나, 사진 옆 표에 직접 입력하세요", ErrOCRUnavailable)
		default:
			failed++
		}
	}
	if failed == len(res) {
		return fmt.Errorf("%w — %s. 사진 옆 표에 '줄 추가'로 직접 입력할 수 있습니다", ErrOCRUnavailable, res[0].Error)
	}
	return nil
}

//go:embed ocr_sample.png
var ocrSample []byte

// OCRCheck : 글자 인식 점검 결과 (설정 화면 '글자 인식 점검')
type OCRCheck struct {
	OK         bool   `json:"ok"`
	PowerShell string `json:"powerShell"` // 찾은 PowerShell 위치
	Engine     string `json:"engine"`     // 쓴 인식 언어
	Korean     bool   `json:"korean"`     // 한국어 글자 인식 있음
	Found      int    `json:"found"`      // 점검 그림에서 찾은 제조번호 수 (3개 중)
	Text       string `json:"text"`       // 인식한 글자 (앞부분)
	Problem    string `json:"problem"`    // 안 될 때 이유와 할 일
	Ms         int64  `json:"ms"`
}

// OCRSample 은 점검용 가상 사입현황 표 그림 (실제 약국 자료 아님): 가람·나래·다솜도매, LOT-1001~1003.
func OCRSample() []byte { return ocrSample }

// summarizeCheck : 점검 그림 인식 결과를 정리한다.
func summarizeCheck(c *OCRCheck, res []OCRResult) {
	var sb bytes.Buffer
	for _, r := range res {
		if r.Engine != "" {
			c.Engine = r.Engine
		}
		c.Korean = r.Warn != "no_korean" && r.Error == ""
		for _, l := range r.Lines {
			sb.WriteString(l.Text)
			sb.WriteByte('\n')
		}
	}
	text := sb.String()
	for _, lot := range []string{"1001", "1002", "1003"} {
		if bytes.Contains([]byte(text), []byte(lot)) {
			c.Found++
		}
	}
	if len(text) > 400 {
		text = text[:400]
	}
	c.Text = text
	switch {
	case c.Found == 0:
		c.Problem = "글자 인식은 실행되지만 점검 그림의 제조번호를 하나도 읽지 못했습니다. 사진 옆 표에 직접 입력하세요."
	case !c.Korean:
		c.OK = true
		c.Problem = "한국어 글자 인식이 없어 도매 이름은 못 읽습니다 (날짜·수량·제조번호는 읽힘). Windows 설정 → 시간 및 언어 → 언어에서 '한국어'를 추가하면 도매 이름도 읽습니다."
	default:
		c.OK = true
	}
}

// encodedCommand : PowerShell -EncodedCommand 값 (UTF-16LE → base64)
func encodedCommand(script []byte) string {
	u := utf16.Encode([]rune(strings.TrimPrefix(string(script), "\uFEFF")))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func encodedCommandForTest(s string) string { return encodedCommand([]byte(s)) }
