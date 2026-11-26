package service

// 사입정보 사진을 두 글자 인식 엔진(Windows 기본 · PaddleOCR)으로 읽어 칸마다 견준다 (D25).
//
//   - 두 엔진이 같은 값을 읽은 칸 → 믿는다 ('확인 필요' 표시를 지운다).
//   - 다르게 읽은 칸 → '확인 필요'로 두고 다른 쪽 값을 Alt 에 담는다. 화면이 "A | B" 로 보여 주고 사용자가 고른다.
//   - 한쪽만 읽은 칸 → 읽은 값을 쓰고, 그 엔진이 스스로 매긴 확신을 그대로 둔다.
//   - 한쪽 엔진만 찾은 줄 → 모든 칸을 '확인 필요'로 둔다 (다른 엔진은 그 줄을 못 봤다).
//
// PaddleOCR 을 쓸 수 없는 PC 에서는 견주지 않고 Windows 글자 인식 결과만 쓴다 (paddleRead 가 ok=false).

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"dpr/internal/platform"
)

// paddleRunner : 테스트에서 바꿔 끼운다
var paddleRunner = platform.PaddleOCR

// paddleTimeout : 보조 엔진에 주는 시간. 넘으면 Windows 글자 인식 결과만 쓴다
const paddleTimeout = 60 * time.Second

// lotCompareFields : 견주는 칸 (LotPhotoRow.Unsure 에 쓰는 이름과 같다)
var lotCompareFields = []string{"date", "supplier", "qty", "expiry", "lot"}

// lotEngineRow : 한 엔진이 읽은 한 줄 — 세로 위치(원본 그림 좌표, 줄의 가운데)와 값
type lotEngineRow struct {
	Y   float64
	Row LotPhotoRow
}

// paddleRead 는 올린 사진(JPG·PNG 파일 내용 그대로)을 PaddleOCR 로 읽는다 — 손질하지 않아 좌표가 원본과 같다 (scale 1).
// 다시 PNG 로 저장하지 않는다 (3200×2400 사진에서 0.75초가 걸렸다).
// 방향 표시가 붙은 누운 사진만 ReadLotPhoto 가 바로 세워 JPEG 로 다시 만든 것을 받는다.
// 쓸 수 없거나 실패하면 ok=false: 기록만 남기고, 부르는 쪽은 Windows 글자 인식만으로 계속한다.
func (s *Service) paddleRead(ctx context.Context, raw []byte, dir string) (res platform.OCRResult, ok bool) {
	if s.paddleOff.Load() {
		return res, false
	}
	start := time.Now()
	path := filepath.Join(dir, "paddle-src.img") // 형식은 내용으로 알아본다
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		s.Log.Warn("보조 글자 인식: 그림 저장 실패", "err", err)
		return res, false
	}
	ctx, cancel := context.WithTimeout(ctx, paddleTimeout)
	defer cancel()
	out, err := paddleRunner(ctx, []string{path})
	if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
		s.paddleOff.Store(true) // 엔진을 못 열었거나 멈춤 — 이번 실행 동안은 Windows 글자 인식만 (사용자가 취소한 경우는 제외)
	}
	if err != nil || len(out) == 0 || out[0].Error != "" {
		msg := ""
		if len(out) > 0 {
			msg = out[0].Error
		}
		s.Log.Warn("보조 글자 인식(PaddleOCR)을 쓰지 못해 Windows 글자 인식만 씁니다", "err", err, "msg", msg, "ms", time.Since(start).Milliseconds())
		return res, false
	}
	s.Log.Info("사입정보 사진 보조 글자 인식", "engine", out[0].Engine, "runtime", out[0].Runtime, "lines", len(out[0].Lines), "ms", time.Since(start).Milliseconds())
	return out[0], true
}

// lotFieldText : 칸의 값을 글자로 (빈 값은 "")
func lotFieldText(r LotPhotoRow, k string) string {
	switch k {
	case "date":
		return r.Date
	case "expiry":
		return r.Expiry
	case "lot":
		return r.Lot
	case "qty":
		if r.Qty == 0 {
			return ""
		}
		return strconv.FormatFloat(r.Qty, 'f', -1, 64)
	case "supplier":
		if r.SupplierName != "" {
			return r.SupplierName
		}
		return r.SupplierRaw
	}
	return ""
}

func lotFieldEqual(a, b LotPhotoRow, k string) bool {
	switch k {
	case "qty":
		return math.Abs(a.Qty-b.Qty) < 0.005
	case "supplier":
		if a.SupplierID != 0 && b.SupplierID != 0 {
			return a.SupplierID == b.SupplierID
		}
		if a.SupplierID != 0 || b.SupplierID != 0 {
			return false // 한쪽만 등록된 거래처로 맞춰짐
		}
		squash := func(s string) string { return strings.Join(strings.Fields(s), "") }
		return squash(a.SupplierRaw) == squash(b.SupplierRaw)
	case "lot":
		return strings.EqualFold(a.Lot, b.Lot)
	}
	return lotFieldText(a, k) == lotFieldText(b, k)
}

