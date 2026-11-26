package service

import (
	"context"
	"fmt"
	"image"
	"math/rand"
	"regexp"
	"strings"
	"testing"

	"dpr/internal/platform"
)

// 전수 점검에서 찾은 '0줄'·줄 빠짐·칸 밀림 경로마다 하나씩. 모든 값은 가상(합성) 자료다.
//
// 좌표는 OCR 이 읽은(3배 확대본) 기준: 글자 높이 13, 영문·숫자 폭 7, 한글 폭 12.

var numericCell = regexp.MustCompile(`^-?[\d.,]+$`)

func cellWidth(s string) float64 {
	w := 0.0
	for _, r := range s {
		if r > 0x2E80 {
			w += 12
		} else {
			w += 7
		}
	}
	return w
}

// 이팜 사입현황 열 배치: 사입일자·구분·거래처·수량·단가·총수량·합계액·부가세·유효기간·제조번호
// (왼쪽 끝. 4~7번째 열의 숫자는 열 폭 60 의 오른쪽 끝에 맞춘다)
var epharmCols = []float64{10, 110, 160, 290, 360, 440, 520, 610, 680, 780}

type gridOpt struct {
	y0, pitch float64
	slant     float64 // 가로 1 마다 세로로 밀리는 양 (기운 사진)
}

// gridWords : 표 모양의 단어들. 빈 문자열은 빈 칸. 한 칸에 "|" 가 있으면 OCR 이 그 자리에서 단어를 쪼갠 것.
func gridWords(xs []float64, o gridOpt, rows ...[]string) [][]any {
	if o.pitch == 0 {
		o.pitch = 22
	}
	var out [][]any
	y := o.y0 + 8
	for _, row := range rows {
		for i, c := range row {
			if c == "" {
				continue
			}
			whole := strings.ReplaceAll(c, "|", "")
			x := xs[i]
			if numericCell.MatchString(whole) && i >= 3 && i <= 6 {
				x = xs[i] + 60 - cellWidth(whole) - 5*float64(strings.Count(c, "|"))
			}
			for _, part := range strings.Split(c, "|") {
				w := cellWidth(part)
				out = append(out, []any{part, x, y + x*o.slant, w})
				x += w + 5
			}
		}
		y += o.pitch
	}
	return out
}

func lotRowText(r LotPhotoRow) string {
	return fmt.Sprintf("%s|%s|%g|%s|%s", r.Date, r.SupplierName, r.Qty, r.Expiry, r.Lot)
}

func lotRowTexts(rows []LotPhotoRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = lotRowText(r)
	}
	return out
}

