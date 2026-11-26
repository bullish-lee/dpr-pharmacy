package platform

import (
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
)

// PaddleOCR 전처리에 쓰는 그림 손질 (OpenCV 없이, PaddleOCR 이 쓰는 cv2 함수와 같은 계산).

// bgrImg : 8비트 3채널 그림. 채널 순서는 B·G·R (PaddleOCR 이 cv2 로 읽는 순서 그대로 — 모델이 이 순서로 학습됨)
type bgrImg struct {
	w, h int
	pix  []uint8 // pix[(y*w+x)*3+c]
}

func newBGR(w, h int) *bgrImg { return &bgrImg{w: w, h: h, pix: make([]uint8, w*h*3)} }

// loadBGR : 그림 파일(PNG·JPG·GIF)을 읽는다. 투명한 곳은 흰 바탕.
func loadBGR(path string) (*bgrImg, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return toBGR(src), nil
}

func toBGR(src image.Image) *bgrImg {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	out := newBGR(w, h)
	if g, ok := src.(*image.Gray); ok {
		for y := 0; y < h; y++ {
			row := g.Pix[y*g.Stride : y*g.Stride+w]
			o := out.pix[y*w*3:]
			for x, v := range row {
				o[x*3], o[x*3+1], o[x*3+2] = v, v, v
			}
		}
		return out
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Over)
	for i, j := 0, 0; i < len(rgba.Pix); i, j = i+4, j+3 {
		out.pix[j], out.pix[j+1], out.pix[j+2] = rgba.Pix[i+2], rgba.Pix[i+1], rgba.Pix[i]
	}
	return out
}

