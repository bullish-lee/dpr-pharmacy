package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 두 글자 인식 엔진(Windows 기본 · PaddleOCR)을 같은 가상 사입현황 표 그림으로 견준다 — 칸별 정확도와 걸린 시간을 기록한다.
//
//	그림: ocr_sample.png(점검 그림) · table.png(보통 크기) · small.png(화면 캡처 크기의 작은 글씨) · color.png(색 바탕·색 글씨) ·
//	      photo.jpg(모니터를 휴대폰으로 찍은 듯: 기울기·모아레·흐림)  — 모두 지어낸 자료 (testdata/ocrbench/make.py)
//	칸  : 사입일자 · 도매 · 수량 · 유효기간(YYYYMMDD) · 제조번호(숫자만 / 영문+숫자 / 31007-1 꼴)
//
// 정확도는 기록만 한다 (실패로 치지 않는다). 다만:
//
//	DPR_PADDLE_REQUIRE=1  PaddleOCR 이 점검 그림을 읽어 내야 한다 (CI: Windows 에서 onnxruntime 이 열리고 도는지)
//	DPR_PADDLE_STRICT=1   PaddleOCR 이 모든 칸을 맞혀야 한다 (개발 PC: 기준 구현과 같은 결과인지)
//	DPR_OCR_BENCH_OUT=폴더  엔진별 결과(JSON)를 남긴다
const (
	paddleRequireEnv = "DPR_PADDLE_REQUIRE"
	paddleStrictEnv  = "DPR_PADDLE_STRICT"
	benchOutEnv      = "DPR_OCR_BENCH_OUT"
)

var benchFields = []string{"date", "supplier", "qty", "expiry", "lot"}

type benchRow map[string]string

func benchSampleRows() []benchRow {
	return []benchRow{
		{"date": "2026-08-12", "supplier": "가람도매", "qty": "45.00", "expiry": "20280131", "lot": "LOT-1001"},
		{"date": "2026-06-03", "supplier": "나래도매", "qty": "90.00", "expiry": "20271231", "lot": "LOT-1002"},
		{"date": "2026-04-27", "supplier": "다솜도매", "qty": "150.00", "expiry": "20280430", "lot": "LOT-1003"},
	}
}

// benchScore : 칸마다 정답이 읽혔는지 — 낱말이 정답과 같거나, 글줄(띄어쓰기 뺌)에 정답이 들어 있으면 읽은 것으로 친다.
// 표 읽기(숫자 칸의 한글 되돌리기 등)를 거치지 않은 날것의 인식 결과를 견준다.
func benchScore(res OCRResult, rows []benchRow) (hit map[string]int, missed []string) {
	words := map[string]bool{}
	var lines []string
	for _, l := range res.Lines {
		lines = append(lines, strings.ReplaceAll(l.Text, " ", ""))
		for _, w := range l.Words {
			words[w.Text] = true
		}
	}
	hit = map[string]int{}
	for _, f := range benchFields {
		for _, r := range rows {
			ok := words[r[f]]
			for i := 0; !ok && i < len(lines); i++ {
				ok = strings.Contains(lines[i], r[f])
			}
			if ok {
				hit[f]++
			} else {
				missed = append(missed, r[f])
			}
		}
	}
	return hit, missed
}

// benchPrepared : Windows 엔진에 주기 전의 손질 (사입정보 사진 판독이 하는 것과 비슷하게) — 작은 그림은 키우고 큰 사진은 반으로 줄인다
func benchPrepared(t *testing.T, path, dir string) (string, float64) {
	src, err := loadBGR(path)
	if err != nil {
		t.Fatal(err)
	}
	k := 0.5
	switch m := max(src.w, src.h); {
	case m <= 1500:
		k = 3
	case m <= 2200:
		k = 2
	}
	dw, dh := int(math.Round(float64(src.w)*k)), int(math.Round(float64(src.h)*k))
	r := resizeArea(src, dw, dh)
	img := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for i, j := 0, 0; i < len(r.pix); i, j = i+3, j+4 {
		img.Pix[j], img.Pix[j+1], img.Pix[j+2], img.Pix[j+3] = r.pix[i+2], r.pix[i+1], r.pix[i], 255
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+"-prep.png")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return out, k
}

