package service

// 사입정보 사진 → Windows 기본 OCR(무료·오프라인) → 사입일자·사입도매·수량·유효기간·제조번호.
//
// 1. 사진 손질: 화면 캡처처럼 작은 이미지는 키우고(작은 글꼴은 OCR 이 못 읽는다), 휴대폰으로 모니터를 찍은 사진은
//    모아레(화면 격자 무늬)를 흐리게 지운 두 가지 버전을 만든다. 모두 흑백·대비 보정.
// 2. Windows OCR(한국어)로 단어와 위치를 읽는다.
// 3. 표 머리(사입일자·거래처·수량·유효기간·제조번호…)를 찾아 열 위치를 정하고, 단어를 줄·열로 나눈다.
//    한국어 OCR 은 숫자를 비슷한 한글로 읽는 일이 잦아(0→口, 8→日, 9→님) 숫자 칸에서는 되돌린다.
// 4. 이미 가져온 사입현황(DB)에 같은 날짜의 사입이 있으면 날짜·도매·수량은 DB 값을 쓴다 (정확).
// 5. 결과는 저장하지 않고 화면에 보여 준다. 사용자가 사진과 비교해 고친 뒤 저장한다.

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"dpr/internal/domain"
	"dpr/internal/platform"
)

// ocrRunner : 테스트에서 바꿔 끼운다
var ocrRunner = platform.OCR

type LotPhotoRequest struct {
	ImageBase64 string `json:"imageBase64"`
	Code        string `json:"code"` // 어느 약품의 사입 이력인지 (사용자가 고른 약품)
}

// LotPhotoRow : 사진에서 읽은 한 줄 (사용자가 고친 뒤 저장)
type LotPhotoRow struct {
	Code         string   `json:"code"`
	Date         string   `json:"date"`         // 사입일자 YYYY-MM-DD
	SupplierRaw  string   `json:"supplierRaw"`  // 사진에서 읽은 도매 이름
	SupplierID   int64    `json:"supplierId"`   // 등록된 거래처와 맞춘 결과 (0 = 못 찾음)
	SupplierName string   `json:"supplierName"` // 맞춘 거래처 이름
	Qty          float64  `json:"qty"`          // 총수량 (없으면 수량)
	Expiry       string   `json:"expiry"`       // 유효기간 YYYY-MM-DD
	Lot          string   `json:"lot"`          // 제조번호
	Matched      string   `json:"matched"`      // 사입현황과 맞춘 방법: "date"(같은 날짜) | "order"(순서로 추정) | ""
	Unsure       []string `json:"unsure"`       // 확신이 낮은 칸: date | supplier | qty | expiry | lot
	DateRaw      string   `json:"-"`            // 사입일자 칸에서 읽은 숫자 (사입현황과 맞출 때 일부라도 쓴다)
}

type LotPhotoResult struct {
	Rows    []LotPhotoRow `json:"rows"`
	Mode    string        `json:"mode"`    // screenshot | photo
	Note    string        `json:"note"`    // 안내 (머리를 못 찾음 등)
	RawText string        `json:"rawText"` // 인식된 글자 (확인용)
	Skipped int           `json:"skipped"` // 반품·재고조정 등 사입이 아니라 뺀 줄
}

