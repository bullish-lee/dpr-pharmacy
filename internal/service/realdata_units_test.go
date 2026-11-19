package service

// 실데이터 폴더(환경변수 DPR_REALDATA)가 있을 때만 실행. 기대값은 코드에 두지 않고 결과만 기록한다.

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 폴더에 master.xlsx(심평원 약제급여목록표), gtin.csv(약가마스터_의약품표준코드)가 있으면 함께 가져온다.
func realdataImports(dir string, masterFirst bool) []struct{ kind, file string } {
	base := []struct{ kind, file string }{{"purchase", "purchase.xlsx"}, {"dispense", "dispense.xlsx"}, {"initial_stock", "initial.xlsx"}, {"notice", "notice.xlsx"}}
	var extra []struct{ kind, file string }
	for _, f := range []struct{ kind, file string }{{"drug_master", "master.xlsx"}, {"gtin", "gtin.csv"}} {
		if _, err := os.Stat(filepath.Join(dir, f.file)); err == nil {
			extra = append(extra, f)
		}
	}
	if masterFirst {
		return append(extra, base...)
	}
	return append(base, extra...)
}

func TestRealData_Units(t *testing.T) {
	dir := os.Getenv("DPR_REALDATA")
	if dir == "" {
		t.Skip("DPR_REALDATA 미설정")
	}
	run := func(masterFirst bool) (*Service, map[string]int64) {
		s, err := NewTemp(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range realdataImports(dir, masterFirst) {
			res := mustImport(t, s, ImportRequest{Kind: f.kind, Path: filepath.Join(dir, f.file)})
			t.Logf("[%s] %s (%dms)", f.kind, res.Message, res.DurationMs)
		}
		inv, err := s.Inventory("", false)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int64{}
		for _, r := range inv.Rows {
			out[r.Code] = int64(r.Qty)
		}
		t.Logf("재고 품목 %d, 음수 추정재고 %d", inv.Products, inv.Negative)
		return s, out
	}
	a, stockA := run(false)
	defer a.Close()
	b, stockB := run(true)
	defer b.Close()
	lineMap := func(s *Service) map[string]string {
		out := map[string]string{}
		rows, err := s.DB.Query("SELECT sheet, row, product_code, name FROM initial_line")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var sh, code, name string
			var row int
			_ = rows.Scan(&sh, &row, &code, &name)
			out[sh+"#"+strconv.Itoa(row)] = code + " " + name
		}
		return out
	}
	la, lb := lineMap(a), lineMap(b)
	ld := 0
	for k, v := range la {
		if lb[k] != v {
			ld++
			if ld <= 12 {
				t.Logf("초기재고 매칭 차이 %s: [나중] %s | [먼저] %s", k, v, lb[k])
			}
		}
	}
	for k, v := range lb {
		if _, ok := la[k]; !ok {
			ld++
			if ld <= 12 {
				t.Logf("초기재고 매칭 차이 %s: [나중] (미확인) | [먼저] %s", k, v)
			}
		}
	}
	ua, _ := a.ListUnresolved()
	ub, _ := b.ListUnresolved()
	// DPR_DEBUG_NAMES=이름1,이름2 : 이름에 이 글자가 든 미확인 행의 후보를 기록한다
	if names := os.Getenv("DPR_DEBUG_NAMES"); names != "" {
		keys := strings.Split(names, ",")
		for _, side := range []struct {
			label string
			rows  []UnresolvedRow
		}{{"나중", ua}, {"먼저", ub}} {
			for _, u := range side.rows {
				for _, k := range keys {
					if k = strings.TrimSpace(k); k != "" && strings.Contains(u.Name, k) {
						t.Logf("[%s 미확인] %s %.0f원 → %s", side.label, u.Name, u.UnitPrice, toJSON(u.Candidates))
					}
				}
			}
		}
	}
	t.Logf("미확인 행: 급여목록 나중 %d · 먼저 %d, 매칭이 다른 행 %d", len(ua), len(ub), ld)
	diff := 0
	for c, q := range stockA {
		if stockB[c] != q {
			diff++
			if diff <= 5 {
				t.Logf("순서에 따라 다름: %s %d vs %d", c, q, stockB[c])
			}
		}
	}
	switch {
	case diff > 5:
		t.Errorf("가져오기 순서에 따라 재고가 다른 품목 %d개", diff)
	case diff > 0: // 이름 매칭 경계 사례 (같은 사전이어도 단가 환산 순서에 따라 갈리는 행) — 기록만
		t.Logf("주의: 가져오기 순서에 따라 재고가 다른 품목 %d개 (허용 5개 이하)", diff)
	}
	rows, err := a.DB.Query("SELECT unit_status, COUNT(*) FROM product GROUP BY unit_status ORDER BY 2 DESC")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var st string
		var n int
		_ = rows.Scan(&st, &n)
		t.Logf("단위 상태 %-12s %d", st, n)
	}
	rows.Close()
	rows, err = a.DB.Query(`SELECT code, name, unit_status, base_label, unit_note FROM product WHERE unit_status IN ('auto_unit','auto_pack','check') ORDER BY unit_status, code`)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for rows.Next() {
		var c, n, st, lb, note string
		_ = rows.Scan(&c, &n, &st, &lb, &note)
		if len([]rune(n)) > 22 {
			n = string([]rune(n)[:22])
		}
		lines = append(lines, st+" "+c+" "+n+" ["+lb+"] "+note+" → 재고 "+fmtQty(stockA[c]))
	}
	rows.Close()
	sort.Strings(lines)
	for _, l := range lines {
		t.Log(l)
	}
}

func fmtQty(q int64) string { return trimF(float64(q) / 1000) }
