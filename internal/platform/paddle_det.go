package platform

import (
	"math"
	"sort"
)

// 글줄 검출(PP-OCRv5_mobile_det) 의 전처리·후처리 — PaddleOCR 의 DetResizeForTest · NormalizeImage · DBPostProcess 와 같게.
// 값은 모델에 딸린 inference.yml 그대로: thresh 0.3, box_thresh 0.6, unclip_ratio 1.5, 평균·표준편차는 ImageNet 값.

const (
	detThresh      = 0.3
	detBoxThresh   = 0.6
	detUnclipRatio = 1.5
	detMinSize     = 3
	detMaxBoxes    = 1000
	detMinSide     = 64 // 이보다 작은 변은 키운다 (PaddleOCR 기본: limit_side_len 64, limit_type min)
)

var (
	detMean = [3]float64{0.485, 0.456, 0.406}
	detStd  = [3]float64{0.229, 0.224, 0.225}
)

// detInput : 검출 모델 입력 (1×3×H×W, H·W 는 32의 배수)
func detInput(img *bgrImg) (data []float32, h, w int) {
	ratio := 1.0
	if m := min(img.w, img.h); m < detMinSide {
		ratio = detMinSide / float64(m)
	}
	h = max(int(math.Round(float64(int(float64(img.h)*ratio))/32))*32, 32)
	w = max(int(math.Round(float64(int(float64(img.w)*ratio))/32))*32, 32)
	r := resizeLinear(img, w, h)
	data = make([]float32, 3*h*w)
	n := h * w
	var scale, shift [3]float32
	for c := 0; c < 3; c++ {
		scale[c] = float32(1 / 255.0 / detStd[c])
		shift[c] = float32(-detMean[c] / detStd[c])
	}
	for i := 0; i < n; i++ {
		p := r.pix[i*3:]
		data[i] = float32(p[0])*scale[0] + shift[0]
		data[n+i] = float32(p[1])*scale[1] + shift[1]
		data[2*n+i] = float32(p[2])*scale[2] + shift[2]
	}
	return data, h, w
}

type pt struct{ x, y float64 }

// quad : 글줄 상자 (왼위 → 오른위 → 오른아래 → 왼아래)
type quad [4]pt

func dist(a, b pt) float64 { return math.Hypot(a.x-b.x, a.y-b.y) }

// convexHull : 볼록 껍질 (Andrew monotone chain)
func convexHull(ps []pt) []pt {
	if len(ps) < 3 {
		return ps
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].x != ps[j].x {
			return ps[i].x < ps[j].x
		}
		return ps[i].y < ps[j].y
	})
	cross := func(o, a, b pt) float64 { return (a.x-o.x)*(b.y-o.y) - (a.y-o.y)*(b.x-o.x) }
	h := make([]pt, 0, 2*len(ps))
	for _, p := range ps {
		for len(h) >= 2 && cross(h[len(h)-2], h[len(h)-1], p) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, p)
	}
	lo := len(h) + 1
	for i := len(ps) - 2; i >= 0; i-- {
		p := ps[i]
		for len(h) >= lo && cross(h[len(h)-2], h[len(h)-1], p) <= 0 {
			h = h[:len(h)-1]
		}
		h = append(h, p)
	}
	return h[:len(h)-1]
}