// ReadLotPhoto 는 사입정보 사진 1장을 Windows OCR 로 판독한다 (저장하지 않음).
func (s *Service) ReadLotPhoto(ctx context.Context, req LotPhotoRequest) (*LotPhotoResult, error) {
	code := domain.NormalizeProductCode(req.Code)
	if code == "" {
		return nil, errors.New("어느 약품의 사입 이력인지 먼저 고르세요")
	}
	data := strings.TrimSpace(req.ImageBase64)
	if i := strings.Index(data, ","); strings.HasPrefix(data, "data:") && i > 0 {
		data = data[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(raw) == 0 {
		return nil, errors.New("사진 데이터를 읽지 못했습니다")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("사진 형식을 읽지 못했습니다 (JPG·PNG 로 올려 주세요. 아이폰 HEIC 는 JPG 로 바꿔 주세요)")
	}

	variants, mode := ocrVariants(src)
	dir, err := os.MkdirTemp("", "dpr-lotocr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	paths := make([]string, len(variants))
	for i, v := range variants {
		paths[i] = filepath.Join(dir, fmt.Sprintf("v%d.png", i))
		var buf bytes.Buffer
		if err := png.Encode(&buf, v.img); err != nil {
			return nil, err
		}
		if err := os.WriteFile(paths[i], buf.Bytes(), 0o600); err != nil {
			return nil, err
		}
	}
	start := time.Now()
	results, err := ocrRunner(ctx, paths)
	if err != nil {
		return nil, err
	}
	s.Log.Info("사입정보 사진 OCR", "mode", mode, "variants", len(variants), "ms", time.Since(start).Milliseconds())

	sup := s.supplierMatcher()
	var parsed []parsedTable
	for i, r := range results {
		if i >= len(variants) {
			break
		}
		parsed = append(parsed, parseLotTable(r, variants[i].scale, sup))
	}
	res := mergeTables(parsed)
	res.Mode = mode
	for i := range res.Rows {
		res.Rows[i].Code = code
	}
	unifyLots(res.Rows)
	s.alignWithPurchases(code, res.Rows)
	if len(res.Rows) == 0 && res.Note == "" {
		res.Note = "사입 줄을 찾지 못했습니다. 사입일자·거래처·유효기간·제조번호 열이 보이게 다시 올려 주세요."
	}
	return res, nil
}

// ── 1. 사진 손질 ─────────────────────────────────────────────────────

type ocrVariant struct {
	img   *image.Gray
	scale float64 // 손질한 이미지 좌표 ÷ scale = 원본 좌표
}

const ocrMaxDim = 9000 // Windows OCR 최대 10000px

// ocrVariants : 화면 캡처(작은 이미지)는 키운 한 장, 휴대폰 사진(큰 이미지)은 모아레를 지운 두 장
func ocrVariants(src image.Image) ([]ocrVariant, string) {
	g := toGray(src)
	w, h := g.Rect.Dx(), g.Rect.Dy()
	maxd := w
	if h > maxd {
		maxd = h
	}
	if maxd <= 2200 {
		k := 3.0
		if maxd > 1500 {
			k = 2
		}
		if float64(maxd)*k > ocrMaxDim {
			k = ocrMaxDim / float64(maxd)
		}
		return []ocrVariant{{img: stretchContrast(resizeGray(g, k), 0.01, 0.99), scale: k}}, "screenshot"
	}
	// 휴대폰 사진: (1) 흐리게 + 반으로 줄임  (2) 원래 크기에서 약하게 흐리게
	k := 0.5
	if float64(maxd)*k < 1800 {
		k = 1800 / float64(maxd)
	}
	a := stretchContrast(resizeGray(boxBlur(g, 3), k), 0.02, 0.98)
	b := g
	sb := 1.0
	if maxd > ocrMaxDim {
		sb = ocrMaxDim / float64(maxd)
		b = resizeGray(g, sb)
	}
	b = stretchContrast(boxBlur(b, 2), 0.02, 0.98)
	return []ocrVariant{{img: a, scale: k}, {img: b, scale: sb}}, "photo"
}

func toGray(src image.Image) *image.Gray {
	b := src.Bounds()
	g := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	if gs, ok := src.(*image.Gray); ok {
		for y := 0; y < b.Dy(); y++ {
			copy(g.Pix[y*g.Stride:y*g.Stride+b.Dx()], gs.Pix[(y)*gs.Stride:(y)*gs.Stride+b.Dx()])
		}
		return g
	}
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, gg, bb, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			g.Pix[y*g.Stride+x] = color.GrayModel.Convert(color.RGBA64{uint16(r), uint16(gg), uint16(bb), 0xffff}).(color.Gray).Y
		}
	}
	return g
}

// resizeGray : 줄일 때는 영역 평균, 키울 때는 양선형 보간
func resizeGray(g *image.Gray, k float64) *image.Gray {
	sw, sh := g.Rect.Dx(), g.Rect.Dy()
	W, H := int(math.Round(float64(sw)*k)), int(math.Round(float64(sh)*k))
	if W < 1 {
		W = 1
	}
	if H < 1 {
		H = 1
	}
	o := image.NewGray(image.Rect(0, 0, W, H))
	if k < 1 {
		for y := 0; y < H; y++ {
			y0, y1 := int(float64(y)/k), int(float64(y+1)/k)
			if y1 <= y0 {
				y1 = y0 + 1
			}
			if y1 > sh {
				y1 = sh
			}
			for x := 0; x < W; x++ {
				x0, x1 := int(float64(x)/k), int(float64(x+1)/k)
				if x1 <= x0 {
					x1 = x0 + 1
				}
				if x1 > sw {
					x1 = sw
				}
				s, n := 0, 0
				for yy := y0; yy < y1; yy++ {
					row := g.Pix[yy*g.Stride:]
					for xx := x0; xx < x1; xx++ {
						s += int(row[xx])
						n++
					}
				}
				if n > 0 {
					o.Pix[y*o.Stride+x] = uint8(s / n)
				}
			}
		}
		return o
	}
	for y := 0; y < H; y++ {
		fy := (float64(y)+0.5)/k - 0.5
		y0 := int(math.Floor(fy))
		ty := fy - float64(y0)
		y0c, y1c := clampInt(y0, 0, sh-1), clampInt(y0+1, 0, sh-1)
		for x := 0; x < W; x++ {
			fx := (float64(x)+0.5)/k - 0.5
			x0 := int(math.Floor(fx))
			tx := fx - float64(x0)
			x0c, x1c := clampInt(x0, 0, sw-1), clampInt(x0+1, 0, sw-1)
			p00 := float64(g.Pix[y0c*g.Stride+x0c])
			p10 := float64(g.Pix[y0c*g.Stride+x1c])
			p01 := float64(g.Pix[y1c*g.Stride+x0c])
			p11 := float64(g.Pix[y1c*g.Stride+x1c])
			v := (p00*(1-tx)+p10*tx)*(1-ty) + (p01*(1-tx)+p11*tx)*ty
			o.Pix[y*o.Stride+x] = uint8(math.Round(v))
		}
	}
	return o
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// boxBlur : (2r+1)² 평균 (적분 영상, 크기와 무관하게 빠름)
func boxBlur(g *image.Gray, r int) *image.Gray {
	W, H := g.Rect.Dx(), g.Rect.Dy()
	ii := make([]int64, (W+1)*(H+1))
	for y := 0; y < H; y++ {
		var row int64
		for x := 0; x < W; x++ {
			row += int64(g.Pix[y*g.Stride+x])
			ii[(y+1)*(W+1)+x+1] = ii[y*(W+1)+x+1] + row
		}
	}
	o := image.NewGray(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		y0, y1 := clampInt(y-r, 0, H), clampInt(y+r+1, 0, H)
		for x := 0; x < W; x++ {
			x0, x1 := clampInt(x-r, 0, W), clampInt(x+r+1, 0, W)
			s := ii[y1*(W+1)+x1] - ii[y0*(W+1)+x1] - ii[y1*(W+1)+x0] + ii[y0*(W+1)+x0]
			o.Pix[y*o.Stride+x] = uint8(s / int64((x1-x0)*(y1-y0)))
		}
	}
	return o
}

// stretchContrast : 밝기 분포의 lo~hi 분위를 0~255 로 펼친다
func stretchContrast(g *image.Gray, lo, hi float64) *image.Gray {
	var hist [256]int
	for _, p := range g.Pix {
		hist[p]++
	}
	n := len(g.Pix)
	find := func(q float64) int {
		target, acc := int(q*float64(n)), 0
		for v := 0; v < 256; v++ {
			acc += hist[v]
			if acc > target {
				return v
			}
		}
		return 255
	}
	a, z := find(lo), find(hi)
	if z-a < 16 {
		return g
	}
	var lut [256]uint8
	for v := 0; v < 256; v++ {
		t := float64(v-a) / float64(z-a) * 255
		lut[v] = uint8(math.Max(0, math.Min(255, t)))
	}
	o := image.NewGray(g.Rect)
	for i, p := range g.Pix {
		o.Pix[i] = lut[p]
	}
	return o
}

// ── 2~3. 표 읽기 ─────────────────────────────────────────────────────

type ocrBox struct {
	t              string
	x0, y0, x1, y1 float64 // 원본 이미지 좌표
	line           int
}

func (b ocrBox) cx() float64 { return (b.x0 + b.x1) / 2 }
func (b ocrBox) cy() float64 { return (b.y0 + b.y1) / 2 }
func (b ocrBox) h() float64  { return b.y1 - b.y0 }

type parsedRow struct {
	LotPhotoRow
	y     float64
	score int
}

type parsedTable struct {
	rows    []parsedRow
	header  bool
	raw     string
	skipped int
	rowH    float64
}

// 머리 이름 (열 위치를 정한다). skip 은 읽지 않는 열이지만 숫자가 옆 열로 섞이지 않게 위치는 잡아 둔다.
var lotHeaderNames = []struct {
	key   string
	names []string
}{
	{"date", []string{"사입일자", "입고일자", "매입일자", "거래일자", "일자", "차입일자", "사입말자", "차입말자"}},
	{"kind", []string{"구분", "구문"}}, // 구문: OCR 이 자주 이렇게 읽는다
	{"supplier", []string{"거래처", "도매처", "공급처", "매입처"}},
	{"qty", []string{"수량", "수당"}},
	{"total", []string{"총수량", "총수당", "층수당", "송수당"}},
	{"expiry", []string{"유효기간", "유효일자", "사용기한", "유통기한"}},
	{"lot", []string{"제조번호", "로트번호", "LOT"}},
	{"skip", []string{"단가", "합계액", "합계", "금액", "부가세", "비고", "약품명", "규격", "단위"}},
}

type ocrCol struct {
	key string
	x   float64
}

func parseLotTable(r platform.OCRResult, scale float64, sup func(string) (int64, string)) parsedTable {
	if scale <= 0 {
		scale = 1
	}
	var boxes []ocrBox
	var raw strings.Builder
	// 기울기 보정: OCR 이 알려 준 글줄 각도만큼 되돌린다
	ang := -r.Angle * math.Pi / 180
	cw, ch := float64(r.Width)/2, float64(r.Height)/2
	for li, l := range r.Lines {
		raw.WriteString(l.Text)
		raw.WriteString("\n")
		for _, w := range l.Words {
			x0, y0 := w.X, w.Y
			if ang != 0 {
				cx, cy := w.X+w.W/2-cw, w.Y+w.H/2-ch
				nx := cx*math.Cos(ang) - cy*math.Sin(ang) + cw
				ny := cx*math.Sin(ang) + cy*math.Cos(ang) + ch
				x0, y0 = nx-w.W/2, ny-w.H/2
			}
			boxes = append(boxes, ocrBox{t: w.Text, x0: x0 / scale, y0: y0 / scale, x1: (x0 + w.W) / scale, y1: (y0 + w.H) / scale, line: li})
		}
	}
	out := parsedTable{raw: raw.String()}
	if len(boxes) == 0 {
		return out
	}
	hs := make([]float64, len(boxes))
	for i, b := range boxes {
		hs[i] = b.h()
	}
	sort.Float64s(hs)
	medH := hs[len(hs)/2]
	if medH <= 0 {
		medH = 10
	}
	out.rowH = medH * 1.6

	cols, headerY := findLotHeader(boxes, medH)
	out.header = len(cols) > 0
	var body []ocrBox
	for _, b := range boxes {
		if !out.header || b.cy() > headerY+medH*0.6 {
			body = append(body, b)
		}
	}
	lastY := headerY
	for _, line := range groupRows(body, medH) {
		// 표가 끝난 뒤(빈 공간이 크게 벌어진 뒤)의 글자는 다른 표·화면 요소다
		if out.header && len(line) >= 3 {
			if line[0].cy()-lastY > medH*5 {
				break
			}
			lastY = line[0].cy()
		}
		row, skip := buildLotRow(line, cols, medH, sup)
		if skip {
			out.skipped++
			continue
		}
		if row == nil {
			continue
		}
		// 확실한 칸이 하나도 없고 제조번호도 짧으면 표의 줄이 아니라 잡음으로 본다
		if row.score == 0 && len(row.Lot) < 5 {
			continue
		}
		out.rows = append(out.rows, *row)
	}
	return out
}

// findLotHeader : 머리 이름과 비슷한 단어(또는 붙어 있는 두 단어)가 가장 많이 모인 가로줄을 머리로 본다
func findLotHeader(boxes []ocrBox, medH float64) ([]ocrCol, float64) {
	type cand struct {
		key   string
		x, y  float64
		score float64
	}
	var cands []cand
	try := func(text string, x, y float64) {
		t := ocrNameKey(text)
		if t == "" {
			return
		}
		bestKey, best := "", 0.0
		for _, h := range lotHeaderNames {
			for _, n := range h.names {
				sc := textSimilarity(t, ocrNameKey(n))
				need := 0.6
				if len([]rune(n)) <= 2 {
					need = 0.99 // '구분'·'수량' 같은 두 글자는 정확히 같을 때만
				}
				if sc >= need && sc > best {
					best, bestKey = sc, h.key
				}
			}
		}
		if bestKey != "" {
			cands = append(cands, cand{bestKey, x, y, best})
		}
	}
	for i, b := range boxes {
		try(b.t, b.cx(), b.cy())
		if i+1 < len(boxes) {
			n := boxes[i+1]
			if n.line == b.line && n.x0-b.x1 < medH*1.2 {
				try(b.t+n.t, (b.x0+n.x1)/2, (b.cy()+n.cy())/2)
			}
		}
	}
	var bestCols []ocrCol
	bestY, bestN := 0.0, 0
	for _, c := range cands {
		seen := map[string]cand{}
		for _, d := range cands {
			if math.Abs(d.y-c.y) < medH*0.8 {
				if o, ok := seen[d.key]; !ok || d.score > o.score || (d.key == "skip" && math.Abs(o.x-d.x) > medH) {
					if d.key == "skip" {
						seen[fmt.Sprintf("skip%.0f", d.x)] = d
					} else {
						seen[d.key] = d
					}
				}
			}
		}
		n := 0
		important := false
		for k := range seen {
			if !strings.HasPrefix(k, "skip") {
				n++
			}
			if k == "lot" || k == "expiry" || k == "date" {
				important = true
			}
		}
		if n >= 2 && important && n > bestN {
			bestN, bestY = n, c.y
			bestCols = bestCols[:0]
			for _, d := range seen {
				bestCols = append(bestCols, ocrCol{d.key, d.x})
			}
		}
	}
	sort.Slice(bestCols, func(i, j int) bool { return bestCols[i].x < bestCols[j].x })
	return bestCols, bestY
}

// groupRows : 세로 위치가 비슷한 단어끼리 한 줄로 묶는다
func groupRows(boxes []ocrBox, medH float64) [][]ocrBox {
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].cy() < boxes[j].cy() })
	var rows [][]ocrBox
	var cur []ocrBox
	var cy float64
	for _, b := range boxes {
		if len(cur) > 0 && math.Abs(b.cy()-cy) > medH*0.55 {
			rows = append(rows, cur)
			cur = nil
		}
		cur = append(cur, b)
		s := 0.0
		for _, c := range cur {
			s += c.cy()
		}
		cy = s / float64(len(cur))
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}
	for _, r := range rows {
		sort.Slice(r, func(i, j int) bool { return r[i].x0 < r[j].x0 })
	}
	return rows
}