func lotFieldCopy(dst *LotPhotoRow, src LotPhotoRow, k string) {
	switch k {
	case "date":
		dst.Date = src.Date
	case "expiry":
		dst.Expiry = src.Expiry
	case "lot":
		dst.Lot = src.Lot
	case "qty":
		dst.Qty = src.Qty
	case "supplier":
		dst.SupplierID, dst.SupplierName, dst.SupplierRaw = src.SupplierID, src.SupplierName, src.SupplierRaw
	}
}

func lotUnsure(r LotPhotoRow, k string) bool {
	for _, u := range r.Unsure {
		if u == k {
			return true
		}
	}
	return false
}

// compareLotRow : 같은 줄을 읽은 두 엔진의 결과를 칸마다 견준다. a 가 바탕이다 (값이 갈리면 a 를 쓰고 b 를 Alt 로).
// 다만 a 는 흐리게 읽었고 b 는 확실하게 읽은 칸은 b 를 쓰고 a 를 Alt 로 둔다.
func compareLotRow(a, b LotPhotoRow) LotPhotoRow {
	out := a
	out.Alt = nil
	if len(b.DateRaw) > len(a.DateRaw) {
		out.DateRaw = b.DateRaw
	}
	if out.Code == "" {
		out.Code = b.Code
	}
	unsure := map[string]bool{}
	for _, k := range lotCompareFields {
		av, bv := lotFieldText(a, k), lotFieldText(b, k)
		switch {
		case av == "" && bv == "":
			unsure[k] = true
		case bv == "":
			unsure[k] = lotUnsure(a, k)
		case av == "":
			lotFieldCopy(&out, b, k)
			unsure[k] = lotUnsure(b, k)
		case lotFieldEqual(a, b, k):
			// 같은 값: 등록된 거래처로 맞춘 쪽이 있으면 그쪽을 쓴다
			if k == "supplier" && a.SupplierID == 0 && b.SupplierID != 0 {
				lotFieldCopy(&out, b, k)
			}
			// 등록된 거래처와 못 맞춘 도매는 여전히 골라야 한다.
			// 두 엔진 모두 '확인 필요'로 둔 칸도 그대로 둔다 — 같은 판독 규칙을 거치므로 열을 잘못 짐작했다면 둘 다 똑같이 틀린다
			// (유효기간이 지난 약의 유효기간 열, 금액일지 모르는 제조번호 열 등).
			unsure[k] = (k == "supplier" && out.SupplierID == 0) || (lotUnsure(a, k) && lotUnsure(b, k))
		default:
			other := b
			if lotUnsure(a, k) && !lotUnsure(b, k) {
				other = a
				lotFieldCopy(&out, b, k)
			}
			unsure[k] = true
			if out.Alt == nil {
				out.Alt = map[string]string{}
			}
			out.Alt[k] = lotFieldText(other, k)
			if k == "supplier" && other.SupplierID != 0 {
				out.Alt["supplierId"] = strconv.FormatInt(other.SupplierID, 10)
			}
		}
	}
	out.Unsure = nil
	for _, k := range lotCompareFields {
		if unsure[k] {
			out.Unsure = append(out.Unsure, k)
		}
	}
	return out
}

// compareLotRows 는 두 엔진이 읽은 줄들을 세로 위치로 짝지어 칸마다 견준다. a 가 바탕 엔진이다.
// rowH 는 줄 간격(원본 그림 좌표) — 세로 위치가 이것의 0.6배 안이면 같은 줄로 본다. 0 이면 읽은 줄들의 간격에서 어림한다.
// 결과는 위에서 아래 순서.
func compareLotRows(a, b []lotEngineRow, rowH float64) []LotPhotoRow {
	if rowH <= 0 {
		rowH = guessRowHeight(a, b)
	}
	tol := rowH * 0.6
	// 가까운 짝부터 하나씩 (한 줄은 한 번만 짝짓는다)
	type pair struct {
		i, j int
		d    float64
	}
	var pairs []pair
	for i := range a {
		for j := range b {
			if d := math.Abs(a[i].Y - b[j].Y); d <= tol {
				pairs = append(pairs, pair{i, j, d})
			}
		}
	}
	sort.SliceStable(pairs, func(x, y int) bool { return pairs[x].d < pairs[y].d })
	matchA, matchB := make([]int, len(a)), make([]int, len(b))
	for i := range matchA {
		matchA[i] = -1
	}
	for j := range matchB {
		matchB[j] = -1
	}
	for _, p := range pairs {
		if matchA[p.i] < 0 && matchB[p.j] < 0 {
			matchA[p.i], matchB[p.j] = p.j, p.i
		}
	}
	type yrow struct {
		y   float64
		row LotPhotoRow
	}
	alone := func(r LotPhotoRow) LotPhotoRow {
		r.Unsure = append([]string(nil), lotCompareFields...)
		return r
	}
	var all []yrow
	for i, r := range a {
		if j := matchA[i]; j >= 0 {
			all = append(all, yrow{r.Y, compareLotRow(r.Row, b[j].Row)})
		} else {
			all = append(all, yrow{r.Y, alone(r.Row)})
		}
	}
	for j, r := range b {
		if matchB[j] < 0 {
			all = append(all, yrow{r.Y, alone(r.Row)})
		}
	}
	sort.SliceStable(all, func(x, y int) bool { return all[x].y < all[y].y })
	out := make([]LotPhotoRow, len(all))
	for i, r := range all {
		out[i] = r.row
	}
	return out
}

