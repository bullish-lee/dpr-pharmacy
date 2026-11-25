package platform

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

// 스크립트 출력 읽기: 배열·객체 하나·BOM·앞에 섞인 다른 줄
func TestParseOCROutput(t *testing.T) {
	for name, raw := range map[string]string{
		"배열":    `[{"width":10,"height":5,"engine":"ko","lines":[{"text":"LOT-1001","words":[]}]}]`,
		"객체 하나": `{"width":10,"height":5,"engine":"ko","lines":[{"text":"LOT-1001","words":[]}]}`,
		"BOM":   "\xEF\xBB\xBF" + `[{"width":10,"lines":[{"text":"LOT-1001"}]}]`,
		"앞에 경고": "WARNING: 무언가\r\n" + `[{"width":10,"lines":[{"text":"LOT-1001"}]}]`,
	} {
		res, err := parseOCROutput([]byte(raw))
		if err != nil || len(res) != 1 || len(res[0].Lines) != 1 || res[0].Lines[0].Text != "LOT-1001" {
			t.Errorf("%s: %+v %v", name, res, err)
		}
	}
	if _, err := parseOCROutput([]byte("엉뚱한 출력")); err == nil {
		t.Error("JSON 이 아니면 오류")
	}
}

// 스크립트가 알린 문제 → 알아듣기 쉬운 오류 (모두 ErrOCRUnavailable 로 감싸 화면이 직접 입력으로 이어 가게)
func TestCheckOCRResults(t *testing.T) {
	cases := map[string]struct {
		res  []OCRResult
		ok   bool
		want string
	}{
		"정상":       {[]OCRResult{{Engine: "ko"}}, true, ""},
		"한국어 없음":   {[]OCRResult{{Engine: "en-US", Warn: "no_korean"}}, true, ""},
		"일부만 실패":   {[]OCRResult{{Error: "이미지를 읽지 못함: x"}, {Engine: "ko"}}, true, ""},
		"제한 모드":    {[]OCRResult{{Error: "constrained", Mode: "ConstrainedLanguage"}}, false, "제한 모드(ConstrainedLanguage)"},
		"인식 언어 없음": {[]OCRResult{{Error: "no_engine"}}, false, "글자 인식 언어가 하나도 없습니다"},
		"모두 실패":    {[]OCRResult{{Error: "글자 인식 준비 실패: 형식을 찾을 수 없음"}}, false, "글자 인식 준비 실패"},
		"결과 없음":    {nil, false, "비었습니다"},
	}
	for name, c := range cases {
		err := checkOCRResults(c.res)
		if c.ok != (err == nil) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if err != nil {
			if !errors.Is(err, ErrOCRUnavailable) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
}

// 점검 그림 결과 정리
func TestSummarizeCheck(t *testing.T) {
	line := func(s string) OCRLine { return OCRLine{Text: s} }
	var c OCRCheck
	summarizeCheck(&c, []OCRResult{{Engine: "ko", Lines: []OCRLine{line("2026-11-14 사입 가람도매 30.00 LOT-1001"), line("LOT-1002"), line("LOT-1003")}}})
	if !c.OK || !c.Korean || c.Found != 3 || c.Engine != "ko" || c.Problem != "" {
		t.Fatalf("한국어: %+v", c)
	}
	c = OCRCheck{}
	summarizeCheck(&c, []OCRResult{{Engine: "en-US", Warn: "no_korean", Lines: []OCRLine{line("LOT-1001 LOT-1002")}}})
	if !c.OK || c.Korean || c.Found != 2 || !strings.Contains(c.Problem, "한국어") {
		t.Fatalf("한국어 없음: %+v", c)
	}
	c = OCRCheck{}
	summarizeCheck(&c, []OCRResult{{Engine: "ko"}})
	if c.OK || c.Problem == "" {
		t.Fatalf("하나도 못 읽음: %+v", c)
	}
}

// -EncodedCommand 값은 UTF-16LE base64 (한글 주석 포함, 앞의 BOM 은 뺀다) — 명령줄 길이 한도(32K) 안
func TestEncodedCommandRoundTrip(t *testing.T) {
	enc := encodedCommandForTest("\uFEFF# 한글 주석\nWrite-Output '가'")
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b)%2 != 0 {
		t.Fatal(err)
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	if got := string(utf16.Decode(u)); got != "# 한글 주석\nWrite-Output '가'" {
		t.Fatalf("%q", got)
	}
	if n := len(encodedCommandForTest(string(ocrScript))); n > 30000 {
		t.Fatalf("OCR 스크립트가 명령줄에 너무 깁니다: %d", n)
	}
	if len(OCRSample()) < 1000 {
		t.Fatal("점검 그림이 비어 있음")
	}
}