var notPurchase = []string{"반품", "조정", "재고정리", "고정리", "폐기", "차용", "출고"}

var qtyLike = regexp.MustCompile(`^-?\d{1,5}\.\d{2}$`)

// buildLotRow : 한 줄의 단어를 열에 나눠 값을 만든다. skip=true 는 반품·재고조정처럼 사입이 아닌 줄.
func buildLotRow(line []ocrBox, cols []ocrCol, medH float64, sup func(string) (int64, string)) (*parsedRow, bool) {
	cells := map[string][]string{}
	var all []string
	var minX, maxX float64 = math.Inf(-1), math.Inf(1)
	if len(cols) >= 2 {
		// 표 바깥(왼쪽 메뉴·오른쪽 여백)의 글자는 뺀다
		minX = cols[0].x - (cols[1].x-cols[0].x)*0.8
		maxX = cols[len(cols)-1].x + (cols[len(cols)-1].x-cols[len(cols)-2].x)*0.8
	}
	for _, b := range line {
		if b.cx() < minX || b.cx() > maxX {
			continue
		}
		all = append(all, b.t)
		if len(cols) == 0 {
			continue
		}
		// 가장 가까운 열 (열 사이 경계 = 두 머리의 가운데).
		// '구분' 열은 좁고 다음의 거래처는 왼쪽 정렬이라, 구분 머리 바로 오른쪽부터는 거래처로 본다.
		k := cols[len(cols)-1].key
		for i := 0; i < len(cols)-1; i++ {
			edge := (cols[i].x + cols[i+1].x) / 2
			if cols[i].key == "kind" {
				edge = math.Min(edge, cols[i].x+medH*1.3)
			}
			if b.cx() < edge {
				k = cols[i].key
				break
			}
		}
		cells[k] = append(cells[k], b.t)
	}
	if len(all) == 0 {
		return nil, false
	}
	text := strings.Join(all, " ")
	joined := func(k string) string { return strings.Join(cells[k], "") }

	kindText := joined("kind") + " " + joined("supplier")
	if len(cols) == 0 {
		kindText = text
	}
	for _, w := range notPurchase {
		if strings.Contains(strings.ReplaceAll(kindText, " ", ""), w) {
			return nil, true
		}
	}

	row := &parsedRow{y: line[0].cy()}
	unsure := map[string]bool{}
	if len(cols) > 0 {
		row.Date, unsure["date"] = parseOCRDate(joined("date"), true)
		row.Expiry, unsure["expiry"] = parseOCRDate(joined("expiry"), false)
		row.Lot, unsure["lot"] = cleanLot(joined("lot"))
		row.SupplierRaw = strings.TrimSpace(strings.Join(cells["supplier"], " "))
		row.DateRaw = fixDigits(joined("date"))
		q, okq := parseOCRNumber(joined("total"))
		if !okq {
			q, okq = parseOCRNumber(joined("qty"))
		}
		if !okq && len(cells["total"]) == 0 && len(cells["qty"]) == 0 {
			// 수량 머리를 못 읽음: 이팜은 수량을 소수 둘째 자리(4.00, 120.00)로, 금액은 쉼표로 쓴다 → 소수가 둘이면 뒤가 총수량
			var decs []float64
			for _, t := range cells["skip"] {
				if qtyLike.MatchString(fixDigits(t)) {
					if v, ok := parseOCRNumber(t); ok {
						decs = append(decs, v)
					}
				}
			}
			if len(decs) > 0 {
				q, okq = decs[min(1, len(decs)-1)], true
			}
		}
		if okq {
			row.Qty = q
		}
		unsure["qty"] = !okq
	} else {
		// 머리를 못 찾음: 줄 전체에서 모양으로 찾는다 (날짜 2개 → 앞 = 사입일자, 뒤 = 유효기간)
		dates := findDates(text)
		if len(dates) > 0 {
			row.Date = dates[0]
		}
		if len(dates) > 1 {
			row.Expiry = dates[len(dates)-1]
		}
		unsure["date"], unsure["expiry"], unsure["qty"] = true, true, true
		row.Lot, _ = cleanLot(lastLotToken(line))
		unsure["lot"] = true
	}
	if row.Qty < 0 {
		return nil, true // 취소(음수) 줄
	}
	if row.Lot == "" && row.Expiry == "" {
		return nil, false
	}
	if row.SupplierRaw != "" {
		row.SupplierID, row.SupplierName = sup(row.SupplierRaw)
	}
	unsure["supplier"] = row.SupplierID == 0
	for _, k := range []string{"date", "supplier", "qty", "expiry", "lot"} {
		if unsure[k] {
			row.Unsure = append(row.Unsure, k)
		} else {
			row.score++
		}
	}
	return row, false
}

