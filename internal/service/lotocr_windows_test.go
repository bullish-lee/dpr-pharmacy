//go:build windows

package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"dpr/internal/platform"
)

// lotEngineModes : 실제 시험을 세 가지로 돌린다 — Windows 글자 인식만 · 두 엔진 견주기 · Windows 글자 인식이 안 될 때(PaddleOCR 만)
var lotEngineModes = []struct {
	name          string
	paddle, noWin bool
}{{"windows", false, false}, {"두엔진", true, false}, {"paddle만", true, true}}

// useEngines : 이 시험에서 쓸 엔진을 정한다. PaddleOCR 을 쓸 수 없는 PC 면 건너뛴다 (CI 는 DPR_PADDLE_REQUIRE=1 로 실패 처리).
func useEngines(t *testing.T, paddle, noWin bool) {
	t.Helper()
	oldP, oldW := paddleRunner, ocrRunner
	t.Cleanup(func() { paddleRunner, ocrRunner = oldP, oldW })
	if paddle {
		if err := paddleAvailable(t); err != nil {
			t.Fatal(err)
		}
		paddleRunner = platform.PaddleOCR
	}
	if noWin {
		ocrRunner = func(ctx context.Context, paths []string) ([]platform.OCRResult, error) {
			return nil, platform.ErrOCRUnavailable
		}
	}
}

