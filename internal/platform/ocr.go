package platform

import (
	_ "embed"
	"errors"
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
}

// ErrOCRUnavailable : 이 PC 에서 OCR 을 쓸 수 없음 (Windows 가 아니거나 한국어 OCR 없음)
var ErrOCRUnavailable = errors.New("이 PC 에서는 글자 인식(Windows OCR)을 쓸 수 없습니다")
