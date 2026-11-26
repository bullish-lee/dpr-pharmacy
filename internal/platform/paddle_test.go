package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// 시험 프로그램도 DPR.exe 처럼 자기 자신을 PaddleOCR 일꾼으로 실행할 수 있게 한다 (PaddleOCR 이 일꾼 경로로 돈다).
func TestMain(m *testing.M) {
	if RunPaddleWorker(os.Args) {
		return
	}
	os.Exit(m.Run())
}

func sum256(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// 프로그램에 든 모델·사전이 고정한 파일 그대로인지 (paddle.go 의 출처·해시)
func TestPaddleAssetsPinned(t *testing.T) {
	for _, c := range []struct {
		name, got, want string
	}{
		{"검출 모델", sum256(paddleDetModel), paddleDetSHA256},
		{"글자 모델", sum256(paddleRecModel), paddleRecSHA256},
		{"글자 사전", sum256([]byte(paddleDictText)), paddleDictSHA256},
	} {
		if c.got != c.want {
			t.Errorf("%s 해시가 다릅니다: %s", c.name, c.got)
		}
	}
	d := paddleDict()
	if len(d) != 11945 { // 모델 출력 11947 = 빈칸 기호 + 사전 + 띄어쓰기
		t.Fatalf("사전 %d자", len(d))
	}
	seen := map[rune]bool{}
	for _, r := range d {
		if r == '�' || seen[r] {
			t.Fatalf("사전에 잘못된 글자: %q", r)
		}
		seen[r] = true
	}
	for _, r := range "가람도매0123456789-LOT" {
		if !seen[r] {
			t.Errorf("사전에 %q 없음", r)
		}
	}
}

func TestPaddleMinAreaRect(t *testing.T) {
	// 축에 나란한 10×4
	_, w, h := minAreaRect([]pt{{0, 0}, {10, 0}, {10, 4}, {0, 4}, {5, 2}})
	if math.Abs(math.Max(w, h)-10) > 1e-9 || math.Abs(math.Min(w, h)-4) > 1e-9 {
		t.Fatalf("w=%v h=%v", w, h)
	}
	// 30도 기운 20×6
	c, s := math.Cos(math.Pi/6), math.Sin(math.Pi/6)
	var ps []pt
	for _, p := range []pt{{0, 0}, {20, 0}, {20, 6}, {0, 6}, {10, 3}} {
		ps = append(ps, pt{100 + p.x*c - p.y*s, 50 + p.x*s + p.y*c})
	}
	corners, w, h := minAreaRect(ps)
	if math.Abs(math.Max(w, h)-20) > 1e-6 || math.Abs(math.Min(w, h)-6) > 1e-6 {
		t.Fatalf("w=%v h=%v", w, h)
	}
	q := orderQuad(corners)
	if !(q[0].x < q[1].x && q[0].y < q[3].y && q[1].y < q[2].y) {
		t.Fatalf("꼭짓점 순서: %v", q)
	}
	if _, w, h := minAreaRect([]pt{{3, 3}}); w != 0 || h != 0 {
		t.Fatal("점 하나")
	}
}

// 확률 지도에 글줄 두 개 + 잡음 → 상자 두 개, 위에서 아래·왼쪽에서 오른쪽 순서, 원본 크기 좌표
func TestPaddleDetBoxes(t *testing.T) {
	const w, h = 160, 96
	prob := make([]float32, w*h)
	fill := func(x0, y0, x1, y1 int, v float32) {
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				prob[y*w+x] = v
			}
		}
	}
	fill(90, 20, 140, 29, 0.9)   // 오른쪽 글줄
	fill(10, 22, 60, 31, 0.9)    // 왼쪽 글줄 (같은 줄)
	fill(10, 60, 40, 69, 0.9)    // 아랫줄
	fill(100, 60, 101, 61, 0.9)  // 너무 작음
	fill(120, 70, 150, 80, 0.45) // 흐림 (평균 0.6 미만)
	boxes := detBoxes(prob, w, h, w*2, h*2)
	if len(boxes) != 3 {
		t.Fatalf("상자 %d개: %v", len(boxes), boxes)
	}
	if !(boxes[0][0].x < boxes[1][0].x && boxes[1][0].y < boxes[2][0].y) {
		t.Fatalf("순서: %v", boxes)
	}
	// 왼쪽 글줄: 지도에서 (10,22)-(60,31), 넓히기 d = 50*9*1.5/(2*59) ≈ 5.72 → 원본(2배) 좌표로 약 (8.6,32.6)-(131.4,73.4)
	b := boxes[0]
	for i, want := range []pt{{9, 33}, {131, 33}, {131, 73}, {9, 73}} {
		if math.Abs(b[i].x-want.x) > 1.5 || math.Abs(b[i].y-want.y) > 1.5 {
			t.Fatalf("상자 %v, 기대 꼭짓점 %d ≈ %v", b, i, want)
		}
	}
}

