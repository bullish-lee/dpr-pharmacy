package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dpr/internal/crawler"
	"dpr/internal/ingest"
)

// 목록에서 빠진 품목이라도 약국 기록이 있으면 규격·단위를 남긴다 — 빠지면 박스(30정) 사입이 1정으로 바뀌었다
func TestMasterCarriesUsedCodes(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mk := func(skip ...int) []ingest.MasterRec {
		var recs []ingest.MasterRec
	next:
		for i := 0; i < 1500; i++ {
			for _, k := range skip {
				if i == k {
					continue next
				}
			}
			recs = append(recs, ingest.MasterRec{Code: fmt.Sprintf("6%08d", i), Name: fmt.Sprintf("약%d정", i), Spec: "1", Unit: "정", Price: 100})
		}
		return recs
	}
	if _, _, _, err := s.storeDrugMaster("full", "a", "2026-11-01", mk(), 0); err != nil {
		t.Fatal(err)
	}
	// 30정 박스 1개를 3000원에 삼 (= 1정 100원)
	importPurchases(t, s, "p.xlsx", []any{"20261102", "약10정", "600000010", "가나약품", "사입", "박스", 1, 2, 3000, 6000})
	var f0 float64
	var q0 int64
	_ = s.DB.QueryRow("SELECT factor, qty FROM purchase_line WHERE product_code = '600000010'").Scan(&f0, &q0)
	if f0 <= 1 {
		t.Fatalf("박스 배율을 못 찾음: factor=%v qty=%d", f0, q0)
	}
	// 600000010(약국 기록 있음)·600000011(없음)이 빠진 새 목록
	_, n, carried, err := s.storeDrugMaster("next", "b", "2026-12-01", mk(10, 11), 0)
	if err != nil || n != 1498 || carried != 1 {
		t.Fatalf("n=%d carried=%d err=%v", n, carried, err)
	}
	var f1 float64
	var q1 int64
	_ = s.DB.QueryRow("SELECT factor, qty FROM purchase_line WHERE product_code = '600000010'").Scan(&f1, &q1)
	if f1 != f0 || q1 != q0 {
		t.Fatalf("목록에서 빠졌다고 수량이 바뀜: factor %v→%v qty %d→%d", f0, f1, q0, q1)
	}
	var kept, gone int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM drug_master WHERE code = '600000010' AND in_list = 0 AND list_date = '2026-11-01'").Scan(&kept)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM drug_master WHERE code = '600000011'").Scan(&gone)
	if kept != 1 || gone != 0 {
		t.Fatalf("약국 품목은 남기고(in_list=0) 나머지는 지움: kept=%d gone=%d", kept, gone)
	}
	if st := s.GetSettings(); st.MasterCount != 1498 || st.MasterDate != "2026-12-01" {
		t.Fatalf("지금 목록 수는 남긴 행을 빼고: %d %s", st.MasterCount, st.MasterDate)
	}
	hits, err := s.LookupDrugs("600000010")
	if err != nil || len(hits) != 1 || hits[0].InMaster || hits[0].Name != "약10정" {
		t.Fatalf("조회: 정보는 보여 주되 '지금 급여목록에 없음': %+v %v", hits, err)
	}
	// 다음 목록에 다시 들어오면 지금 목록으로
	if _, _, carried, err := s.storeDrugMaster("back", "c", "2027-01-01", mk(), 0); err != nil || carried != 0 {
		t.Fatalf("다시 들어옴: carried=%d err=%v", carried, err)
	}
	if hits, _ := s.LookupDrugs("600000010"); len(hits) != 1 || !hits[0].InMaster {
		t.Fatalf("다시 들어오면 지금 목록: %+v", hits)
	}
	// 급여목록 가져오기 기록은 지울 수 없다 (지워도 행은 남고 '지웠다'고만 보였다)
	var bid int64
	_ = s.DB.QueryRow("SELECT id FROM import_batch WHERE kind = 'drug_master' ORDER BY id DESC LIMIT 1").Scan(&bid)
	if err := s.DeleteImport(bid); err == nil {
		t.Fatal("급여목록 가져오기 기록을 지움")
	}
	if st := s.GetSettings(); st.MasterSource != "back" {
		t.Fatalf("지금 목록의 출처: %q", st.MasterSource)
	}
	// 가져오기 기록과 연결이 끊겨도(batch_id NULL) 지금 목록 판단(in_list)은 그대로
	_, _ = s.DB.Exec("UPDATE drug_master SET batch_id = NULL")
	if st := s.GetSettings(); st.MasterCount != 1500 {
		t.Fatalf("지금 목록 %d", st.MasterCount)
	}
}