// 실제 Windows 글자 인식으로 가상 사입현황 표 그림을 읽어 사입 근거 줄을 만든다 (PATH 를 비운 채로).
// 한국어 글자 인식이 있는 PC(DPR_OCR_REQUIRE_KO=1)에서만 끝까지 확인한다.
func TestReadLotPhotoRealOCR(t *testing.T) {
	if os.Getenv("DPR_OCR_REQUIRE_KO") != "1" {
		t.Skip("한국어 글자 인식이 있는 Windows 에서만 (DPR_OCR_REQUIRE_KO=1)")
	}
	t.Setenv("PATH", "")
	for _, mode := range lotEngineModes {
		t.Run(mode.name, func(t *testing.T) {
			if mode.paddle && os.Getenv("DPR_PADDLE_REQUIRE") != "1" {
				if err := paddleAvailable(t); err != nil {
					t.Skip(err)
				}
			}
			useEngines(t, mode.paddle, mode.noWin)
			s, err := NewTemp(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			seedSample(t, s)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			start := time.Now()
			res, err := s.ReadLotPhoto(ctx, LotPhotoRequest{Code: "900000010", ImageBase64: base64.StdEncoding.EncodeToString(platform.OCRSample())})
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, r := range res.Rows {
				got = append(got, r.Date+" "+r.SupplierName+" "+r.Lot+" "+r.Expiry+fmt.Sprintf(" 확인필요=%v 후보=%v", r.Unsure, r.Alt))
			}
			t.Logf("ms=%d note=%q\n%s", time.Since(start).Milliseconds(), res.Note, strings.Join(got, "\n"))
			want := map[string]string{"LOT-1001": "가람도매", "LOT-1002": "나래도매", "LOT-1003": "다솜도매"}
			found := 0
			for _, r := range res.Rows {
				if sup, ok := want[r.Lot]; ok {
					found++
					if r.SupplierName != sup {
						t.Errorf("%s: 도매 %q, 기대 %q", r.Lot, r.SupplierName, sup)
					}
				}
			}
			if found != 3 {
				t.Fatalf("제조번호 3개 중 %d개 읽음", found)
			}
		})
	}
}

// 2026-11-26 신고(머리 없이 본문만 자른 화면 캡처 → '0줄 읽음')를 실제 Windows 글자 인식으로 확인한다.
// 그림은 가상 자료로 그린 것 (testdata/, 실제 약국 자료 아님): 이팜 사입현황 본문 줄만, 700px 폭, 12px 글꼴.
//   - lot_headerless.png       : 사입 6줄 + 반품(음수) 2줄, 숫자뿐인 제조번호, 8자리 유효기간, 노란 강조 줄 하나
//   - lot_headerless_blank.png : 사입 6줄 중 2줄은 유효기간·제조번호가 빈 칸, 도매 이름 뒤에 (주)
//
// 줄 수·반품 수(표 구조)는 정확히 맞아야 하고, 칸 값은 글자 인식이 한 줄쯤 틀리는 것까지 봐준다.
func TestLotOCRHeaderlessRealOCR(t *testing.T) {
	if os.Getenv("DPR_OCR_REQUIRE_KO") != "1" {
		t.Skip("한국어 글자 인식이 있는 Windows 에서만 (DPR_OCR_REQUIRE_KO=1)")
	}
	t.Setenv("PATH", "")
	type want struct {
		date, sup, expiry, lot string
		qty                    float64
	}
	cases := []struct {
		file    string
		skipped int
		rows    []want
	}{
		{"lot_headerless.png", 2, []want{
			{"2026-10-16", "가람도매", "2029-05-31", "0031201", 120},
			{"2026-10-05", "다솜도매", "2029-05-31", "0031201", 60},
			{"2026-09-01", "나래도매", "2028-06-30", "0052209", 150},
			{"2026-05-14", "나래도매", "2028-06-30", "0052209", 30},
			{"2026-03-25", "가람도매", "2028-03-31", "0045107", 180},
			{"2026-02-10", "다솜도매", "2028-03-31", "0045107", 60},
		}},
		{"lot_headerless_blank.png", 0, []want{
			{"2026-08-19", "가람도매", "2030-01-31", "7301954", 450},
			{"2026-08-12", "가람도매", "2030-01-31", "7301954", 240},
			{"2026-07-29", "나래도매", "", "", 240},
			{"2026-07-29", "나래도매", "2029-10-31", "7286610", 120},
			{"2026-07-08", "가람도매", "", "", 300},
			{"2026-06-30", "나래도매", "2029-10-31", "7286610", 120},
		}},
	}
	for _, mode := range lotEngineModes {
		for _, c := range cases {
			t.Run(mode.name+"/"+c.file, func(t *testing.T) {
				if mode.paddle && os.Getenv("DPR_PADDLE_REQUIRE") != "1" {
					if err := paddleAvailable(t); err != nil {
						t.Skip(err)
					}
				}
				useEngines(t, mode.paddle, mode.noWin)
				raw, err := os.ReadFile("testdata/" + c.file)
				if err != nil {
					t.Fatal(err)
				}
				s := lotTestService(t) // 거래처만 등록 (사입현황이 없어 DB 맞추기는 하지 않는다 → 읽은 값 그대로 확인)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
				defer cancel()
				res, err := s.ReadLotPhoto(ctx, LotPhotoRequest{Code: "900000010", ImageBase64: base64.StdEncoding.EncodeToString(raw)})
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, r := range res.Rows {
					got = append(got, lotRowText(r)+" 확인필요="+strings.Join(r.Unsure, ",")+fmt.Sprintf(" 후보=%v", r.Alt))
				}
				t.Logf("mode=%s skipped=%d note=%q\n%s\n--- 인식한 글자\n%s", res.Mode, res.Skipped, res.Note, strings.Join(got, "\n"), res.RawText)
				if len(res.Rows) != len(c.rows) || res.Skipped != c.skipped {
					t.Fatalf("줄 수 %d (기대 %d), 뺀 줄 %d (기대 %d)", len(res.Rows), len(c.rows), res.Skipped, c.skipped)
				}
				okLot, okDate, okSup, okQty := 0, 0, 0, 0
				for i, w := range c.rows {
					r := res.Rows[i]
					if w.lot == "" && (r.Lot != "" || r.Expiry != "") {
						t.Errorf("%d번째 줄: 빈 칸에 값이 들어감 (유효기간 %q, 제조번호 %q)", i+1, r.Expiry, r.Lot)
					}
					if r.Lot == w.lot && r.Expiry == w.expiry {
						okLot++
					}
					if r.Date == w.date {
						okDate++
					}
					if r.SupplierName == w.sup {
						okSup++
					}
					if r.Qty == w.qty {
						okQty++
					}
				}
				n := len(c.rows)
				if okLot < n-1 || okDate < n-1 || okQty < n-1 || okSup < n-2 {
					t.Fatalf("맞은 줄: 유효기간·제조번호 %d, 날짜 %d, 수량 %d, 도매 %d (전체 %d줄)", okLot, okDate, okQty, okSup, n)
				}
			})
		}
	}
}
