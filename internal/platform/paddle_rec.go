package platform

import (
	"math"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// 글자 읽기(korean_PP-OCRv5_mobile_rec) 의 전처리·후처리 — PaddleOCR 의 get_rotate_crop_image · RecResizeImg · CTCLabelDecode 와 같게.

const (
	recHeight   = 48
	recBaseW    = 320 // 한 묶음의 최소 폭 (rec_image_shape 3×48×320)
	recBatch    = 6
	recMinConf  = 0.5 // 이보다 확신이 낮은 글줄은 버린다
	recWordGap  = 1.6 // 글자 사이가 글줄 높이의 이 배수보다 벌어지면 다른 낱말(다른 칸)로 본다
	recMaxRatio = 60  // 너무 긴 글줄은 이 가로세로비까지만
)

// cropLine : 글줄 상자를 잘라 편다. 세로로 긴 상자(높이 ≥ 폭×1.5)는 눕힌다.
func cropLine(img *bgrImg, q quad) (crop *bgrImg, rotated bool) {
	cw := int(math.Max(dist(q[0], q[1]), dist(q[2], q[3])))
	ch := int(math.Max(dist(q[0], q[3]), dist(q[1], q[2])))
	if cw < 1 || ch < 1 {
		return nil, false
	}
	c := warpQuad(img, [4][2]float64{{q[0].x, q[0].y}, {q[1].x, q[1].y}, {q[2].x, q[2].y}, {q[3].x, q[3].y}}, cw, ch)
	if float64(ch)/float64(cw) >= 1.5 {
		return rot90(c), true
	}
	return c, false
}

// recChar : 읽은 글자 하나와 가로 위치(모델 출력의 몇 번째 칸인지)·확신
type recChar struct {
	r rune
	t int
	p float64
}

type recLine struct {
	text   string
	conf   float64
	chars  []recChar
	steps  int // 모델 출력의 가로 칸 수
	padW   int // 모델 입력 폭 (묶음에서 가장 긴 글줄에 맞춤)
	lineW  int // 그중 이 글줄이 차지한 폭
	failed bool
}

// recInput : 한 묶음의 모델 입력 (N×3×48×W). 높이 48 로 맞추고 오른쪽 빈 곳은 0.
func recInput(crops []*bgrImg) (data []float32, w int, lineW []int) {
	maxRatio := float64(recBaseW) / recHeight
	for _, c := range crops {
		maxRatio = math.Max(maxRatio, float64(c.w)/float64(c.h))
	}
	maxRatio = math.Min(maxRatio, recMaxRatio)
	w = int(recHeight * maxRatio)
	n := recHeight * w
	data = make([]float32, len(crops)*3*n)
	lineW = make([]int, len(crops))
	for i, c := range crops {
		rw := int(math.Ceil(recHeight * float64(c.w) / float64(c.h)))
		if rw > w {
			rw = w
		}
		if rw < 1 {
			rw = 1
		}
		lineW[i] = rw
		r := resizeLinear(c, rw, recHeight)
		base := i * 3 * n
		for y := 0; y < recHeight; y++ {
			for x := 0; x < rw; x++ {
				p := r.pix[(y*rw+x)*3:]
				o := base + y*w + x
				data[o] = float32(p[0])/127.5 - 1
				data[o+n] = float32(p[1])/127.5 - 1
				data[o+2*n] = float32(p[2])/127.5 - 1
			}
		}
	}
	return data, w, lineW
}

// ctcDecode : 칸마다 가장 확률 높은 글자를 고르고, 이어진 같은 글자는 하나로, 빈칸 기호(0번)는 뺀다.
// 글자 번호 = 사전 줄 번호 + 1, 마지막 번호는 띄어쓰기.
func ctcDecode(probs []float32, steps, classes int, dict []rune) (chars []recChar) {
	prev := 0
	for t := 0; t < steps; t++ {
		row := probs[t*classes : (t+1)*classes]
		best, bp := 0, row[0]
		for i, p := range row {
			if p > bp {
				best, bp = i, p
			}
		}
		if best != 0 && best != prev {
			r := ' '
			if best-1 < len(dict) {
				r = dict[best-1]
			}
			chars = append(chars, recChar{r: r, t: t, p: float64(bp)})
		}
		prev = best
	}
	return chars
}

func newRecLine(chars []recChar, steps, padW, lineW int) recLine {
	l := recLine{chars: chars, steps: steps, padW: padW, lineW: lineW}
	var sb strings.Builder
	var sum float64
	for _, c := range chars {
		sb.WriteRune(c.r)
		sum += c.p
	}
	l.text = norm.NFC.String(strings.TrimSpace(sb.String()))
	if len(chars) > 0 {
		l.conf = sum / float64(len(chars))
	}
	return l
}

// recWord : 글줄 안의 낱말 하나 — 글줄 폭에서 차지하는 구간(0~1)
type recWord struct {
	text   string
	f0, f1 float64
	conf   float64
}

// words : 글줄을 낱말로 나눈다 — 띄어쓰기, 또는 글자 사이가 크게 벌어진 곳(검출이 옆 칸까지 한 줄로 묶은 경우).
// 표 읽기는 낱말의 가로 위치로 열을 정하므로, 낱말마다 위치가 있어야 한다.
func (l recLine) words() []recWord {
	if len(l.chars) == 0 || l.steps == 0 || l.lineW == 0 {
		return nil
	}
	step := float64(l.padW) / float64(l.steps) // 출력 한 칸의 폭 (입력 화소)
	center := func(c recChar) float64 { return (float64(c.t) + 0.5) * step }
	// 글자 간격의 가운데값 (낱말 양 끝을 반 글자만큼 넓히는 데 쓴다)
	var gaps []float64
	for i := 1; i < len(l.chars); i++ {
		if l.chars[i].r != ' ' && l.chars[i-1].r != ' ' {
			gaps = append(gaps, center(l.chars[i])-center(l.chars[i-1]))
		}
	}
	adv := recHeight * 0.6
	if len(gaps) > 0 {
		sort.Float64s(gaps)
		adv = math.Min(gaps[len(gaps)/2], recHeight*1.2)
	}
	var out []recWord
	var cur []recChar
	flush := func() {
		if len(cur) == 0 {
			return
		}
		var sb strings.Builder
		var sum float64
		for _, c := range cur {
			sb.WriteRune(c.r)
			sum += c.p
		}
		x0, x1 := center(cur[0])-adv/2, center(cur[len(cur)-1])+adv/2
		out = append(out, recWord{
			text: norm.NFC.String(sb.String()),
			f0:   math.Max(x0/float64(l.lineW), 0), f1: math.Min(x1/float64(l.lineW), 1),
			conf: sum / float64(len(cur)),
		})
		cur = cur[:0]
	}
	for _, c := range l.chars {
		if c.r == ' ' {
			flush()
			continue
		}
		if len(cur) > 0 && center(c)-center(cur[len(cur)-1]) > recWordGap*recHeight {
			flush()
		}
		cur = append(cur, c)
	}
	flush()
	if len(out) > 0 { // 양 끝은 검출 상자의 끝에 맞춘다
		out[0].f0, out[len(out)-1].f1 = 0, 1
	}
	return out
}