func TestPaddleOCRBenchmark(t *testing.T) {
	dir := t.TempDir()
	sample := filepath.Join(dir, "ocr_sample.png")
	if err := os.WriteFile(sample, ocrSample, 0o600); err != nil {
		t.Fatal(err)
	}
	var truth struct {
		Rows []benchRow `json:"rows"`
	}
	b, err := os.ReadFile("testdata/ocrbench/truth.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &truth); err != nil || len(truth.Rows) == 0 {
		t.Fatalf("truth.json: %v", err)
	}
	images := []struct {
		path string
		rows []benchRow
	}{
		{sample, benchSampleRows()},
		{"testdata/ocrbench/table.png", truth.Rows},
		{"testdata/ocrbench/small.png", truth.Rows},
		{"testdata/ocrbench/color.png", truth.Rows},
		{"testdata/ocrbench/photo.jpg", truth.Rows},
	}
	type engine struct {
		name string
		run  func(context.Context, []string) ([]OCRResult, error)
		prep bool // 손질한 그림으로
	}
	engines := []engine{
		{"paddle", PaddleOCR, false},
		{"windows", OCR, false},
		{"windows+손질", OCR, true},
	}
	total := map[string]map[string]int{}
	totalMs := map[string]int64{}
	failed := map[string][]string{} // 엔진별: 읽지 못한 그림과 이유 (그 그림은 0칸으로 친다)
	paddleSample := ""              // PaddleOCR 이 점검 그림을 못 읽은 이유
	rowsTotal := 0
	for _, im := range images {
		rowsTotal += len(im.rows)
	}
	for _, e := range engines {
		total[e.name] = map[string]int{}
		for _, im := range images {
			path, k := im.path, 1.0
			if e.prep {
				path, k = benchPrepared(t, im.path, dir)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			start := time.Now()
			res, err := e.run(ctx, []string{path})
			ms := time.Since(start).Milliseconds()
			cancel()
			if err == nil && len(res) == 0 {
				err = errors.New("결과 없음")
			}
			if err != nil {
				failed[e.name] = append(failed[e.name], filepath.Base(im.path)+": "+err.Error())
				if e.name == "paddle" && im.path == sample {
					paddleSample = err.Error()
				}
				if len(failed[e.name]) == 1 {
					t.Logf("%-15s %-13s 읽지 못함 (%dms): %v", filepath.Base(im.path), e.name, ms, err)
				}
				continue
			}
			hit, missed := benchScore(res[0], im.rows)
			var sb strings.Builder
			for _, f := range benchFields {
				fmt.Fprintf(&sb, " %s %d/%d", f, hit[f], len(im.rows))
				total[e.name][f] += hit[f]
			}
			totalMs[e.name] += ms
			note := ""
			if res[0].Warn != "" {
				note = " (" + res[0].Warn + ")"
			}
			t.Logf("%-15s %-13s%s  %5dms  글줄 %d  배율 %.1f  엔진 %s %s%s  놓침 %v",
				filepath.Base(im.path), e.name, sb.String(), ms, len(res[0].Lines), k, res[0].Engine, res[0].Runtime, note, missed)
			if out := os.Getenv(benchOutEnv); out != "" {
				j, _ := json.MarshalIndent(res[0], "", " ")
				name := strings.NewReplacer("+", "-", ".", "_").Replace(e.name + "-" + filepath.Base(im.path))
				if err := os.WriteFile(filepath.Join(out, name+".json"), j, 0o644); err != nil {
					t.Log(err)
				}
			}
			if e.name == "paddle" && im.path == sample && hit["lot"] == 0 {
				paddleSample = "점검 그림의 제조번호를 하나도 읽지 못함"
			}
			if e.name == "paddle" && os.Getenv(paddleStrictEnv) == "1" && len(missed) > 0 {
				t.Errorf("%s: PaddleOCR 이 놓친 칸 %v", filepath.Base(im.path), missed)
			}
		}
	}
	t.Log("── 합계 (칸별 맞힌 수 / 전체, 걸린 시간) ──")
	for _, e := range engines {
		if len(failed[e.name]) == len(images) {
			t.Logf("%-13s 쓸 수 없음: %s", e.name, failed[e.name][0])
			continue
		}
		var sb strings.Builder
		all := 0
		for _, f := range benchFields {
			fmt.Fprintf(&sb, " %s %d/%d", f, total[e.name][f], rowsTotal)
			all += total[e.name][f]
		}
		t.Logf("%-13s%s  전체 %.1f%%  %dms  읽지 못한 그림 %d %v", e.name, sb.String(), 100*float64(all)/float64(rowsTotal*len(benchFields)), totalMs[e.name], len(failed[e.name]), failed[e.name])
	}
	if paddleSample != "" && os.Getenv(paddleRequireEnv) == "1" {
		t.Fatalf("PaddleOCR 이 점검 그림을 읽어야 함: %s", paddleSample)
	}
}