// 한국어 OCR 이 숫자를 비슷하게 생긴 글자로 읽는 경우 (화면 캡처·사진 실측)
var digitLookalike = map[rune]string{
	'口': "0", 'ㅁ': "0", 'O': "0", 'o': "0", 'D': "0", 'Q': "0", '○': "0", '〇': "0",
	'I': "1", 'l': "1", '|': "1", 'ㅣ': "1", '!': "1", 'i': "1", '丨': "1",
	'Z': "2", 'z': "2",
	'S': "5", 's': "5",
	'B': "8", '日': "8", '目': "8",
	'님': "9", '테': "9", 'g': "9", 'q': "9",
	'기': "7",
	'이': "01",
}

// fixDigits : 숫자 칸의 글자를 숫자로 되돌린다 (- . , 는 남김)
func fixDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r == '-', r == '.', r == ',':
			b.WriteRune(r)
		case unicode.IsSpace(r):
		default:
			if d, ok := digitLookalike[r]; ok {
				b.WriteString(d)
			}
		}
	}
	return b.String()
}

// parseOCRDate : YYYY-MM-DD 또는 YYYYMMDD. unsure=true 면 값을 믿기 어렵다 (빈 값 포함).
func parseOCRDate(s string, past bool) (string, bool) {
	t := strings.TrimSpace(s)
	if t == "" || t == "0" {
		return "", true
	}
	f := fixDigits(t)
	digits := strings.NewReplacer("-", "", ".", "", ",", "").Replace(f)
	changed := f != strings.ReplaceAll(t, " ", "")
	if len(digits) == 6 {
		// 앞의 '20' 을 한글 한 글자(꼬·끄·죠 등)로 읽어 빠진 경우: 19-10-30 → 2019-10-30
		digits, changed = "20"+digits, true
	}
	if len(digits) < 8 {
		return "", true
	}
	digits = digits[:8]
	d, err := time.Parse("20060102", digits)
	if err != nil || d.Year() < 2000 || d.Year() > 2099 {
		return "", true
	}
	if past && d.After(time.Now().AddDate(0, 0, 1)) {
		return domain.FormatDate(d), true
	}
	return domain.FormatDate(d), changed
}

