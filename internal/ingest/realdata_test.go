package ingest

// 실데이터 폴더(환경변수)가 있을 때만 실행. 기대값은 코드에 두지 않는다.

// 실데이터 검증 (커밋하지 않는 로컬 파일 사용).
//
//	DPR_REALDATA=/path/to/dir go test ./internal/ingest -run RealData -v
//
// 폴더 안 파일명: purchase.xlsx, dispense.xlsx, initial.xlsx, notice.xlsx
//
// 기대값은 같은 폴더의 expected.json 에 둔다 (없으면 일반 검증만 한다). 모든 항목은 선택:
//
//	{
//	  "min_rows": {"purchase": 1000, "dispense": 1000, "initial": 100},
//	  "code_qty": {"purchase": {"<약품코드>": 0}, "dispense": {}, "initial": {}},
//	  "notice_kinds": {"delete/delete": 0, "new/new": 0},
//	  "min_notice_changes": 0
//	}

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dpr/internal/domain"
	"dpr/internal/sheet"
)

type realExpected struct {
	MinRows          map[string]int                `json:"min_rows"`
	CodeQty          map[string]map[string]float64 `json:"code_qty"` // 종류 → 약품코드 → 수량 합(낱개)
	NoticeKinds      map[string]int                `json:"notice_kinds"`
	MinNoticeChanges int                           `json:"min_notice_changes"`
}

func realFile(t *testing.T, name string) ([]sheet.Sheet, realExpected) {
	t.Helper()
	dir := os.Getenv("DPR_REALDATA")
	if dir == "" {
		t.Skip("DPR_REALDATA 미설정")
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s 없음", name)
	}
	sh, err := sheet.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var exp realExpected
	if data, err := os.ReadFile(filepath.Join(dir, "expected.json")); err == nil {
		if err := json.Unmarshal(data, &exp); err != nil {
			t.Fatalf("expected.json: %v", err)
		}
	}
	return sh, exp
}

// checkRows 는 행 수(> 0, expected.json 의 최소 행 수)와 품목별 수량 합을 확인한다.
func checkRows(t *testing.T, exp realExpected, kind string, n int, sums map[string]domain.Qty) {
	t.Helper()
	if n == 0 {
		t.Fatalf("%s: 읽은 행이 없음", kind)
	}
	if min := exp.MinRows[kind]; n < min {
		t.Errorf("%s: 행 %d (최소 %d)", kind, n, min)
	}
	for code, want := range exp.CodeQty[kind] {
		if got := sums[code]; got != domain.QtyFromFloat(want) {
			t.Errorf("%s: %s 수량 합 %s (기대 %v)", kind, code, got, want)
		}
	}
}

func TestRealData_Purchase(t *testing.T) {
	sh, exp := realFile(t, "purchase.xlsx")
	tb := DetectTable(sh[0], PurchaseSchema, nil)
	if len(tb.Missing) > 0 {
		t.Fatalf("열 매핑 실패: %v", tb.Missing)
	}
	recs, issues := ParsePurchases(tb)
	t.Logf("사입 %d행, 이슈 %d, 기간 %s~%s", len(recs), len(issues), tb.RangeFrom, tb.RangeTo)
	sums := map[string]domain.Qty{}
	for _, r := range recs {
		if r.Date == "" || r.Code == "" {
			t.Fatalf("행 %d: 일자·약품코드 없음", r.Row)
		}
		sums[r.Code] += r.Qty
	}
	checkRows(t, exp, "purchase", len(recs), sums)
}

func TestRealData_Dispense(t *testing.T) {
	sh, exp := realFile(t, "dispense.xlsx")
	tb := DetectTable(sh[0], DispenseSchema, nil)
	if len(tb.Missing) > 0 {
		t.Fatalf("열 매핑 실패: %v", tb.Missing)
	}
	recs, _ := ParseDispenses(tb)
	sums := map[string]domain.Qty{}
	negative := 0
	for _, r := range recs {
		if r.Qty < 0 {
			negative++
		}
		sums[r.Code] += r.Qty
	}
	t.Logf("조제 %d행, 음수 수량 %d행", len(recs), negative)
	checkRows(t, exp, "dispense", len(recs), sums)
}

func TestRealData_Initial(t *testing.T) {
	sheets, exp := realFile(t, "initial.xlsx")
	total := 0
	sums := map[string]domain.Qty{}
	for _, sh := range sheets {
		tb := DetectTable(sh, StockSchema, nil)
		if len(tb.Missing) > 0 {
			t.Logf("시트 %q 매핑 실패: %v", sh.Name, tb.Missing)
			continue
		}
		recs, _ := ParseStock(tb)
		total += len(recs)
		for _, r := range recs {
			if r.Code != "" {
				sums[r.Code] += r.Qty
			}
		}
		t.Logf("시트 %q: %d행 (매핑 %v)", sh.Name, len(recs), tb.Mapping)
	}
	checkRows(t, exp, "initial", total, sums)
}

func TestRealData_Notice(t *testing.T) {
	sheets, exp := realFile(t, "notice.xlsx")
	res, err := ParseNotice(sheets, "notice.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, c := range res.Changes {
		count[c.Kind+"/"+c.Detail]++
	}
	for _, s := range res.Sheets {
		t.Logf("%s → %s %s~%s %d", s.Sheet, s.Role, s.EffectiveDate, s.GraceUntil, s.Items)
	}
	t.Logf("분류: %v, 경고: %v", count, res.Warnings)
	if len(res.Changes) == 0 {
		t.Fatal("고시 변경 항목이 없음")
	}
	if count["change/unknown"] > 0 {
		t.Errorf("분류하지 못한 변경 %d건", count["change/unknown"])
	}
	for k, want := range exp.NoticeKinds {
		if count[k] != want {
			t.Errorf("%s: %d (기대 %d)", k, count[k], want)
		}
	}
	changes := 0
	for k, v := range count {
		if strings.HasPrefix(k, "change") {
			changes += v
		}
	}
	if changes < exp.MinNoticeChanges {
		t.Errorf("변경 %d건 (최소 %d)", changes, exp.MinNoticeChanges)
	}
}