// minAreaRect : 점들을 감싸는 가장 작은 (기울어진) 직사각형 — cv2.minAreaRect. 네 꼭짓점과 두 변의 길이를 돌려준다.
func minAreaRect(ps []pt) (corners [4]pt, w, h float64) {
	hull := convexHull(ps)
	switch len(hull) {
	case 0:
		return
	case 1:
		return [4]pt{hull[0], hull[0], hull[0], hull[0]}, 0, 0
	}
	best := math.Inf(1)
	for i := range hull {
		a, b := hull[i], hull[(i+1)%len(hull)]
		l := dist(a, b)
		if l == 0 {
			continue
		}
		ux, uy := (b.x-a.x)/l, (b.y-a.y)/l
		minU, maxU, minV, maxV := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
		for _, p := range hull {
			u := (p.x-a.x)*ux + (p.y-a.y)*uy
			v := -(p.x-a.x)*uy + (p.y-a.y)*ux
			minU, maxU = math.Min(minU, u), math.Max(maxU, u)
			minV, maxV = math.Min(minV, v), math.Max(maxV, v)
		}
		if area := (maxU - minU) * (maxV - minV); area < best {
			best = area
			w, h = maxU-minU, maxV-minV
			at := func(u, v float64) pt { return pt{a.x + u*ux - v*uy, a.y + u*uy + v*ux} }
			corners = [4]pt{at(minU, minV), at(maxU, minV), at(maxU, maxV), at(minU, maxV)}
		}
	}
	return
}

// orderQuad : 네 꼭짓점을 왼위·오른위·오른아래·왼아래 순으로 (PaddleOCR get_mini_boxes)
func orderQuad(c [4]pt) quad {
	p := c[:]
	sort.SliceStable(p, func(i, j int) bool { return p[i].x < p[j].x })
	var q quad
	if p[1].y > p[0].y {
		q[0], q[3] = p[0], p[1]
	} else {
		q[0], q[3] = p[1], p[0]
	}
	if p[3].y > p[2].y {
		q[1], q[2] = p[2], p[3]
	} else {
		q[1], q[2] = p[3], p[2]
	}
	return q
}

// inQuad : 점이 볼록 사각형 안(경계 포함)에 있는가
func inQuad(q quad, x, y float64) bool {
	pos, neg := false, false
	for i := 0; i < 4; i++ {
		a, b := q[i], q[(i+1)%4]
		c := (b.x-a.x)*(y-a.y) - (b.y-a.y)*(x-a.x)
		if c > 1e-9 {
			pos = true
		} else if c < -1e-9 {
			neg = true
		}
	}
	return !(pos && neg)
}