func clampI(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func toU8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// resizeLinear : cv2.resize(INTER_LINEAR) 와 같은 방식 (화소 중심 맞춤, 가장자리는 끝 화소 반복)
func resizeLinear(src *bgrImg, dw, dh int) *bgrImg {
	if dw == src.w && dh == src.h {
		return src
	}
	out := newBGR(dw, dh)
	type tap struct {
		a, b int
		f    float64
	}
	taps := func(dn, sn int) []tap {
		t := make([]tap, dn)
		k := float64(sn) / float64(dn)
		for i := range t {
			s := (float64(i)+0.5)*k - 0.5
			a := int(math.Floor(s))
			f := s - float64(a)
			if a < 0 {
				a, f = 0, 0
			}
			b := a + 1
			if b >= sn {
				b = sn - 1
				if a >= sn {
					a = sn - 1
				}
			}
			t[i] = tap{a, b, f}
		}
		return t
	}
	tx, ty := taps(dw, src.w), taps(dh, src.h)
	for y := 0; y < dh; y++ {
		r0 := src.pix[ty[y].a*src.w*3:]
		r1 := src.pix[ty[y].b*src.w*3:]
		fy := ty[y].f
		o := out.pix[y*dw*3:]
		for x := 0; x < dw; x++ {
			a, b, fx := tx[x].a*3, tx[x].b*3, tx[x].f
			for c := 0; c < 3; c++ {
				top := float64(r0[a+c])*(1-fx) + float64(r0[b+c])*fx
				bot := float64(r1[a+c])*(1-fx) + float64(r1[b+c])*fx
				o[x*3+c] = toU8(top*(1-fy) + bot*fy)
			}
		}
	}
	return out
}

// resizeArea : 줄일 때 쓴다 — 넓이 평균 (cv2.INTER_AREA 와 같은 방식). 모아레·잡음이 덜 남는다.
func resizeArea(src *bgrImg, dw, dh int) *bgrImg {
	if dw >= src.w || dh >= src.h {
		return resizeLinear(src, dw, dh)
	}
	type span struct {
		i0, i1 int
		w0, w1 float64 // 첫·끝 화소의 비중 (가운데는 1)
	}
	spans := func(dn, sn int) []span {
		s := make([]span, dn)
		k := float64(sn) / float64(dn)
		for i := range s {
			a, b := float64(i)*k, float64(i+1)*k
			i0, i1 := int(math.Floor(a)), int(math.Ceil(b))-1
			if i1 >= sn {
				i1 = sn - 1
			}
			if i1 < i0 {
				i1 = i0
			}
			s[i] = span{i0, i1, float64(i0+1) - a, b - float64(i1)}
			if i0 == i1 {
				s[i].w0, s[i].w1 = b-a, 0
			}
		}
		return s
	}
	sx, sy := spans(dw, src.w), spans(dh, src.h)
	// 가로 먼저
	tmp := make([]float32, dw*src.h*3)
	for y := 0; y < src.h; y++ {
		row := src.pix[y*src.w*3:]
		o := tmp[y*dw*3:]
		for x := 0; x < dw; x++ {
			s := sx[x]
			var acc [3]float64
			var wsum float64
			for i := s.i0; i <= s.i1; i++ {
				w := 1.0
				if i == s.i0 {
					w = s.w0
				} else if i == s.i1 {
					w = s.w1
				}
				wsum += w
				acc[0] += w * float64(row[i*3])
				acc[1] += w * float64(row[i*3+1])
				acc[2] += w * float64(row[i*3+2])
			}
			o[x*3], o[x*3+1], o[x*3+2] = float32(acc[0]/wsum), float32(acc[1]/wsum), float32(acc[2]/wsum)
		}
	}
	out := newBGR(dw, dh)
	for y := 0; y < dh; y++ {
		s := sy[y]
		o := out.pix[y*dw*3:]
		for x := 0; x < dw*3; x++ {
			var acc, wsum float64
			for i := s.i0; i <= s.i1; i++ {
				w := 1.0
				if i == s.i0 {
					w = s.w0
				} else if i == s.i1 {
					w = s.w1
				}
				wsum += w
				acc += w * float64(tmp[i*dw*3+x])
			}
			o[x] = toU8(acc / wsum)
		}
	}
	return out
}

// cubicW : cv2 INTER_CUBIC 의 가중치 (a = -0.75)
func cubicW(f float64) [4]float64 {
	const a = -0.75
	var w [4]float64
	w[0] = ((a*(f+1)-5*a)*(f+1)+8*a)*(f+1) - 4*a
	w[1] = ((a+2)*f-(a+3))*f*f + 1
	w[2] = ((a+2)*(1-f)-(a+3))*(1-f)*(1-f) + 1
	w[3] = 1 - w[0] - w[1] - w[2]
	return w
}

// warpQuad : 사각형 q(왼위·오른위·오른아래·왼아래)를 cw×ch 로 펴서 잘라 낸다.
// PaddleOCR 의 get_rotate_crop_image (cv2.warpPerspective, INTER_CUBIC, BORDER_REPLICATE) 와 같다.
// 검출 상자는 (기울어진) 직사각형이라 원근 변환이 아핀 변환과 같다.
func warpQuad(src *bgrImg, q [4][2]float64, cw, ch int) *bgrImg {
	out := newBGR(cw, ch)
	ux, uy := (q[1][0]-q[0][0])/float64(cw), (q[1][1]-q[0][1])/float64(cw)
	vx, vy := (q[3][0]-q[0][0])/float64(ch), (q[3][1]-q[0][1])/float64(ch)
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			sx := q[0][0] + ux*float64(x) + vx*float64(y)
			sy := q[0][1] + uy*float64(x) + vy*float64(y)
			ix, iy := int(math.Floor(sx)), int(math.Floor(sy))
			fx, fy := sx-float64(ix), sy-float64(iy)
			o := out.pix[(y*cw+x)*3:]
			if fx < 1e-6 && fy < 1e-6 { // 기울지 않은 상자: 화소 그대로
				p := src.pix[(clampI(iy, 0, src.h-1)*src.w+clampI(ix, 0, src.w-1))*3:]
				o[0], o[1], o[2] = p[0], p[1], p[2]
				continue
			}
			wx, wy := cubicW(fx), cubicW(fy)
			var acc [3]float64
			for j := 0; j < 4; j++ {
				row := clampI(iy-1+j, 0, src.h-1) * src.w
				for i := 0; i < 4; i++ {
					p := src.pix[(row+clampI(ix-1+i, 0, src.w-1))*3:]
					w := wx[i] * wy[j]
					acc[0] += w * float64(p[0])
					acc[1] += w * float64(p[1])
					acc[2] += w * float64(p[2])
				}
			}
			o[0], o[1], o[2] = toU8(acc[0]), toU8(acc[1]), toU8(acc[2])
		}
	}
	return out
}

// rot90 : 시계 반대 방향으로 90도 (np.rot90) — 세로로 긴 글줄을 눕힌다
func rot90(src *bgrImg) *bgrImg {
	out := newBGR(src.h, src.w)
	for y := 0; y < src.h; y++ {
		for x := 0; x < src.w; x++ {
			ny, nx := src.w-1-x, y
			copy(out.pix[(ny*out.w+nx)*3:(ny*out.w+nx)*3+3], src.pix[(y*src.w+x)*3:(y*src.w+x)*3+3])
		}
	}
	return out
}