func TestPaddleCTCDecode(t *testing.T) {
	dict := []rune("가나1-")
	// 번호: 0 빈칸, 1 가, 2 나, 3 '1', 4 '-', 5 띄어쓰기
	seq := []int{0, 1, 1, 0, 1, 2, 0, 0, 5, 3, 3, 4, 0}
	classes := len(dict) + 2
	probs := make([]float32, len(seq)*classes)
	for t, k := range seq {
		probs[t*classes+k] = 0.9
	}
	chars := ctcDecode(probs, len(seq), classes, dict)
	l := newRecLine(chars, len(seq), len(seq)*8, len(seq)*8)
	if l.text != "가가나 1-" {
		t.Fatalf("%q", l.text)
	}
	if math.Abs(l.conf-0.9) > 1e-6 {
		t.Fatalf("conf %v", l.conf)
	}
	ws := l.words()
	if len(ws) != 2 || ws[0].text != "가가나" || ws[1].text != "1-" {
		t.Fatalf("%+v", ws)
	}
	if ws[0].f0 != 0 || ws[1].f1 != 1 || !(ws[0].f1 < ws[1].f0) {
		t.Fatalf("낱말 위치: %+v", ws)
	}
}

// 검출이 두 칸을 한 줄로 묶어도, 글자 사이가 크게 벌어진 곳에서 낱말을 나눈다 (띄어쓰기가 없어도)
func TestPaddleWordsSplitOnGap(t *testing.T) {
	var chars []recChar
	for i, r := range []rune("가람도매") {
		chars = append(chars, recChar{r: r, t: 2 + i*5, p: 0.9})
	}
	for i, r := range []rune("30.00") {
		chars = append(chars, recChar{r: r, t: 60 + i*3, p: 0.8})
	}
	l := newRecLine(chars, 80, 640, 640)
	ws := l.words()
	if len(ws) != 2 || ws[0].text != "가람도매" || ws[1].text != "30.00" {
		t.Fatalf("%+v", ws)
	}
	if !(ws[0].f1 < 0.4 && ws[1].f0 > 0.6) {
		t.Fatalf("낱말 위치: %+v", ws)
	}
}

func TestPaddleResize(t *testing.T) {
	src := newBGR(4, 2)
	for i := range src.pix {
		src.pix[i] = uint8(i * 10)
	}
	if r := resizeLinear(src, 4, 2); r != src {
		t.Fatal("같은 크기는 그대로")
	}
	up := resizeLinear(src, 8, 4)
	if up.w != 8 || up.h != 4 || up.pix[0] != src.pix[0] || up.pix[len(up.pix)-1] != src.pix[len(src.pix)-1] {
		t.Fatalf("확대: %v", up.pix)
	}
	// 2배 줄이기 = 2×2 평균
	big := newBGR(4, 4)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			for c := 0; c < 3; c++ {
				big.pix[(y*4+x)*3+c] = uint8(10*(y/2) + 100*(x/2) + c)
			}
		}
	}
	down := resizeArea(big, 2, 2)
	if down.pix[0] != 0 || down.pix[3] != 100 || down.pix[6] != 10 || down.pix[9+1] != 111 {
		t.Fatalf("축소: %v", down.pix)
	}
	// 기울지 않은 상자는 화소 그대로 잘린다
	crop := warpQuad(big, [4][2]float64{{2, 0}, {4, 0}, {4, 2}, {2, 2}}, 2, 2)
	if crop.pix[0] != 100 || crop.pix[2] != 102 {
		t.Fatalf("자르기: %v", crop.pix)
	}
	if r := rot90(src); r.w != 2 || r.h != 4 || r.pix[0] != src.pix[3*3] {
		t.Fatalf("돌리기: %v", r.pix)
	}
}

// 쓸 수 없는 곳(onnxruntime 없음)에서는 오류로 돌려주고 죽지 않는다 — 부르는 쪽은 Windows 글자 인식만으로 계속한다
func TestPaddleOCRDegradesGracefully(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if res, err := PaddleOCR(ctx, nil); res != nil || err != nil {
		t.Fatalf("빈 입력: %v %v", res, err)
	}
	res, err := PaddleOCR(ctx, []string{"없는-그림.png"})
	if err == nil {
		t.Fatalf("없는 그림인데 오류가 없음: %+v", res)
	}
	if !errors.Is(err, ErrPaddleUnavailable) {
		t.Fatalf("ErrPaddleUnavailable 이어야 함: %v", err)
	}
	c := paddleSelfTest(ctx)
	t.Logf("점검: ok=%v runtime=%q korean=%v found=%d ms=%d problem=%q", c.OK, c.Runtime, c.Korean, c.Found, c.Ms, c.Problem)
	if !c.OK && c.Problem == "" {
		t.Fatal("안 되면 이유가 있어야 함")
	}
	// 되는 곳에서 점검 그림을 다 읽는지는 개발 PC 에서만 따진다 (다른 PC·CI 에서는 기록만 — TestPaddleOCRBenchmark)
	if c.OK && os.Getenv(paddleStrictEnv) == "1" && (c.Found != 3 || !c.Korean || !strings.Contains(c.Text, "가람도매")) {
		t.Fatalf("점검 그림을 다 읽어야 함: %+v", c)
	}
}