type fakeList struct {
	total      int
	eff        string // adtStaDd (YYYYMMDD)
	spec, unit string
}

func fakeMasterSrv(t *testing.T, cur *atomic.Pointer[fakeList]) *int32 {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		l := cur.Load()
		page, _ := strconv.Atoi(r.URL.Query().Get("pageNo"))
		rows, _ := strconv.Atoi(r.URL.Query().Get("numOfRows"))
		var b strings.Builder
		b.WriteString(`<response><header><resultCode>00</resultCode></header><body><items>`)
		for i := (page - 1) * rows; i < page*rows && i < l.total; i++ {
			fmt.Fprintf(&b, `<item><mdsCd>6%08d</mdsCd><itmNm>약%d정</itmNm><nomNm>%s</nomNm><unit>%s</unit><mxCprc>%d</mxCprc><adtStaDd>%s</adtStaDd></item>`, i, i, l.spec, l.unit, 100+i, l.eff)
		}
		fmt.Fprintf(&b, `</items><totalCount>%d</totalCount></body></response>`, l.total)
		_, _ = w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)
	old := crawler.DgamtURL
	crawler.DgamtURL = srv.URL + "/getDgamtList"
	t.Cleanup(func() { crawler.DgamtURL = old })
	return &calls
}

func manualList(n int, spec, unit string) []ingest.MasterRec {
	recs := make([]ingest.MasterRec, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, ingest.MasterRec{Code: fmt.Sprintf("6%08d", i), Name: fmt.Sprintf("약%d정", i), Spec: spec, Unit: unit, Price: float64(50 + i)})
	}
	return recs
}

