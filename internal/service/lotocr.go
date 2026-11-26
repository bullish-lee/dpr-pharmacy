package service

// 사입정보 사진 → Windows 기본 OCR(무료·오프라인) → 사입일자·사입도매·수량·유효기간·제조번호.
//
// 1. 사진 손질: 화면 캡처처럼 작은 이미지는 키우고(작은 글꼴은 OCR 이 못 읽는다), 휴대폰으로 모니터를 찍은 사진은
//    모아레(화면 격자 무늬)를 흐리게 지운 두 가지 버전을 만든다. 모두 흑백·대비 보정.
// 2. Windows OCR(한국어)로 단어와 위치를 읽는다.
// 3. 표 머리(사입일자·거래처·수량·유효기간·제조번호…)를 찾아 열 위치를 정하고, 단어를 줄·열로 나눈다.
//    머리가 없거나 반쯤 잘렸으면 칸의 모양(날짜·수량·금액·제조번호)으로 열을 정한다 (lotocr_shape.go).
//    날짜가 있는 사입 줄은 유효기간·제조번호를 못 읽어도 버리지 않는다 — 빈 칸으로 보여 주고 사용자가 채운다.
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
	"image/jpeg"
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
	// Alt : 두 글자 인식 엔진이 다르게 읽은 칸의 다른 쪽 값 (칸 이름 → 값, 도매가 등록된 거래처면 "supplierId" 에 번호).
	// 화면이 두 후보를 나란히 보여 주고 사용자가 고른다 (lotocr_compare.go).
	Alt map[string]string `json:"alt,omitempty"`
	y   float64           // 원본 그림에서 줄의 세로 위치 (두 엔진의 줄을 짝지을 때)
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
	// 세로로 찍은 휴대폰 사진: 화소는 누워 있고 방향 표시만 붙어 있다 → 바로 세운다 (lotocr_exif.go).
	// 보조 엔진에도 바로 세운 그림을 준다 — 두 엔진의 좌표가 같아야 줄을 짝짓는다. 방향 표시가 없으면 올린 파일 그대로.
	paddleSrc := raw
	if o := exifOrientation(raw); o > 1 {
		src = orientImage(src, o)
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: 95}); err != nil {
			return nil, err
		}
		paddleSrc = buf.Bytes()
		s.Log.Info("사입정보 사진을 방향 표시대로 바로 세움", "orientation", o)
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
	// 보조 엔진(PaddleOCR)을 Windows 글자 인식과 함께 돌린다. 안 되면 Windows 결과만 쓴다 (lotocr_compare.go).
	type paddleOut struct {
		r  platform.OCRResult
		ok bool
	}
	pch := make(chan paddleOut, 1)
	go func() {
		r, ok := s.paddleRead(ctx, paddleSrc, dir)
		pch <- paddleOut{r, ok}
	}()
	start := time.Now()
	results, werr := ocrRunner(ctx, paths)
	s.Log.Info("사입정보 사진 OCR", "mode", mode, "variants", len(variants), "ms", time.Since(start).Milliseconds(), "err", werr)
	pd := <-pch
	if werr != nil && !pd.ok {
		return nil, werr
	}

	sup := s.supplierMatcher()
	var parsed []parsedTable
	for i, r := range results {
		if werr != nil || i >= len(variants) {
			break
		}
		parsed = append(parsed, parseLotTable(r, variants[i].scale, sup))
	}
	res := mergeTables(parsed)
	res.Mode = mode
	if pd.ok {
		combineEngines(res, parsed, parseLotTable(pd.r, 1, sup), werr != nil)
	}
	for i := range res.Rows {
		res.Rows[i].Code = code
	}
	unifyLots(res.Rows)
	s.alignWithPurchases(code, res.Rows)
	if len(res.Rows) == 0 && res.Note == "" {
		res.Note = "사입 줄을 찾지 못했습니다. 사입일자·거래처·유효기간·제조번호 열이 보이게 다시 올려 주세요."
	}
	if len(res.Rows) > 0 && !strings.Contains(res.Note, "유효기간·제조번호를 읽지 못했습니다") {
		none := true
		for _, r := range res.Rows {
			if r.Lot != "" || r.Expiry != "" {
				none = false
				break
			}
		}
		if none {
			res.Note = "사입 줄은 찾았지만 유효기간·제조번호 열이 보이지 않습니다. 그 열이 보이게 다시 올리거나, 빈 칸을 직접 채워 주세요."
		}
	}
	for _, r := range results {
		if r.Warn == "no_korean" && !pd.ok { // 보조 엔진(한국어)이 읽었으면 도매 이름도 있다
			note := "이 PC 에 한국어 글자 인식이 없어 도매 이름은 읽지 못했습니다 — 도매를 직접 고르세요 (설정 → 시간 및 언어 → 언어에서 '한국어'를 추가하면 읽습니다)."
			if res.Note != "" {
				note += " " + res.Note
			}
			res.Note = note
			break
		}
	}
	for i := range res.Rows {
		dropSameAlt(&res.Rows[i])
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
		plain := ocrVariant{img: stretchContrast(resizeGray(g, k), 0.01, 0.99), scale: k}
		if looksLikePhoto(g) {
			// 메신저로 줄여 보낸 휴대폰 사진: 크기는 작아도 화면 무늬·잡음이 있다 → 흐리게 한 버전도 함께 읽는다
			soft := ocrVariant{img: stretchContrast(resizeGray(boxBlur(g, 1), k), 0.02, 0.98), scale: k}
			return []ocrVariant{plain, soft}, "photo"
		}
		return []ocrVariant{plain}, "screenshot"
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

// looksLikePhoto : 화면 캡처는 바탕이 고르고(이웃 픽셀이 똑같다), 카메라 사진은 잡음 때문에 거의 모든 픽셀이 조금씩 다르다
func looksLikePhoto(g *image.Gray) bool {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	if w < 64 || h < 16 {
		return false
	}
	same, n := 0, 0
	for y := 0; y < h; y += 2 {
		row := g.Pix[y*g.Stride : y*g.Stride+w]
		for x := 0; x+1 < w; x += 2 {
			if row[x] == row[x+1] {
				same++
			}
			n++
		}
	}
	return n > 0 && float64(same)/float64(n) < 0.3
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
	rows     []parsedRow
	header   bool
	raw      string
	skipped  int
	skipY    []float64 // 반품·조정으로 뺀 줄의 세로 위치 (다른 버전이 같은 줄을 사입으로 되살리지 않게)
	rowH     float64
	fallback bool // 표다운 줄이 없어 날짜만 있는 줄을 그대로 내놓음
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
	key      string
	x        float64
	x0, x1   float64 // 그 열 값들의 가로 범위 (모르면 0)
	inferred bool    // 머리가 아니라 내용의 모양으로 정한 열
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

	cols, headerY := findLotHeader(boxes, medH)
	out = parseLotBody(boxes, cols, headerY, medH, sup)
	if out.header {
		// 머리로 본 줄이 엉뚱한 글자였을 수 있다 (표 아래의 다른 글자, 화면 안내문 등).
		// 머리 기준으로는 한 줄도 못 읽었거나 머리 위쪽에 줄이 더 많으면, 머리 없이 내용 모양으로 읽은 쪽을 쓴다.
		alt := parseLotBody(boxes, nil, 0, medH, sup)
		above := 0
		for _, r := range alt.rows {
			if r.y < headerY && (r.Lot != "" || r.Expiry != "") {
				above++
			}
		}
		empty := len(out.rows) == 0 || out.fallback // 머리 기준으로는 표다운 줄이 하나도 없음
		if n, a := usableRows(out), usableRows(alt); (n == 0 && a > 0) || above > n || (empty && len(alt.rows) > 0 && !alt.fallback) || (len(out.rows) == 0 && len(alt.rows) > 0) {
			out = alt
		}
	}
	out.raw = raw.String()
	return out
}

// usableRows : 유효기간이나 제조번호를 읽은 줄 수
func usableRows(t parsedTable) int {
	if t.fallback {
		return 0
	}
	n := 0
	for _, r := range t.rows {
		if r.Lot != "" || r.Expiry != "" {
			n++
		}
	}
	return n
}

// parseLotBody : 머리 열(없으면 nil) 기준으로 본문 줄을 읽는다
func parseLotBody(boxes []ocrBox, hdr []ocrCol, headerY, medH float64, sup func(string) (int64, string)) parsedTable {
	out := parsedTable{header: len(hdr) > 0, rowH: medH * 1.6}
	var body []ocrBox
	for _, b := range boxes {
		if !out.header || b.cy() > headerY+medH*0.6 {
			body = append(body, b)
		}
	}
	lines := groupRows(body, medH)
	toks := make([][]lotTok, len(lines))
	for i, line := range lines {
		toks[i] = rowTokens(line, medH)
	}
	if out.header {
		// 표가 끝난 뒤(빈 공간이 크게 벌어진 뒤)의 글자는 다른 표·화면 요소다.
		// 다만 날짜가 있는 줄이면 표가 이어지는 것으로 본다 (종이가 접힌 자리, 사진의 빈 줄).
		lastY := headerY
		for i, line := range lines {
			if len(line) < 3 {
				continue
			}
			if line[0].cy()-lastY > medH*5 && !hasDateTok(toks[i]) {
				lines, toks = lines[:i], toks[:i]
				break
			}
			lastY = line[0].cy()
		}
	}
	var data [][]lotTok
	for _, t := range toks {
		if hasDateTok(t) {
			data = append(data, t)
		}
	}
	m := buildColModel(hdr, data, medH, sup)
	var weak []parsedRow
	for i, line := range lines {
		row, st := buildLotRow(line, toks[i], m, medH, sup)
		switch st {
		case rowSkip:
			out.skipped++
			out.skipY = append(out.skipY, line[0].cy())
		case rowKeep:
			out.rows = append(out.rows, *row)
		case rowWeak:
			weak = append(weak, *row)
		}
	}
	// 반드시 지키는 약속: 날짜가 있는 (반품·조정이 아닌) 줄이 하나라도 있으면 '0줄'로 끝내지 않는다.
	// 표다운 줄이 하나도 없으면 날짜만 읽힌 줄이라도 내놓는다 (모든 칸 '확인 필요').
	if len(out.rows) == 0 && len(weak) > 0 {
		out.rows, out.fallback = weak, true
		for i := range out.rows {
			out.rows[i].Unsure, out.rows[i].score = []string{"date", "supplier", "qty", "expiry", "lot"}, 0
		}
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
				bestCols = append(bestCols, ocrCol{key: d.key, x: d.x})
			}
		}
	}
	sort.Slice(bestCols, func(i, j int) bool { return bestCols[i].x < bestCols[j].x })
	return bestCols, bestY
}