var dateLike = regexp.MustCompile(`(20\d{2})[-./]?(\d{2})[-./]?(\d{2})`)

func findDates(s string) []string {
	var out []string
	for _, m := range dateLike.FindAllStringSubmatch(fixDigits(strings.ReplaceAll(s, " ", "")), -1) {
		if d, err := time.Parse("20060102", m[1]+m[2]+m[3]); err == nil {
			out = append(out, domain.FormatDate(d))
		}
	}
	return out
}

// parseOCRNumber : "120.00" "1,559,040" "-18.00" → 수
func parseOCRNumber(s string) (float64, bool) {
	f := strings.ReplaceAll(fixDigits(s), ",", "")
	if f == "" || f == "-" {
		return 0, false
	}
	v, err := strconv.ParseFloat(f, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// cleanLot : 제조번호는 영문·숫자·하이픈. 한글로 잘못 읽힌 숫자는 되돌리고 나머지 이상한 글자는 뺀다.
func cleanLot(s string) (string, bool) {
	var b strings.Builder
	odd := strings.Contains(s, `\/`)
	s = strings.ReplaceAll(s, `\/`, "V") // V(또는 Y)를 \/ 로 읽는 경우
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z', r == '-':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(unicode.ToUpper(r))
		case unicode.IsSpace(r):
		default:
			if d, ok := map[rune]string{'口': "0", '日': "8", '님': "9", '이': "01", '테': "9", 'ㅁ': "0"}[r]; ok {
				b.WriteString(d)
			}
			odd = true
		}
	}
	lot := strings.Trim(b.String(), "-")
	if lot == "" || lot == "0" {
		return "", true
	}
	return lot, odd || len(lot) < 4
}