// 자동으로 받은 목록이 지금 목록보다 옛것·크게 적음·고시 단위가 많이 다름 → 바꾸지 않는다
func TestMasterAutoGuards(t *testing.T) {
	now := time.Now()
	thisMonth := now.Format("200601") + "01"
	nextMonth := now.AddDate(0, 1, -now.Day()+1).Format("2006-01-02")
	var cur atomic.Pointer[fakeList]
	cur.Store(&fakeList{total: 1500, eff: thisMonth, spec: "1", unit: "정"})
	fakeMasterSrv(t, &cur)
	ctx := context.Background()
	setup := func(t *testing.T, listDate string, recs []ingest.MasterRec) *Service {
		s, err := NewTemp(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if _, _, _, err := s.storeDrugMaster("약제급여목록표.xlsx", "filesha", listDate, recs, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.SetDataGoKey("TESTKEY-0123456789-abcdefghij"); err != nil {
			t.Fatal(err)
		}
		return s
	}
	maxDate := func(s *Service) (d string) {
		_ = s.DB.QueryRow("SELECT MAX(list_date) FROM drug_master WHERE in_list = 1").Scan(&d)
		return
	}
	notes := func(s *Service) (n int) {
		_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '약제급여목록을 받지 못했습니다'").Scan(&n)
		return
	}

	t.Run("직접 올린 더 새 목록", func(t *testing.T) {
		s := setup(t, nextMonth, manualList(1500, "1", "정"))
		res, err := s.CheckDrugMaster(ctx, false)
		if err != nil || res.Updated || !strings.Contains(res.Message, "더 새것") {
			t.Fatalf("%+v %v", res, err)
		}
		if maxDate(s) != nextMonth || notes(s) != 0 {
			t.Fatalf("목록 %s, 실패 알림 %d", maxDate(s), notes(s))
		}
	})
	t.Run("크게 적은 목록", func(t *testing.T) {
		s := setup(t, "2020-01-01", manualList(1500, "1", "정"))
		cur.Store(&fakeList{total: 1200, eff: thisMonth, spec: "1", unit: "정"})
		defer cur.Store(&fakeList{total: 1500, eff: thisMonth, spec: "1", unit: "정"})
		_, err := s.CheckDrugMaster(ctx, true)
		if !errors.Is(err, errMasterRefused) || !strings.Contains(err.Error(), "크게 적습니다") {
			t.Fatalf("err=%v", err)
		}
		if maxDate(s) != "2020-01-01" || notes(s) != 1 {
			t.Fatalf("목록 %s, 실패 알림 %d", maxDate(s), notes(s))
		}
	})
	t.Run("고시 단위가 많이 다름", func(t *testing.T) {
		s := setup(t, "2020-01-01", manualList(1500, "1", "정"))
		cur.Store(&fakeList{total: 1500, eff: thisMonth, spec: "28", unit: "정/팩"})
		defer cur.Store(&fakeList{total: 1500, eff: thisMonth, spec: "1", unit: "정"})
		_, err := s.CheckDrugMaster(ctx, true)
		if !errors.Is(err, errMasterRefused) || !strings.Contains(err.Error(), "고시 단위") {
			t.Fatalf("err=%v", err)
		}
		var unit string
		_ = s.DB.QueryRow("SELECT unit FROM drug_master WHERE code = '600000010'").Scan(&unit)
		if unit != "정" {
			t.Fatalf("바꾸면 안 되는데 단위 %q", unit)
		}
	})
	t.Run("같은 목록이면 받아들임", func(t *testing.T) {
		s := setup(t, "2020-01-01", manualList(1500, "1", "정"))
		res, err := s.CheckDrugMaster(ctx, true)
		if err != nil || !res.Updated {
			t.Fatalf("%+v %v", res, err)
		}
	})
}

// 이번 달 목록이 아직 안 나왔으면(지난달 적용일뿐) 그달을 끝내지 않고 하루 한 번 다시 본다
func TestMasterMonthlyWaitsForThisMonth(t *testing.T) {
	now := time.Now()
	lastMonth := now.AddDate(0, 0, -now.Day()).Format("200601") + "01"
	thisMonth := now.Format("200601") + "01"
	var cur atomic.Pointer[fakeList]
	cur.Store(&fakeList{total: 1500, eff: lastMonth, spec: "1", unit: "정"})
	calls := fakeMasterSrv(t, &cur)
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.SetDataGoKey("TESTKEY-0123456789-abcdefghij")
	ctx := context.Background()
	res, err := s.CheckDrugMaster(ctx, false)
	if err != nil || !res.Updated {
		t.Fatalf("지난달 목록이라도 처음엔 받음: %+v %v", res, err)
	}
	if s.DB.GetSetting(setMasterMonth, "") != "" {
		t.Fatal("지난달 목록으로 이번 달을 끝냄")
	}
	before := atomic.LoadInt32(calls)
	if res, _ := s.CheckDrugMaster(ctx, false); !res.Skipped || atomic.LoadInt32(calls) != before {
		t.Fatalf("같은 날에는 다시 받지 않음: %+v", res)
	}
	// 다음 날 — 이번 달 목록이 나옴
	_ = s.DB.SetSetting(setMasterCheckedOn, now.AddDate(0, 0, -1).Format("2006-01-02"))
	cur.Store(&fakeList{total: 1500, eff: thisMonth, spec: "1", unit: "정"})
	res, err = s.CheckDrugMaster(ctx, false)
	if err != nil || !res.Updated || res.ListDate != now.Format("2006-01")+"-01" {
		t.Fatalf("이번 달 목록: %+v %v", res, err)
	}
	if s.DB.GetSetting(setMasterMonth, "") != now.Format("2006-01") {
		t.Fatal("이번 달 목록을 받았으면 그달은 끝")
	}
	_ = s.DB.SetSetting(setMasterCheckedOn, "")
	if res, _ := s.CheckDrugMaster(ctx, false); !res.Skipped || !strings.Contains(res.Message, "이번 달") {
		t.Fatalf("그달에는 다시 받지 않음: %+v", res)
	}
}