// groupRows : 세로 위치가 비슷한 단어끼리 한 줄로 묶는다.
// 왼쪽에서 오른쪽으로 가며 그 줄의 마지막 단어들과 비교한다 — 사진이 조금 기울거나 휘어 줄의 왼쪽과 오른쪽 높이가
// 달라도(줄 전체 평균과 비교하면 한 줄이 둘로 쪼개져 날짜와 제조번호가 갈라진다) 한 줄로 따라간다.
func groupRows(boxes []ocrBox, medH float64) [][]ocrBox {
	sorted := append([]ocrBox(nil), boxes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].x0 < sorted[j].x0 })
	var rows [][]ocrBox
	tail := func(r []ocrBox) float64 {
		n := len(r)
		if n >= 2 {
			return (r[n-1].cy() + r[n-2].cy()) / 2
		}
		return r[n-1].cy()
	}
	for _, b := range sorted {
		best, bd := -1, medH*0.55
		for i, r := range rows {
			if d := math.Abs(b.cy() - tail(r)); d <= bd {
				best, bd = i, d
			}
		}
		if best < 0 {
			rows = append(rows, []ocrBox{b})
		} else {
			rows[best] = append(rows[best], b)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i][0].cy() < rows[j][0].cy() })
	return rows
}

var notPurchase = []string{"반품", "조정", "재고정리", "고정리", "폐기", "차용", "출고"}