func lastLotToken(line []ocrBox) string {
	for i := len(line) - 1; i >= 0; i-- {
		t := line[i].t
		hasL, hasD := false, false
		for _, r := range t {
			if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
				hasL = true
			}
			if r >= '0' && r <= '9' {
				hasD = true
			}
		}
		if hasL && hasD {
			return t
		}
	}
	return ""
}

// ocrNameKey : 비교용 — 괄호·기호·공백을 빼고 (주) 표시를 없앤다
func ocrNameKey(s string) string {
	s = domain.NormalizeSupplierName(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		}
	}
	return b.String()
}

// textSimilarity : 최장 공통 부분수열 길이 ÷ 긴 쪽 길이 (0~1)
func textSimilarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	dp := make([][]int, len(ra)+1)
	for i := range dp {
		dp[i] = make([]int, len(rb)+1)
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			if ra[i-1] == rb[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	m := len(ra)
	if len(rb) > m {
		m = len(rb)
	}
	return float64(dp[len(ra)][len(rb)]) / float64(m)
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// ── 여러 버전 합치기 ─────────────────────────────────────────────────

// mergeTables : 가장 잘 읽힌 버전을 바탕으로, 빈 칸·흐린 칸을 다른 버전의 같은 줄(세로 위치)로 채운다
func mergeTables(ts []parsedTable) *LotPhotoResult {
	res := &LotPhotoResult{Rows: []LotPhotoRow{}}
	if len(ts) == 0 {
		return res
	}
	best, bestScore := 0, -1
	for i, t := range ts {
		sc := 0
		for _, r := range t.rows {
			sc += r.score + 1
		}
		if t.header {
			sc += 3
		}
		if sc > bestScore {
			best, bestScore = i, sc
		}
	}
	base := ts[best]
	res.RawText = base.raw
	res.Skipped = base.skipped
	if !base.header && len(base.rows) > 0 {
		res.Note = "표 머리(사입일자·거래처·유효기간·제조번호)를 찾지 못해 줄 모양으로만 읽었습니다. 모든 칸을 사진과 꼭 비교하세요."
	}
	for _, r := range base.rows {
		row := r.LotPhotoRow
		for j, t := range ts {
			if j == best {
				continue
			}
			o := nearestRow(t.rows, r.y, base.rowH*0.6)
			if o == nil {
				continue
			}
			fill(&row, o.LotPhotoRow)
		}
		res.Rows = append(res.Rows, row)
	}
	// 바탕 버전이 놓친 줄은 다른 버전에서 가져온다 (세로 위치 순서 유지)
	type yrow struct {
		y   float64
		row LotPhotoRow
	}
	all := make([]yrow, 0, len(res.Rows))
	for i, r := range base.rows {
		all = append(all, yrow{r.y, res.Rows[i]})
	}
	for j, t := range ts {
		if j == best {
			continue
		}
		for _, o := range t.rows {
			dup := false
			for _, a := range all {
				if math.Abs(a.y-o.y) <= base.rowH*0.6 {
					dup = true
					break
				}
			}
			if !dup {
				all = append(all, yrow{o.y, o.LotPhotoRow})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].y < all[j].y })
	res.Rows = res.Rows[:0]
	for _, a := range all {
		res.Rows = append(res.Rows, a.row)
	}
	return res
}

func nearestRow(rows []parsedRow, y, tol float64) *parsedRow {
	var best *parsedRow
	bd := tol
	for i := range rows {
		if d := math.Abs(rows[i].y - y); d <= bd {
			best, bd = &rows[i], d
		}
	}
	return best
}

func isUnsure(r LotPhotoRow, k string) bool {
	for _, u := range r.Unsure {
		if u == k {
			return true
		}
	}
	return false
}

func setSure(r *LotPhotoRow, k string) {
	out := r.Unsure[:0]
	for _, u := range r.Unsure {
		if u != k {
			out = append(out, u)
		}
	}
	r.Unsure = out
}

// fill : r 의 흐린 칸을 o 의 확실한 칸으로 바꾼다
func fill(r *LotPhotoRow, o LotPhotoRow) {
	take := func(k string, empty bool, apply func()) {
		if (empty || isUnsure(*r, k)) && !isUnsure(o, k) {
			apply()
			setSure(r, k)
		}
	}
	if len(o.DateRaw) > len(r.DateRaw) {
		r.DateRaw = o.DateRaw
	}
	take("date", r.Date == "", func() { r.Date = o.Date })
	take("expiry", r.Expiry == "", func() { r.Expiry = o.Expiry })
	take("lot", r.Lot == "", func() { r.Lot = o.Lot })
	take("qty", r.Qty == 0, func() { r.Qty = o.Qty })
	take("supplier", r.SupplierID == 0, func() { r.SupplierID, r.SupplierName, r.SupplierRaw = o.SupplierID, o.SupplierName, o.SupplierRaw })
}

// unifyLots : 흐리게 읽은 제조번호가 같은 사진의 확실한 제조번호와 한두 글자만 다르면 그쪽으로 맞춘다.
//   - 확실하게 읽은 제조번호는 바꾸지 않는다. 실제 제조번호는 연속 생산분이라 한 글자만 다른 것이 흔하다
//     (31007-1 과 31007-2). 서로 다른 로트를 하나로 합치면 바코드 → 도매 판정이 틀어진다.
//   - 두 줄 모두 유효기간을 읽었는데 달(月)이 다르면 다른 로트로 본다.
//   - 맞춘 칸은 계속 '확인 필요'(노란색)로 둔다 — 사용자가 사진과 비교해 확정한다.
//
// 같은 제조번호는 유효기간도 같으므로, 유효기간을 못 읽은 줄은 같은 제조번호 줄의 유효기간으로 채운다.
func unifyLots(rows []LotPhotoRow) {
	weight := func(r LotPhotoRow) int {
		if r.Lot == "" {
			return 0
		}
		if !isUnsure(r, "lot") {
			return 3
		}
		return 1
	}
	count := map[string]int{}
	sure := map[string]bool{}    // 확실하게 읽힌 적이 있는 제조번호
	expOf := map[string]string{} // 확실한 제조번호의 유효기간 (연·월)
	for _, r := range rows {
		count[r.Lot] += weight(r)
		if r.Lot != "" && !isUnsure(r, "lot") {
			sure[r.Lot] = true
			if len(r.Expiry) >= 7 && !isUnsure(r, "expiry") {
				expOf[r.Lot] = r.Expiry[:7]
			}
		}
	}
	near := func(a, b string) bool {
		la, lb := len([]rune(a)), len([]rune(b))
		d := editDistance(a, b)
		return d <= 2 && d*3 <= max(la, lb) // 짧은 제조번호는 1글자 차이까지만
	}
	for i := range rows {
		cur := rows[i].Lot
		if cur == "" || !isUnsure(rows[i], "lot") {
			continue // 확실하게 읽은 값은 그대로
		}
		best := ""
		for l := range sure {
			if l == cur || !near(l, cur) {
				continue
			}
			if e := expOf[l]; e != "" && len(rows[i].Expiry) >= 7 && !isUnsure(rows[i], "expiry") && rows[i].Expiry[:7] != e {
				continue // 유효기간이 다르면 다른 로트
			}
			if best == "" || count[l] > count[best] || (count[l] == count[best] && l < best) {
				best = l
			}
		}
		if best != "" {
			rows[i].Lot = best // '확인 필요' 표시는 그대로 둔다
		}
	}
	// 같은 제조번호 → 같은 유효기간
	exp := map[string]map[string]int{}
	for _, r := range rows {
		if r.Lot == "" || r.Expiry == "" {
			continue
		}
		if exp[r.Lot] == nil {
			exp[r.Lot] = map[string]int{}
		}
		w := 1
		if !isUnsure(r, "expiry") {
			w = 3
		}
		exp[r.Lot][r.Expiry] += w
	}
	for i := range rows {
		r := &rows[i]
		m := exp[r.Lot]
		if r.Lot == "" || len(m) == 0 {
			continue
		}
		best, bc := "", 0
		for e, c := range m {
			if c > bc || (c == bc && e > best) {
				best, bc = e, c
			}
		}
		if r.Expiry == "" || (isUnsure(*r, "expiry") && best != r.Expiry && bc >= 3) {
			r.Expiry = best
			if !isUnsure(*r, "expiry") {
				r.Unsure = append(r.Unsure, "expiry")
			}
		}
	}
}

// ── 4. 사입현황(DB)과 맞추기 ─────────────────────────────────────────

type purchaseRef struct {
	date     string
	supplier int64
	name     string
	qty      float64
}

// pairScore : 판독 줄 ↔ 사입현황 한 건이 같은 사입일 가능성 (높을수록 비슷, 2.5 미만은 짝짓지 않음)
func pairScore(r LotPhotoRow, p purchaseRef) float64 {
	sc := 0.0
	if r.SupplierID != 0 && p.supplier != 0 {
		if r.SupplierID == p.supplier {
			sc += 3
		} else {
			sc -= 4
		}
	}
	dbDigits := strings.ReplaceAll(p.date, "-", "")
	if r.Date != "" && r.Date == p.date {
		sc += 5
	} else if d := strings.NewReplacer("-", "", ".", "", ",", "").Replace(r.DateRaw); len(d) >= 4 {
		// 일부만 읽은 날짜: 뒤에서부터 맞는 숫자 (월·일이 같으면 크게)
		k := 0
		for k < len(d) && k < len(dbDigits) && d[len(d)-1-k] == dbDigits[len(dbDigits)-1-k] {
			k++
		}
		sc += float64(k) * 0.6
		if k < 4 {
			sc -= 1
		}
	}
	if r.Qty > 0 && p.qty > 0 && math.Abs(r.Qty-p.qty) < 0.01 {
		sc += 1
	}
	return sc
}

// alignWithPurchases : 같은 약품의 사입현황(DB)과 줄을 맞춰 날짜·도매·수량은 DB 값을 쓴다.
// 화면과 DB 모두 날짜 내림차순이므로 순서를 지키는 짝짓기(동적 계획법)로 가장 잘 맞는 짝을 고른다.
// 그래서 날짜를 제대로 못 읽어도 도매 순서와 읽은 숫자 일부로 어느 사입인지 찾는다.
func (s *Service) alignWithPurchases(code string, rows []LotPhotoRow) {
	if len(rows) == 0 {
		return
	}
	q, err := s.DB.Query(`SELECT p.date, IFNULL(p.supplier_id, 0), IFNULL(s.name, ''), p.qty
		  FROM purchase_line p LEFT JOIN supplier s ON s.id = p.supplier_id
		 WHERE p.product_code = ? AND p.kind = 'purchase' AND p.qty > 0
		 ORDER BY p.date DESC, p.id DESC`, code)
	if err != nil {
		return
	}
	var refs []purchaseRef
	for q.Next() {
		var p purchaseRef
		var n int64
		if q.Scan(&p.date, &p.supplier, &p.name, &n) == nil {
			p.qty = domain.Qty(n).Float()
			refs = append(refs, p)
		}
	}
	q.Close()
	if len(refs) == 0 {
		return
	}
	const minPair = 2.5
	n, m := len(rows), len(refs)
	dp := make([][]float64, n+1)
	for i := range dp {
		dp[i] = make([]float64, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			best := math.Max(dp[i-1][j], dp[i][j-1])
			if sc := pairScore(rows[i-1], refs[j-1]); sc >= minPair {
				best = math.Max(best, dp[i-1][j-1]+sc)
			}
			dp[i][j] = best
		}
	}
	apply := func(r *LotPhotoRow, p purchaseRef) {
		how := "order"
		if r.Date == p.date {
			how = "date"
		}
		r.Date, r.Matched = p.date, how
		if p.supplier > 0 {
			r.SupplierID, r.SupplierName = p.supplier, p.name
		}
		if p.qty > 0 {
			r.Qty = p.qty
		}
		setSure(r, "date")
		setSure(r, "supplier")
		setSure(r, "qty")
		if how == "order" {
			r.Unsure = append(r.Unsure, "date")
		}
	}
	match := make([]int, n)
	for i := range match {
		match[i] = -1
	}
	for i, j := n, m; i > 0 && j > 0; {
		sc := pairScore(rows[i-1], refs[j-1])
		switch {
		case sc >= minPair && math.Abs(dp[i][j]-(dp[i-1][j-1]+sc)) < 1e-9:
			match[i-1] = j - 1
			i, j = i-1, j-1
		case dp[i][j] == dp[i-1][j]:
			i--
		default:
			j--
		}
	}
	// 맞춘 줄 사이에 못 맞춘 줄이 남으면, 그 사이의 사입 중에서 순서를 지키며 느슨하게 짝짓는다
	// (도매가 어긋나는 짝은 하지 않고, 이렇게 맞춘 줄은 '순서로 추정'으로 표시해 사용자가 확인한다)
	for i := 0; i < n; {
		if match[i] >= 0 {
			i++
			continue
		}
		k := i
		for k < n && match[k] < 0 {
			k++
		}
		lo, hi := -1, m
		if i > 0 {
			lo = match[i-1]
		}
		if k < n {
			hi = match[k]
		}
		if (lo >= 0 || hi < m) && hi-lo-1 > 0 {
			gr, gp := rows[i:k], refs[lo+1:hi]
			soft := func(a, b int) (float64, bool) {
				sc := pairScore(gr[a], gp[b])
				return sc + 1.5, sc >= -1
			}
			g := make([][]float64, len(gr)+1)
			for x := range g {
				g[x] = make([]float64, len(gp)+1)
			}
			for a := 1; a <= len(gr); a++ {
				for b := 1; b <= len(gp); b++ {
					v := math.Max(g[a-1][b], g[a][b-1])
					if sc, ok := soft(a-1, b-1); ok {
						v = math.Max(v, g[a-1][b-1]+sc)
					}
					g[a][b] = v
				}
			}
			for a, b := len(gr), len(gp); a > 0 && b > 0; {
				sc, ok := soft(a-1, b-1)
				switch {
				case ok && math.Abs(g[a][b]-(g[a-1][b-1]+sc)) < 1e-9:
					match[i+a-1] = lo + b
					a, b = a-1, b-1
				case g[a][b] == g[a-1][b]:
					a--
				default:
					b--
				}
			}
		}
		i = k
	}
	for i, j := range match {
		if j >= 0 {
			apply(&rows[i], refs[j])
		}
	}
}