func lotTestService(t *testing.T) *Service {
	t.Helper()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, n := range []string{"가람도매", "나래도매", "다솜도매", "이기도매"} {
		if _, err := s.SaveSupplier(SupplierInput{Name: n, Kind: "wholesaler"}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func runLotOCR(t *testing.T, s *Service, results ...platform.OCRResult) *LotPhotoResult {
	t.Helper()
	old := ocrRunner
	ocrRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
		out := make([]platform.OCRResult, len(paths))
		for i := range paths {
			out[i] = results[min(i, len(results)-1)]
		}
		return out, nil
	}
	defer func() { ocrRunner = old }()
	got, err := s.ReadLotPhoto(context.Background(), LotPhotoRequest{ImageBase64: tinyPNG(t), Code: "900000010"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// 2026-11-26 신고 재현: 머리 없이 본문만 자른 화면 캡처, 숫자뿐인 제조번호, 반품(음수) 줄 → '0줄 읽음'.
func TestLotOCRHeaderlessCapture(t *testing.T) {
	s := lotTestService(t)
	got := runLotOCR(t, s, fakeOCR(gridWords(epharmCols, gridOpt{},
		[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
		[]string{"2026-10-02", "반품", "(주)나래도매", "-2.00", "19,200", "-60.00", "-38,400", "비과세", "20280630", "0045107"},
		[]string{"2026-09-01", "사입", "(주)가람도매", "1.00", "19,200", "30.00", "19,200", "포함", "20290531", "0031201"},
		[]string{"2026-05-14", "사입", "(주)나래도매", "3.00", "19,200", "90.00", "57,600", "포함", "20280630", "0045107"},
	)...))
	want := []string{
		"2026-10-16|가람도매|60|2029-05-31|0031201",
		"2026-09-01|가람도매|30|2029-05-31|0031201",
		"2026-05-14|나래도매|90|2028-06-30|0045107",
	}
	if g := lotRowTexts(got.Rows); strings.Join(g, "\n") != strings.Join(want, "\n") || got.Skipped != 1 {
		t.Fatalf("skipped=%d note=%q\n%s", got.Skipped, got.Note, strings.Join(g, "\n"))
	}
	for _, r := range got.Rows {
		if len(r.Unsure) != 0 {
			t.Errorf("또렷한 화면 캡처는 '확인 필요' 칸이 없어야 함: %+v", r)
		}
	}
	if !strings.Contains(got.Note, "머리") {
		t.Errorf("머리 없이 읽었다는 안내가 있어야 함: %q", got.Note)
	}
}

func TestLotOCRFailureModes(t *testing.T) {
	type tc struct {
		name    string
		words   [][]any
		want    []string // 날짜|도매|수량|유효기간|제조번호
		skipped int
		check   func(t *testing.T, res *LotPhotoResult)
	}
	unsureAll := func(keys ...string) func(*testing.T, *LotPhotoResult) {
		return func(t *testing.T, res *LotPhotoResult) {
			for _, r := range res.Rows {
				for _, k := range keys {
					if !isUnsure(r, k) {
						t.Errorf("%s 칸은 '확인 필요'여야 함: %+v", k, r)
					}
				}
			}
		}
	}
	header := []string{"사입일자", "구분", "거래처", "수량", "단가", "총수량", "합계액", "부가세", "유효기간", "제조번호"}
	cases := []tc{
		{
			name: "한 줄뿐인 캡처",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"}),
			want: []string{"2026-10-16|가람도매|60|2029-05-31|0031201"},
		},
		{
			// 유효기간·제조번호를 적지 않은 사입: 줄은 남기고 칸은 비운다 (과세 구분이나 금액이 제조번호로 들어가면 안 됨)
			name: "유효기간·제조번호가 빈 줄",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "", ""},
				[]string{"2026-05-14", "사입", "(주)나래도매", "3.00", "19,200", "90.00", "57,600", "포함", "20280630", ""},
				[]string{"2026-03-25", "사입", "(주)가람도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"}),
			want: []string{
				"2026-10-16|가람도매|60|2029-05-31|0031201",
				"2026-09-01|나래도매|30||",
				"2026-05-14|나래도매|90|2028-06-30|",
				"2026-03-25|가람도매|30|2028-06-30|0045107",
			},
		},
		{
			// 도매 이름의 '이'·'기'는 숫자 01·7 과 닮았다. 한 줄을 이어 붙여 숫자로 바꾸면 날짜 옆에 숫자가 끼어든다.
			name: "도매 이름에 숫자와 닮은 글자",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "이기도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "이기도매", "1.00", "19,200", "30.00", "19,200", "포함", "20290531", "0031201"}),
			want: []string{"2026-10-16|이기도매|60|2029-05-31|0031201", "2026-09-01|이기도매|30|2029-05-31|0031201"},
		},
		{
			// 부가세 열이 없는 배치: 합계액 바로 옆이 유효기간 (이어 붙이면 '38,400' 의 끝 숫자가 날짜에 섞인다)
			name: "금액 바로 옆의 유효기간",
			words: gridWords([]float64{10, 110, 160, 290, 360, 440, 520, 600, 700}, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)가람도매", "1.00", "19,200", "30.00", "202,900", "20290531", "0031201"}),
			want: []string{"2026-10-16|가람도매|60|2029-05-31|0031201", "2026-09-01|가람도매|30|2029-05-31|0031201"},
		},
		{
			// 머리가 반쯤 잘려 오른쪽 두 이름만 읽힘: 예전에는 표 바깥으로 보고 날짜·도매·수량을 잘라 냈다
			name: "머리가 반쯤 잘림",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"시이으자", "", "기괴저", "", "", "", "", "", "유효기간", "제조번호"},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"}),
			want: []string{"2026-10-16|가람도매|60|2029-05-31|0031201", "2026-09-01|나래도매|30|2028-06-30|0045107"},
		},
		{
			// 제조번호 머리만 못 읽음: 예전에는 제조번호가 유효기간 칸에 붙고 줄은 제조번호 없이 나왔다
			name: "제조번호 머리를 못 읽음",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"사입일자", "구분", "거래처", "수량", "단가", "총수량", "합계액", "부가세", "유효기간", "세소민오"},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "KH2301"}),
			want: []string{"2026-10-16|가람도매|60|2029-05-31|0031201", "2026-09-01|나래도매|30|2028-06-30|KH2301"},
		},
		{
			// 표 아래 화면 안내문에 머리 이름이 모여 있음 (앱 화면을 함께 찍은 사진): 그 줄을 머리로 보면 위의 표를 통째로 버린다
			name: "엉뚱한 글자를 머리로 봄",
			words: append(gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"}),
				gridWords([]float64{300, 380, 460, 540}, gridOpt{y0: 120}, []string{"사입일자", "거래처", "유효기간", "제조번호"})...),
			want: []string{"2026-10-16|가람도매|60|2029-05-31|0031201", "2026-09-01|나래도매|30|2028-06-30|0045107"},
		},
		{
			// 유효기간·제조번호 열이 아예 없는 화면: 줄은 내놓되 칸은 비워 둔다 (사용자가 채움)
			name: "유효기간·제조번호 열이 없음",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함"}),
			want: []string{"2026-10-16|가람도매|60||", "2026-09-01|나래도매|30||"},
			check: func(t *testing.T, res *LotPhotoResult) {
				if !strings.Contains(res.Note, "유효기간·제조번호 열이 보이지 않습니다") {
					t.Errorf("안내: %q", res.Note)
				}
			},
		},
		{
			// 작은 글자에서 소수점을 쉼표로 읽거나(120,00) 놓침(6000): 그대로 두면 수량이 100배가 된다
			name: "수량의 소수점을 잘못 읽음",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "4,00", "19,200", "120,00", "76,800", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "2.00", "19,200", "6000", "38,400", "포함", "20280630", "0045107"},
				[]string{"2026-05-14", "사입", "(주)나래도매", "5.00", "19,200", "150.00", "96,000", "포함", "20280630", "0045107"}),
			want: []string{
				"2026-10-16|가람도매|120|2029-05-31|0031201",
				"2026-09-01|나래도매|60|2028-06-30|0045107",
				"2026-05-14|나래도매|150|2028-06-30|0045107",
			},
			check: func(t *testing.T, res *LotPhotoResult) {
				if !isUnsure(res.Rows[0], "qty") || !isUnsure(res.Rows[1], "qty") || isUnsure(res.Rows[2], "qty") {
					t.Errorf("되돌린 수량만 '확인 필요': %+v", res.Rows)
				}
			},
		},
		{
			// 반드시 지키는 약속: 날짜가 있는 줄이 하나라도 있으면 0줄로 끝내지 않는다
			name:  "날짜만 읽힌 줄",
			words: gridWords(epharmCols, gridOpt{}, []string{"2026-10-16", "", "흐린글자"}),
			want:  []string{"2026-10-16||0||"},
			check: func(t *testing.T, res *LotPhotoResult) {
				if !strings.Contains(res.Note, "유효기간·제조번호를 읽지 못했습니다") {
					t.Errorf("안내: %q", res.Note)
				}
				unsureAll("date", "supplier", "qty", "expiry", "lot")(t, res)
			},
		},
		{
			// '반품'을 다른 글자로 읽어도 음수 수량·금액이면 사입이 아니다
			name: "반품 글자를 잘못 읽음",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-10-02", "반춤", "(주)나래도매", "-2.00", "19,200", "-60.00", "-38,400", "비과세", "20280630", "0045107"}),
			want:    []string{"2026-10-16|가람도매|60|2029-05-31|0031201"},
			skipped: 1,
		},
		{
			// 한국어 글자 인식이 없는 PC: 한글 칸은 엉뚱한 영문·기호로 나온다. 날짜·수량·유효기간·제조번호는 읽혀야 한다
			name: "한글을 못 읽음",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "AFSI", "(?)7tEtEoH", "2.00", "19,200", "60.00", "38,400", "EEt", "20290531", "0031201"},
				[]string{"2026-10-02", "tJ*", "(?)LfeHEoH", "-2.00", "19,200", "-60.00", "-38,400", "HIDfAil", "20280630", "0045107"},
				[]string{"2026-09-01", "AFSI", "(?)LfeHEoH", "1.00", "19,200", "30.00", "19,200", "EEt", "20280630", "0045107"}),
			want:    []string{"2026-10-16||60|2029-05-31|0031201", "2026-09-01||30|2028-06-30|0045107"},
			skipped: 1,
		},
		{
			// 사진이 기울어 줄의 왼쪽과 오른쪽 높이가 한 줄 가까이 다름: 줄 평균과 비교하면 날짜와 제조번호가 다른 줄로 갈라진다
			name: "기운 사진",
			words: gridWords(epharmCols, gridOpt{slant: 0.02},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"},
				[]string{"2026-05-14", "사입", "(주)나래도매", "3.00", "19,200", "90.00", "57,600", "포함", "20280630", "0045107"}),
			want: []string{
				"2026-10-16|가람도매|60|2029-05-31|0031201",
				"2026-09-01|나래도매|30|2028-06-30|0045107",
				"2026-05-14|나래도매|90|2028-06-30|0045107",
			},
		},
		{
			// OCR 이 한 칸을 여러 단어로 쪼갬 (날짜·금액·도매 이름)
			name: "칸이 쪼개짐",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-1|0-16", "사입", "(주)|가람도매", "2.00", "19,|200", "60.00", "38,400", "포함", "2029|0531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)|나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"}),
			want: []string{"2026-10-16|가람도매|60|2029-05-31|0031201", "2026-09-01|나래도매|30|2028-06-30|0045107"},
		},
		{
			// 숫자뿐인 제조번호 열에서 0 을 O 로 읽음 → 숫자로 되돌리고 '확인 필요'
			name: "숫자 제조번호를 영문으로 읽음",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"},
				[]string{"2026-05-14", "사입", "(주)나래도매", "3.00", "19,200", "90.00", "57,600", "포함", "20280630", "OO451O7"}),
			want: []string{
				"2026-10-16|가람도매|60|2029-05-31|0031201",
				"2026-09-01|나래도매|30|2028-06-30|0045107",
				"2026-05-14|나래도매|90|2028-06-30|0045107",
			},
			check: func(t *testing.T, res *LotPhotoResult) {
				if !isUnsure(res.Rows[2], "lot") || isUnsure(res.Rows[1], "lot") {
					t.Errorf("되돌린 칸만 '확인 필요': %+v", res.Rows)
				}
			},
		},
		{
			// 머리가 있는 표에서 줄 사이가 크게 벌어짐 (종이가 접힌 자리): 날짜가 있는 줄이면 표가 이어진다.
			// 그 아래 날짜 없는 글자(다른 표·화면 요소)에서 끝낸다.
			name: "표 중간의 빈 공간",
			words: append(append(gridWords(epharmCols, gridOpt{},
				header,
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"}),
				gridWords(epharmCols, gridOpt{y0: 140},
					[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"})...),
				gridWords(epharmCols, gridOpt{y0: 280},
					[]string{"합계", "다른", "화면", "글자", "", "", "", "", "유효기간", "ZZ9999"})...),
			want: []string{"2026-10-16|가람도매|60|2029-05-31|0031201", "2026-09-01|나래도매|30|2028-06-30|0045107"},
		},
		{
			// 도매 거래원장 같은 다른 배치: 약품명·규격·정수 수량, 유효기간은 구분 기호가 있는 날짜, 그 옆이 제조번호
			name: "다른 서류 배치",
			words: gridWords([]float64{10, 110, 300, 360, 430, 520, 620, 720}, gridOpt{},
				[]string{"2026-10-16", "누리정250마이크로그램", "30T", "5", "19,200", "96,000", "2029-05-31", "KH2301"},
				[]string{"2026-09-01", "누리정250마이크로그램", "30T", "2", "19,200", "38,400", "2028-06-30", "KH2207"}),
			want: []string{"2026-10-16||0|2029-05-31|KH2301", "2026-09-01||0|2028-06-30|KH2207"},
			check: func(t *testing.T, res *LotPhotoResult) {
				unsureAll("supplier", "qty")(t, res)
			},
		},
		{
			// 표 옆·아래의 날짜가 있는 다른 글자(작업 표시줄 시계 등)는 줄로 만들지 않는다
			name: "표 밖의 날짜",
			words: append(gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"}),
				gridWords([]float64{700, 760}, gridOpt{y0: 200}, []string{"오후", "2026-11-20"})...),
			want: []string{"2026-10-16|가람도매|60|2029-05-31|0031201", "2026-09-01|나래도매|30|2028-06-30|0045107"},
		},
		{
			// 표 아래 안내문을 머리로 잘못 봤고, 표에는 유효기간·제조번호 열이 없음: 그래도 0줄이 아니다
			name: "엉뚱한 머리 + 유효기간·제조번호 열 없음",
			words: append(gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함"}),
				gridWords([]float64{300, 380, 460, 540}, gridOpt{y0: 120}, []string{"사입일자", "거래처", "유효기간", "제조번호"})...),
			want: []string{"2026-10-16|가람도매|60||", "2026-09-01|나래도매|30||"},
		},
		{
			// OCR 이 유효기간과 제조번호를 한 단어로 붙여 읽음
			name: "유효기간과 제조번호가 붙음",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "202806300045107", ""},
				[]string{"2026-05-14", "사입", "(주)나래도매", "3.00", "19,200", "90.00", "57,600", "포함", "20280630", "0052209"}),
			want: []string{
				"2026-10-16|가람도매|60|2029-05-31|0031201",
				"2026-09-01|나래도매|30|2028-06-30|0045107",
				"2026-05-14|나래도매|90|2028-06-30|0052209",
			},
			check: func(t *testing.T, res *LotPhotoResult) {
				if !isUnsure(res.Rows[1], "expiry") || !isUnsure(res.Rows[1], "lot") {
					t.Errorf("나눈 칸은 '확인 필요': %+v", res.Rows[1])
				}
			},
		},
		{
			// 유효기간을 못 읽은 줄 (숫자 일부가 깨짐): 같은 열의 다른 줄로 열을 알고 있으므로 제조번호는 그대로 읽는다
			name: "한 줄의 유효기간이 깨짐",
			words: gridWords(epharmCols, gridOpt{},
				[]string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"},
				[]string{"2026-09-01", "사입", "(주)나래도매", "1.00", "19,200", "30.00", "19,200", "포함", "2028#6?0", "0045107"},
				[]string{"2026-05-14", "사입", "(주)나래도매", "3.00", "19,200", "90.00", "57,600", "포함", "20280630", "0045107"}),
			want: []string{
				"2026-10-16|가람도매|60|2029-05-31|0031201",
				"2026-09-01|나래도매|30|2028-06-30|0045107", // 같은 제조번호의 유효기간으로 채움 (확인 필요)
				"2026-05-14|나래도매|90|2028-06-30|0045107",
			},
			check: func(t *testing.T, res *LotPhotoResult) {
				if !isUnsure(res.Rows[1], "expiry") {
					t.Errorf("채운 유효기간은 '확인 필요': %+v", res.Rows[1])
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := lotTestService(t)
			got := runLotOCR(t, s, fakeOCR(c.words...))
			if g := lotRowTexts(got.Rows); strings.Join(g, "\n") != strings.Join(c.want, "\n") || got.Skipped != c.skipped {
				t.Fatalf("skipped=%d (기대 %d) note=%q\n읽음:\n%s\n기대:\n%s", got.Skipped, c.skipped, got.Note, strings.Join(g, "\n"), strings.Join(c.want, "\n"))
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

// 약속: OCR 이 날짜 모양 칸이 있는 (반품·조정이 아닌) 줄을 하나라도 돌려주면 결과는 0줄이 아니다.
// 칸을 무작위로 지우고 깨뜨린 표로 확인한다.
func TestLotOCRNeverZeroRowsWithDatedLine(t *testing.T) {
	s := lotTestService(t)
	rng := rand.New(rand.NewSource(20261126))
	junk := []string{"", "", "흐림", "#", "1", "A", "가", "0", "ㅁㅁ", "12", "포함", "(주)"}
	for i := 0; i < 500; i++ {
		row := []string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"}
		for j := 1; j < len(row); j++ {
			if rng.Intn(2) == 0 {
				row[j] = junk[rng.Intn(len(junk))]
			}
		}
		var rows [][]string
		if rng.Intn(3) == 0 {
			rows = append(rows, []string{"사입일자", junk[rng.Intn(len(junk))], "거래처", "", "", "", "", "", "유효기간", junk[rng.Intn(len(junk))]})
		}
		rows = append(rows, row)
		if rng.Intn(4) == 0 { // 표 아래의 다른 글자가 머리처럼 읽힘
			rows = append(rows, []string{"", "", "사입일자", "거래처", "", "유효기간", "제조번호"})
		}
		if rng.Intn(2) == 0 {
			rows = append(rows, []string{"2026-10-02", "반품", "(주)나래도매", "-2.00", "19,200", "-60.00", "-38,400", "비과세", "20280630", "0045107"})
		}
		got := runLotOCR(t, s, fakeOCR(gridWords(epharmCols, gridOpt{}, rows...)...))
		if len(got.Rows) == 0 {
			t.Fatalf("%d번째: 날짜가 있는 사입 줄이 있는데 0줄 — %q", i, rows)
		}
		if got.Rows[0].Date != "2026-10-16" {
			t.Fatalf("%d번째: 날짜 %q — %q", i, got.Rows[0].Date, rows)
		}
	}
}

// 사진은 두 버전으로 읽는다. 한 버전이 '반품'을 잘못 읽어 사입으로 봐도, 다른 버전이 반품으로 본 줄은 되살리지 않는다.
func TestMergeTablesKeepsReturnsOut(t *testing.T) {
	s := lotTestService(t)
	sup := s.supplierMatcher()
	purchase := []string{"2026-10-16", "사입", "(주)가람도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290531", "0031201"}
	a := parseLotTable(fakeOCR(gridWords(epharmCols, gridOpt{}, purchase,
		[]string{"2026-10-02", "반품", "(주)나래도매", "-2.00", "19,200", "-60.00", "-38,400", "비과세", "20280630", "0045107"})...), 1, sup)
	// 다른 버전: '반품'과 음수 부호를 모두 놓침
	b := parseLotTable(fakeOCR(gridWords(epharmCols, gridOpt{}, purchase,
		[]string{"2026-10-02", "반춤", "(주)나래도매", "2.00", "19,200", "60.00", "38,400", "비과세", "20280630", "0045107"})...), 1, sup)
	if len(a.rows) != 1 || len(b.rows) != 2 {
		t.Fatalf("버전별 줄 수: %d, %d", len(a.rows), len(b.rows))
	}
	for _, order := range [][]parsedTable{{a, b}, {b, a}} {
		res := mergeTables(order)
		if len(res.Rows) != 1 || res.Rows[0].Lot != "0031201" || res.Skipped != 1 {
			t.Fatalf("반품 줄이 되살아남: skipped=%d %v", res.Skipped, lotRowTexts(res.Rows))
		}
	}
}

// 사입현황(DB)에 아직 없는 새 사입 줄: 날짜를 또렷이 읽었으면 순서만으로 옛 사입과 짝짓지 않는다 (날짜·도매·수량이 바뀐다).
func TestAlignKeepsClearlyReadNewPurchase(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s) // 900000010 사입: 10-16 다솜 7 · 05-14 나래 2,2,1 · 03-25 가람 1 (×30정)
	got := runLotOCR(t, s, fakeOCR(gridWords(epharmCols, gridOpt{},
		[]string{"2026-10-16", "사입", "다솜도매(주)", "7.00", "19,200", "210.00", "134,400", "포함", "20290531", "0031201"},
		[]string{"2026-09-01", "사입", "(주)나래도매", "4.00", "19,200", "120.00", "76,800", "포함", "20290228", "0052209"}, // DB 에 없는 사입
		[]string{"2026-08-20", "사입", "(주)나래도매", "2.00", "19,200", "60.00", "38,400", "포함", "20290228", "0052209"},  // DB 에 없는 사입 — 05-14 사입과 도매·수량이 같다
		[]string{"2026-03-25", "사입", "(주)가람도매", "1.00", "19,200", "30.00", "19,200", "포함", "20280630", "0045107"},
	)...))
	want := []string{
		"2026-10-16|다솜도매|210|2029-05-31|0031201",
		"2026-09-01|나래도매|120|2029-02-28|0052209",
		"2026-08-20|나래도매|60|2029-02-28|0052209",
		"2026-03-25|가람도매|30|2028-06-30|0045107",
	}
	if g := lotRowTexts(got.Rows); strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Fatalf("읽음:\n%s", strings.Join(g, "\n"))
	}
	if got.Rows[0].Matched != "date" || got.Rows[1].Matched != "" || got.Rows[2].Matched != "" || got.Rows[3].Matched != "date" {
		t.Fatalf("짝짓기: %q %q %q %q", got.Rows[0].Matched, got.Rows[1].Matched, got.Rows[2].Matched, got.Rows[3].Matched)
	}
}

func TestClassifyTok(t *testing.T) {
	for in, want := range map[string]tokKind{
		"2026-10-16": tkDate, "2026.10.16": tkDate, "꼬26-01-05": tkDate, "2O26-1O-16": tkDate,
		"20290531": tkDate8, "2029口531": tkDate8,
		"사입": tkKind, "반품": tkKind, "재고조정재고정리": tkKind,
		"포함": tkTax, "비과세": tkTax,
		"60.00": tkDec, "-2.00": tkDec, "1 2口.口口": tkDec,
		"19,200": tkAmt, "-38,400": tkAmt, "590": tkAmt,
		"0031201": tkCode, "KH2301": tkCode, "LOT-1001": tkCode, "31007-2": tkCode, "AB12口4": tkCode, "20291341": tkCode,
		"(주)가람도매": tkText, "이기도매": tkText, "이기": tkText, "기이이": tkText,
	} {
		if got := classifyTok(in).kind; got != want {
			t.Errorf("%q → %d, 기대 %d", in, got, want)
		}
	}
}

// 작은 이미지라도 카메라 사진(잡음)이면 사진으로 보고 두 버전을 읽는다. 화면 캡처는 한 장.
func TestOCRVariantsSmallPhoto(t *testing.T) {
	flat := image.NewGray(image.Rect(0, 0, 700, 170))
	for i := range flat.Pix {
		flat.Pix[i] = 250
	}
	for x := 20; x < 600; x += 7 {
		for y := 20; y < 30; y++ {
			flat.Pix[y*flat.Stride+x] = 20
		}
	}
	if v, mode := ocrVariants(flat); mode != "screenshot" || len(v) != 1 {
		t.Fatalf("화면 캡처: mode=%s variants=%d", mode, len(v))
	}
	rng := rand.New(rand.NewSource(1))
	noisy := image.NewGray(flat.Rect)
	for i, p := range flat.Pix {
		noisy.Pix[i] = uint8(int(p) - rng.Intn(24))
	}
	if v, mode := ocrVariants(noisy); mode != "photo" || len(v) != 2 || v[0].scale != v[1].scale {
		t.Fatalf("작은 사진: mode=%s variants=%d", mode, len(v))
	}
}