var qtyLike = regexp.MustCompile(`^-?\d{1,5}\.\d{2}$`)

type rowState int

const (
	rowNone rowState = iota // 표의 줄이 아님 (잡음)
	rowKeep                 // 사입 줄
	rowWeak                 // 날짜만 읽힌 줄 — 다른 줄이 하나도 없을 때만 내놓는다
	rowSkip                 // 반품·재고조정·취소 등 사입이 아닌 줄
)

func countDigits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// buildLotRow : 한 줄의 단어를 열에 나눠 값을 만든다.
//
// 줄을 남기는 기준은 여기 한 곳에만 둔다:
//   - 반품·조정 낱말이 있거나 음수 수량·금액이면 사입이 아님 (rowSkip)
//   - 유효기간이나 제조번호를 읽었으면 남긴다 (날짜도 확실한 칸도 없고 제조번호가 짧으면 잡음)
//   - 둘 다 못 읽었어도 날짜가 있고 표의 줄답게 수량·도매·금액이 있으면 남긴다 (빈 칸은 사용자가 채움)
//   - 날짜만 있으면 rowWeak
func buildLotRow(line []ocrBox, toks []lotTok, m *colModel, medH float64, sup func(string) (int64, string)) (*parsedRow, rowState) {
	cols := m.cols
	cells := map[string][]string{}
	var all []string
	var minX, maxX float64 = math.Inf(-1), math.Inf(1)
	if m.header && len(cols) >= 2 {
		// 표 바깥(왼쪽 메뉴·오른쪽 여백)의 글자는 뺀다. 머리가 없으면 자르지 않는다 (모든 열을 내용으로 정했으므로).
		minX = cols[0].x - (cols[1].x-cols[0].x)*0.8
		maxX = cols[len(cols)-1].x + (cols[len(cols)-1].x-cols[len(cols)-2].x)*0.8
		if c := cols[0]; c.x1 > 0 {
			minX = math.Min(minX, c.x0-medH)
		}
		if c := cols[len(cols)-1]; c.x1 > 0 {
			maxX = math.Max(maxX, c.x1+medH)
		}
	}
	for _, b := range line {
		if b.cx() < minX || b.cx() > maxX {
			continue
		}
		all = append(all, b.t)
		if len(cols) == 0 {
			continue
		}
		// 가장 가까운 열 (열 사이 경계 = 두 머리의 가운데, 내용으로 잡은 열은 두 열 사이 빈 곳의 가운데)
		k := cols[len(cols)-1].key
		for i := 0; i < len(cols)-1; i++ {
			if b.cx() < m.colEdge(i, medH) {
				k = cols[i].key
				break
			}
		}
		cells[k] = append(cells[k], b.t)
	}
	if len(all) == 0 {
		return nil, rowNone
	}
	joined := func(k string) string { return strings.Join(cells[k], "") }

	// 이 줄의 칸 (표 안쪽만)
	var in []lotTok
	hasDate, purchaseWord, kindHit := false, false, false
	negs, nums := 0, 0
	for _, t := range toks {
		if t.cx() < minX || t.cx() > maxX {
			continue
		}
		in = append(in, t)
		switch t.kind {
		case tkDate, tkDate8:
			hasDate = true
		case tkDec, tkAmt:
			nums++
			if t.neg {
				negs++
			}
		case tkKind:
			if _, p := isKindWord(strings.Join(strings.Fields(t.t), "")); p {
				purchaseWord = true
			} else {
				kindHit = true
			}
		}
	}
	if hasNotPurchaseWord(strings.ReplaceAll(joined("kind")+joined("supplier"), " ", "")) {
		kindHit = true
	}
	// 반품·조정: 낱말로, 또는 (낱말을 잘못 읽었거나 한국어 인식이 없을 때) 음수 수량·금액으로
	if kindHit {
		if m.header || hasDate || negs > 0 {
			return nil, rowSkip
		}
		return nil, rowNone // 표 밖의 안내문 등
	}
	if negs >= 2 || (negs == 1 && nums == 1 && hasDate) {
		return nil, rowSkip
	}

	row := &parsedRow{y: line[0].cy()}
	unsure := map[string]bool{}
	row.Date, unsure["date"] = parseOCRDate(joined("date"), true)
	row.Expiry, unsure["expiry"] = parseOCRDate(joined("expiry"), false)
	if row.Expiry != "" && row.Expiry < time.Now().Format("2006-01-02") {
		unsure["expiry"] = true // 이미 지난 유효기간: 숫자를 잘못 읽었을 가능성이 크다
	}
	if lotCell := joined("lot"); classifyTok(lotCell).kind != tkDate { // 제조번호 자리에 걸친 다른 날짜(화면 시계 등)는 제조번호가 아니다
		row.Lot, unsure["lot"] = cleanLot(lotCell)
	} else {
		unsure["lot"] = true
	}
	// 유효기간과 제조번호가 한 단어로 붙어 읽힘 (202905310031201): 앞 8자리 = 유효기간, 나머지 = 제조번호 (둘 다 '확인 필요')
	if row.Lot == "" {
		if e, l, ok := splitExpiryLot(joined("expiry")); ok {
			row.Expiry, row.Lot, unsure["expiry"], unsure["lot"] = e, l, true, true
		}
	} else if row.Expiry == "" {
		if e, l, ok := splitExpiryLot(joined("lot")); ok {
			row.Expiry, row.Lot, unsure["expiry"], unsure["lot"] = e, l, true, true
		}
	}
	if m.lotDigits && row.Lot != "" {
		// 숫자뿐인 제조번호 열에 영문이 섞임: 0·1 을 O·I 로 읽은 것이면 되돌리고, 어느 쪽이든 '확인 필요'
		fixed, pure := digitsOnlyLot(row.Lot)
		if fixed != row.Lot || !pure {
			row.Lot, unsure["lot"] = fixed, true
		}
	}
	var supWords []string
	for _, w := range cells["supplier"] {
		if k, _ := isKindWord(strings.TrimSpace(w)); k && !m.has["kind"] {
			continue // 구분 열이 따로 없으면 '사입'이 도매 이름 앞에 붙어 온다
		}
		supWords = append(supWords, w)
	}
	row.SupplierRaw = strings.TrimSpace(strings.Join(supWords, " "))
	row.DateRaw = fixDigits(joined("date"))
	q, okq, qtyOdd := parseOCRQty(joined("total"), m.decimals)
	if !okq {
		q, okq, qtyOdd = parseOCRQty(joined("qty"), m.decimals)
		if okq && m.has["total"] {
			qtyOdd = true // 총수량 열이 있는데 이 줄에서는 못 읽어 수량(묶음 수)을 대신 넣음
		}
	}
	if !okq && len(cells["total"]) == 0 && len(cells["qty"]) == 0 {
		// 수량 열을 못 정함: 이팜은 수량을 소수 둘째 자리(4.00, 120.00)로, 금액은 쉼표로 쓴다 → 소수가 둘이면 뒤가 총수량
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
			qtyOdd = len(decs) == 1 // 하나뿐이면 수량인지 총수량인지 모른다
		}
	}
	if okq {
		row.Qty = q
	}
	unsure["qty"] = !okq || qtyOdd
	if row.Qty < 0 {
		return nil, rowSkip // 취소(음수) 줄
	}

	// 열로 못 찾은 칸은 이 줄 안에서 모양으로 찾는다 (모두 '확인 필요')
	di := -1
	for i, t := range in {
		if t.kind == tkDate || (t.kind == tkDate8 && di < 0 && !hasDashDate(in)) {
			di = i
			break
		}
	}
	if row.Date == "" && di >= 0 && in[di].kind == tkDate {
		row.Date, unsure["date"] = in[di].date, true
		if row.DateRaw == "" {
			row.DateRaw = strings.ReplaceAll(row.Date, "-", "")
		}
	}
	if row.Expiry == "" && row.Lot == "" {
		// 사입일자 오른쪽의 마지막 날짜 = 유효기간, 그 오른쪽의 코드 = 제조번호. 유효기간이 없으면 제조번호도 짐작하지 않는다
		// (칸이 빈 사입 줄에서 '마지막 칸'을 제조번호로 삼으면 금액이나 과세 구분이 들어간다).
		ei := -1
		for j := len(in) - 1; j > di; j-- {
			if in[j].kind == tkDate8 || in[j].kind == tkDate {
				ei = j
				break
			}
		}
		if ei >= 0 && (di >= 0 || in[ei].date > time.Now().Format("2006-01-02")) {
			row.Expiry, unsure["expiry"] = in[ei].date, true
			for j := ei + 1; j < len(in); j++ {
				if in[j].kind == tkCode {
					row.Lot, _ = cleanLot(in[j].t)
					unsure["lot"] = true
					break
				}
			}
		}
	}

	if row.SupplierRaw != "" {
		row.SupplierID, row.SupplierName = sup(row.SupplierRaw)
	}
	unsure["supplier"] = row.SupplierID == 0
	for k, w := range m.weak {
		if w {
			unsure[k] = true
		}
	}
	for _, k := range []string{"date", "supplier", "qty", "expiry", "lot"} {
		if unsure[k] {
			row.Unsure = append(row.Unsure, k)
		} else {
			row.score++
		}
	}
	dated := hasDate || row.Date != "" || countDigits(row.DateRaw) >= 4
	if row.Lot == "" && row.Expiry == "" {
		if !dated {
			return nil, rowNone
		}
		if okq || row.SupplierID != 0 || purchaseWord || nums >= 2 {
			return row, rowKeep
		}
		return row, rowWeak
	}
	// 날짜도 확실한 칸도 없고 제조번호도 짧으면 표의 줄이 아니라 잡음으로 본다
	if !dated && row.score == 0 && len(row.Lot) < 5 {
		return nil, rowNone
	}
	return row, rowKeep
}