// guessRowHeight : 한 엔진 안에서 가장 좁은 줄 간격 (줄이 하나뿐이면 20).
// 한 엔진이 줄을 건너뛰어 읽었어도 다른 엔진의 촘촘한 간격이 잡힌다.
func guessRowHeight(sets ...[]lotEngineRow) float64 {
	best := 0.0
	for _, rows := range sets {
		ys := make([]float64, len(rows))
		for i, r := range rows {
			ys[i] = r.Y
		}
		sort.Float64s(ys)
		for i := 1; i < len(ys); i++ {
			if g := ys[i] - ys[i-1]; g > 0 && (best == 0 || g < best) {
				best = g
			}
		}
	}
	if best == 0 {
		return 20
	}
	return best
}

// combineEngines : Windows 글자 인식으로 읽은 결과(res, 그 바탕 표들 win)에 PaddleOCR 로 읽은 표 p 를 견준다.
//   - 두 엔진 모두 줄을 찾았으면 줄을 짝지어 칸마다 견준다 (같으면 믿고, 다르면 두 후보를 Alt 로).
//   - Windows 쪽이 줄을 못 찾았거나 Windows 글자 인식이 안 되면(winFailed) PaddleOCR 결과만 쓴다.
//   - 한쪽 엔진만 찾은 줄이 다른 엔진이 반품·조정으로 뺀 자리에 있으면 버린다.
func combineEngines(res *LotPhotoResult, win []parsedTable, p parsedTable, winFailed bool) {
	if res.RawText != "" {
		res.RawText += "\n--- 보조 글자 인식(PaddleOCR)\n" + p.raw
	} else {
		res.RawText = p.raw
	}
	if len(p.rows) == 0 || (p.fallback && len(res.Rows) > 0) {
		return
	}
	if len(res.Rows) == 0 {
		alone := mergeTables([]parsedTable{p})
		res.Rows, res.Skipped, res.Note = alone.Rows, alone.Skipped, alone.Note
		if winFailed {
			res.Note = strings.TrimSpace("Windows 글자 인식을 쓸 수 없어 보조 글자 인식(PaddleOCR)으로만 읽었습니다. " + res.Note)
		}
		return
	}
	rowH := p.rowH
	for _, t := range win {
		if t.rowH > rowH {
			rowH = t.rowH
		}
	}
	tol := rowH * 0.6
	var skipW []float64
	for _, t := range win {
		skipW = append(skipW, t.skipY...)
	}
	near := func(y float64, ys []float64) bool {
		for _, v := range ys {
			if math.Abs(v-y) <= tol {
				return true
			}
		}
		return false
	}
	var a, b []lotEngineRow
	var ay, by []float64
	for _, r := range res.Rows {
		ay = append(ay, r.y)
	}
	for _, r := range p.rows {
		by = append(by, r.y)
	}
	for _, r := range res.Rows {
		if near(r.y, p.skipY) && !near(r.y, by) {
			continue
		}
		a = append(a, lotEngineRow{r.y, r})
	}
	for _, r := range p.rows {
		if near(r.y, skipW) && !near(r.y, ay) {
			continue
		}
		b = append(b, lotEngineRow{r.y, r.LotPhotoRow})
	}
	res.Rows = compareLotRows(a, b, rowH)
}

// dropSameAlt : 다른 후보가 지금 값과 같아졌으면(사입현황·같은 제조번호로 맞춘 뒤) 지운다
func dropSameAlt(r *LotPhotoRow) {
	for k, v := range r.Alt {
		if k == "supplierId" {
			continue
		}
		if v == lotFieldText(*r, k) {
			delete(r.Alt, k)
			if k == "supplier" {
				delete(r.Alt, "supplierId")
			}
		}
	}
	if len(r.Alt) == 0 {
		r.Alt = nil
	}
}