// boxScore : 상자 안 확률의 평균 (PaddleOCR box_score_fast)
func boxScore(prob []float32, w, h int, q quad) float64 {
	var iq quad
	minX, maxX, minY, maxY := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for i, p := range q {
		minX, maxX = math.Min(minX, p.x), math.Max(maxX, p.x)
		minY, maxY = math.Min(minY, p.y), math.Max(maxY, p.y)
		iq[i] = pt{math.Trunc(p.x), math.Trunc(p.y)}
	}
	x0, x1 := clampI(int(math.Floor(minX)), 0, w-1), clampI(int(math.Ceil(maxX)), 0, w-1)
	y0, y1 := clampI(int(math.Floor(minY)), 0, h-1), clampI(int(math.Ceil(maxY)), 0, h-1)
	var sum float64
	n := 0
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if inQuad(iq, float64(x), float64(y)) {
				sum += float64(prob[y*w+x])
				n++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

// unclip : 상자를 바깥으로 넓힌다 (넓이 × 비율 ÷ 둘레 만큼). 검출 모델은 글자보다 작게 줄인 영역을 내놓기 때문이다.
// PaddleOCR 은 모서리를 둥글게 넓힌 뒤 다시 최소 직사각형을 구하는데, 직사각형에서는 네 변을 같은 거리만큼 민 것과 같다.
func unclip(q quad) (quad, float64) {
	w, h := dist(q[0], q[1]), dist(q[0], q[3])
	if w == 0 || h == 0 {
		return q, 0
	}
	d := w * h * detUnclipRatio / (2 * (w + h))
	ux, uy := (q[1].x-q[0].x)/w, (q[1].y-q[0].y)/w
	vx, vy := (q[3].x-q[0].x)/h, (q[3].y-q[0].y)/h
	mv := func(p pt, su, sv float64) pt {
		return pt{p.x + d*(su*ux+sv*vx), p.y + d*(su*uy+sv*vy)}
	}
	out := quad{mv(q[0], -1, -1), mv(q[1], 1, -1), mv(q[2], 1, 1), mv(q[3], -1, 1)}
	return out, math.Min(w, h) + 2*d
}

// detBoxes : 확률 지도(mapH×mapW)에서 글줄 상자를 뽑아 원본 크기(srcW×srcH) 좌표로 돌려준다 (DBPostProcess).
func detBoxes(prob []float32, mapW, mapH, srcW, srcH int) []quad {
	label := make([]int32, mapW*mapH)
	var boxes []quad
	var stack []int32
	var edge []pt
	for start := 0; start < len(prob); start++ {
		if prob[start] <= detThresh || label[start] != 0 {
			continue
		}
		if len(boxes) >= detMaxBoxes {
			break
		}
		// 이어진 영역 하나 (8방향) — 가장자리 화소만 모은다
		edge = edge[:0]
		stack = append(stack[:0], int32(start))
		label[start] = 1
		for len(stack) > 0 {
			i := int(stack[len(stack)-1])
			stack = stack[:len(stack)-1]
			x, y := i%mapW, i/mapW
			border := false
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					nx, ny := x+dx, y+dy
					if nx < 0 || ny < 0 || nx >= mapW || ny >= mapH {
						border = true
						continue
					}
					j := ny*mapW + nx
					if prob[j] <= detThresh {
						if dx == 0 || dy == 0 {
							border = true
						}
						continue
					}
					if label[j] == 0 {
						label[j] = 1
						stack = append(stack, int32(j))
					}
				}
			}
			if border {
				edge = append(edge, pt{float64(x), float64(y)})
			}
		}
		corners, w, h := minAreaRect(edge)
		if math.Min(w, h) < detMinSize {
			continue
		}
		q := orderQuad(corners)
		if boxScore(prob, mapW, mapH, q) < detBoxThresh {
			continue
		}
		q, side := unclip(q)
		if side < detMinSize+2 {
			continue
		}
		// 원본 크기로, 그림 안으로
		for i := range q {
			q[i].x = math.Min(math.Max(math.Round(q[i].x/float64(mapW)*float64(srcW)), 0), float64(srcW-1))
			q[i].y = math.Min(math.Max(math.Round(q[i].y/float64(mapH)*float64(srcH)), 0), float64(srcH-1))
		}
		q = orderClockwise(q)
		if int(dist(q[0], q[1])) <= 3 || int(dist(q[0], q[3])) <= 3 {
			continue
		}
		boxes = append(boxes, q)
	}
	sortBoxes(boxes)
	return boxes
}

// orderClockwise : PaddleOCR order_points_clockwise — x 로 나눈 왼쪽 둘·오른쪽 둘을 각각 y 로
func orderClockwise(q quad) quad {
	p := q[:]
	sort.SliceStable(p, func(i, j int) bool { return p[i].x < p[j].x })
	l, r := []pt{p[0], p[1]}, []pt{p[2], p[3]}
	if l[0].y > l[1].y {
		l[0], l[1] = l[1], l[0]
	}
	if r[0].y > r[1].y {
		r[0], r[1] = r[1], r[0]
	}
	return quad{l[0], r[0], r[1], l[1]}
}

// sortBoxes : 위에서 아래로, 같은 줄(세로 차이 10px 미만)은 왼쪽에서 오른쪽으로 (PaddleOCR sorted_boxes)
func sortBoxes(b []quad) {
	sort.SliceStable(b, func(i, j int) bool {
		if b[i][0].y != b[j][0].y {
			return b[i][0].y < b[j][0].y
		}
		return b[i][0].x < b[j][0].x
	})
	for i := 0; i < len(b)-1; i++ {
		for j := i; j >= 0; j-- {
			if math.Abs(b[j+1][0].y-b[j][0].y) < 10 && b[j+1][0].x < b[j][0].x {
				b[j], b[j+1] = b[j+1], b[j]
			} else {
				break
			}
		}
	}
}