// splitExpiryLot : 8자리 날짜 뒤에 제조번호가 붙은 칸을 나눈다
func splitExpiryLot(cell string) (expiry, lot string, ok bool) {
	raw := strings.Join(strings.Fields(cell), "")
	if len(raw) < 8+3 || !shapeDate8.MatchString(raw[:8]) {
		return "", "", false
	}
	expiry, ok = validDate(raw[:4], raw[4:6], raw[6:8])
	if !ok {
		return "", "", false
	}
	lot, _ = cleanLot(raw[8:])
	return expiry, lot, lot != ""
}

func hasDashDate(toks []lotTok) bool {
	for _, t := range toks {
		if t.kind == tkDate {
			return true
		}
	}
	return false
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
	if len(digits) > 8 {
		changed = true // 옆 칸 숫자가 붙었을 수 있다
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

var commaDecimal = regexp.MustCompile(`^-?\d{1,5},\d{2}$`)

// parseOCRQty : 수량 칸. decimals = 이 표의 수량이 소수 둘째 자리(2.00)로 적혀 있음 (이팜).
// 작은 글자에서는 소수점을 쉼표로 읽거나(120,00) 놓친다(200) — 그대로 두면 100배가 된다. 되돌리고 odd=true (확인 필요).
func parseOCRQty(s string, decimals bool) (v float64, ok, odd bool) {
	f := fixDigits(s)
	if commaDecimal.MatchString(f) {
		v, ok = parseOCRNumber(strings.Replace(f, ",", ".", 1))
		return v, ok, true
	}
	v, ok = parseOCRNumber(f)
	if ok && decimals && !strings.Contains(f, ".") {
		odd = true
		if d := strings.TrimPrefix(strings.ReplaceAll(f, ",", ""), "-"); len(d) >= 3 && strings.HasSuffix(d, "00") {
			v /= 100
		}
	}
	return v, ok, odd
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
	switch {
	case base.fallback:
		res.Note = "날짜가 있는 줄은 찾았지만 유효기간·제조번호를 읽지 못했습니다. 사진과 비교해 빈 칸을 직접 채우거나, 유효기간·제조번호 열이 보이게 다시 올려 주세요."
	case !base.header && len(base.rows) > 0:
		res.Note = "표 머리(사입일자·거래처·유효기간·제조번호)가 없어 칸의 모양(날짜·수량·제조번호)으로 열을 정했습니다. 사진과 꼭 비교하세요."
	}
	// 어느 버전이든 반품·조정으로 본 줄은 사입으로 되살리지 않는다 (한 버전이 '반품'을 잘못 읽어도 다른 버전이 잡는다)
	isSkipped := func(y float64, except int) bool {
		for j, t := range ts {
			if j == except {
				continue
			}
			for _, sy := range t.skipY {
				if math.Abs(sy-y) <= base.rowH*0.6 {
					return true
				}
			}
		}
		return false
	}
	baseRows := base.rows[:0:0]
	for _, r := range base.rows {
		if isSkipped(r.y, best) {
			res.Skipped++
			continue
		}
		baseRows = append(baseRows, r)
	}
	for _, r := range baseRows {
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
	for i, r := range baseRows {
		all = append(all, yrow{r.y, res.Rows[i]})
	}
	for j, t := range ts {
		if j == best {
			continue
		}
		for _, o := range t.rows {
			// 다른 버전이 뺀 줄, 날짜만 읽힌 줄(fallback)은 바탕에 줄이 있으면 가져오지 않는다
			dup := isSkipped(o.y, j) || (t.fallback && len(baseRows) > 0)
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
		a.row.y = a.y
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
		if cur == "" || !isUnsure(rows[i], "lot") || rows[i].Alt["lot"] != "" {
			continue // 확실하게 읽은 값, 두 엔진이 다르게 읽은 값(사용자가 두 후보 중 고른다)은 그대로
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
		if r.Lot == "" || len(m) == 0 || r.Alt["expiry"] != "" {
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

// dateConflict : 사입일자를 또렷이 읽었는데 사입현황 날짜와 두 자리 이상 다르면 다른 사입이다.
// (한 자리 차이는 OCR 이 숫자 하나를 잘못 읽은 것일 수 있어 순서 짝짓기에 맡긴다.)
// 이 확인이 없으면 사입현황에 아직 없는 새 사입 줄이 (도매·수량이 같거나 순서만으로) 옛 사입과 짝지어져 날짜가 바뀐다.
func dateConflict(r LotPhotoRow, p purchaseRef) bool {
	if r.Date == "" || isUnsure(r, "date") || len(r.Date) != len(p.date) {
		return false
	}
	diff := 0
	for i := 0; i < len(r.Date); i++ {
		if r.Date[i] != p.date[i] {
			diff++
		}
	}
	return diff >= 2
}

// alignWithPurchases : 같은 약품의 사입현황(DB)과 줄을 맞춰 날짜·도매·수량은 DB 값을 쓴다.
// 화면과 DB 모두 날짜 내림차순이므로 순서를 지키는 짝짓기(동적 계획법)로 가장 잘 맞는 짝을 고른다.
// 그래서 날짜를 제대로 못 읽어도 도매 순서와 읽은 숫자 일부로 어느 사입인지 찾는다.
//
// 똑같이 잘 맞는 짝이 여럿이면 더 최근(위쪽) 사입을 고른다 — 화면은 최근 사입부터 보여 주고, 엑셀에는 화면 아래로 더 옛 사입이 있다.
// 사진에서 도매를 못 읽은 줄을 날짜 근거 없이 순서로만 짝지었으면 그 도매는 짐작이다 → '확인 필요'로 둔다
// (줄 하나를 놓치면 그 아래 줄들이 한 칸씩 밀려 제조번호가 다른 도매에 붙는다).
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
			if sc := pairScore(rows[i-1], refs[j-1]); sc >= minPair && !dateConflict(rows[i-1], refs[j-1]) {
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
		// 도매를 믿을 근거: 같은 날짜이거나, 도매를 빼고 날짜 숫자(와 수량)만으로도 짝이 되는 사입.
		// 근거가 없는데 (1) 사진에서 도매를 못 읽어 순서로만 채웠거나 (2) 두 엔진이 서로 다른 거래처로 읽은 도매로 짝지었으면
		// 그 도매는 확인된 것이 아니다 → '확인 필요'로 두고 다른 후보도 남긴다.
		bare := *r
		bare.SupplierID = 0
		dated := how == "date" || pairScore(bare, p) >= minPair
		doubt := !dated && p.supplier > 0 && (r.SupplierID == 0 || r.Alt["supplierId"] != "")
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
		// 사입현황(엑셀) 값으로 바로잡은 칸은 두 엔진의 다른 후보가 의미 없다
		for _, k := range []string{"date", "qty"} {
			delete(r.Alt, k)
		}
		if how == "order" {
			r.Unsure = append(r.Unsure, "date")
		}
		if doubt {
			r.Unsure = append(r.Unsure, "supplier")
		}
		if !doubt || r.Alt["supplierId"] == "" { // 등록된 거래처로 읽은 후보만 남긴다 (알아보지 못한 글자는 후보가 못 된다)
			delete(r.Alt, "supplier")
			delete(r.Alt, "supplierId")
		}
	}
	match := make([]int, n)
	for i := range match {
		match[i] = -1
	}
	for i, j := n, m; i > 0 && j > 0; {
		sc := pairScore(rows[i-1], refs[j-1])
		switch {
		case dp[i][j] == dp[i][j-1]:
			j-- // 이 사입이 없어도 점수가 같다 → 더 최근(위쪽) 사입과 짝짓는다
		case sc >= minPair && !dateConflict(rows[i-1], refs[j-1]) && math.Abs(dp[i][j]-(dp[i-1][j-1]+sc)) < 1e-9:
			match[i-1] = j - 1
			i, j = i-1, j-1
		default:
			i--
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
				if dateConflict(gr[a], gp[b]) {
					return 0, false
				}
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
				case g[a][b] == g[a][b-1]:
					b-- // 같은 점수면 더 최근(위쪽) 사입
				case ok && math.Abs(g[a][b]-(g[a-1][b-1]+sc)) < 1e-9:
					match[i+a-1] = lo + b
					a, b = a-1, b-1
				default:
					a--
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
