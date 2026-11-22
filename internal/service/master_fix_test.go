package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"dpr/internal/crawler"
)

// 같은 제품코드가 적용일별로 여러 번 오면 오늘 적용 중인 가격을 쓴다
func TestMasterFromDgamtPicksCurrent(t *testing.T) {
	items := []crawler.DgamtItem{
		{MdsCd: "600000010", ItmNm: "가나정", NomNm: "1", Unit: "정", MxCprc: "1,000", AdtStaDd: "20260101"},
		{MdsCd: "600000010", ItmNm: "가나정", NomNm: "1", Unit: "정", MxCprc: "900", AdtStaDd: "20261101"},
		{MdsCd: "600000010", ItmNm: "가나정", NomNm: "1", Unit: "정", MxCprc: "800", AdtStaDd: "20270101"}, // 아직 시행 전
		{MdsCd: "600000020", ItmNm: "다라정", NomNm: "28", Unit: "정/팩", MxCprc: "6000", AdtStaDd: "20270201"},
		{MdsCd: "코드아님", ItmNm: "x"},
	}
	dm := masterFromDgamt(items, "2026-11-18")
	recs, listDate := dm.Recs, dm.ListDate
	if len(recs) != 2 || recs[0].Price != 900 || recs[1].Price != 6000 || listDate != "2026-11-01" {
		t.Fatalf("%+v %s", recs, listDate)
	}
}

func fakeMasterAPI(t *testing.T, total int, price func(i int) int) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("ServiceKey") != "TESTKEY-0123456789-abcdefghij" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE_KEY_IS_NOT_REGISTERED_ERROR</errMsg><returnReasonCode>30</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`))
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("pageNo"))
		rows, _ := strconv.Atoi(r.URL.Query().Get("numOfRows"))
		total := total
		if r.URL.Query().Get("itmNm") != "%" { // 실제 포털처럼: 검색 조건이 없으면 정상 응답에 0건
			total = 0
		}
		var b strings.Builder
		b.WriteString(`<response><header><resultCode>00</resultCode></header><body><items>`)
		for i := (page - 1) * rows; i < page*rows && i < total; i++ {
			fmt.Fprintf(&b, `<item><mdsCd>6%08d</mdsCd><itmNm>약%d정</itmNm><nomNm>28</nomNm><unit>정/팩</unit><mxCprc>%d</mxCprc><adtStaDd>20261101</adtStaDd></item>`, i, i, price(i))
		}
		fmt.Fprintf(&b, `</items><totalCount>%d</totalCount></body></response>`, total)
		_, _ = w.Write([]byte(b.String()))
	}))
	return srv, &calls
}

// 인증키가 있으면 약제급여목록 전체를 받아 바꾼다 — 한 달에 한 번, 같은 내용이면 바꾸지 않음, 실패 알림은 장애마다 한 번
func TestCheckDrugMaster(t *testing.T) {
	priceBase := 100
	srv, calls := fakeMasterAPI(t, 1500, func(i int) int { return priceBase + i })
	defer srv.Close()
	old := crawler.DgamtURL
	crawler.DgamtURL = srv.URL + "/getDgamtList"
	defer func() { crawler.DgamtURL = old }()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if res, _ := s.CheckDrugMaster(ctx, false); !res.Skipped || !strings.Contains(res.Message, "인증키") {
		t.Fatalf("인증키 없으면 건너뜀: %+v", res)
	}
	// 잘못된 키 → 실패, 알림 한 번 (다시 시도해도 한 번)
	_ = s.DB.SetSetting(SetDataGoKey, "WRONG-KEY-xxxxxxxxxx")
	for i := 0; i < 2; i++ {
		if _, err := s.CheckDrugMaster(ctx, true); err == nil {
			t.Fatal("잘못된 키는 실패")
		}
	}
	var notes int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '약제급여목록을 받지 못했습니다'").Scan(&notes)
	if notes != 1 {
		t.Fatalf("실패 알림 %d건", notes)
	}
	if err := s.SaveSettings(SettingsInput{CrawlTimes: "08:30", CrawlInclude: "약제", UpdateRepo: DefaultUpdateRepo, MasterAuto: true, DataGoKey: "TESTKEY-0123456789-abcdefghij"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.CheckDrugMaster(ctx, false)
	if err != nil || !res.Updated || res.Count != 1500 || res.ListDate != "2026-11-01" {
		t.Fatalf("받기: %+v %v", res, err)
	}
	var n int
	var unit string
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM drug_master").Scan(&n)
	_ = s.DB.QueryRow("SELECT unit FROM drug_master WHERE code = '600000010'").Scan(&unit)
	if n != 1500 || unit != "정/팩" {
		t.Fatalf("급여목록 %d품목, 단위 %q", n, unit)
	}
	before := *calls
	if res, _ := s.CheckDrugMaster(ctx, false); !res.Skipped {
		t.Fatalf("이번 달에 받았으면 건너뜀: %+v", res)
	}
	if *calls != before {
		t.Fatal("건너뛸 때는 요청하지 않음")
	}
	if res, err := s.CheckDrugMaster(ctx, true); err != nil || res.Updated {
		t.Fatalf("내용이 같으면 바꾸지 않음: %+v %v", res, err)
	}
	priceBase = 200 // 상한가가 바뀜
	if res, err := s.CheckDrugMaster(ctx, true); err != nil || !res.Updated {
		t.Fatalf("바뀌면 다시 받음: %+v %v", res, err)
	}
	if st := s.GetSettings(); !st.HasDataGoKey || st.MasterCount != 1500 || !strings.Contains(st.MasterSource, "공공데이터포털") {
		t.Fatalf("설정 화면 정보: %+v", st)
	}
	// 오류 보고에서 인증키를 가린다
	if got := s.sanitize("level=WARN msg=x url=\"https://apis.data.go.kr/x?ServiceKey=TESTKEY-0123456789-abcdefghij&pageNo=1\" raw=TESTKEY-0123456789-abcdefghij"); strings.Contains(got, "TESTKEY") {
		t.Fatalf("인증키가 보고서에 남음: %s", got)
	}
}

// 인증키 확인: 바로 쓸 수 있음 / 아직 등록 전 / 너무 짧음
func TestVerifyDataGoKey(t *testing.T) {
	srv, _ := fakeMasterAPI(t, 21500, func(i int) int { return 100 })
	defer srv.Close()
	old := crawler.DgamtURL
	crawler.DgamtURL = srv.URL + "/getDgamtList"
	defer func() { crawler.DgamtURL = old }()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if k := s.VerifyDataGoKey(ctx, "TESTKEY-0123456789-abcdefghij"); !k.OK || k.Total != 21500 {
		t.Fatalf("정상 키: %+v", k)
	}
	if k := s.VerifyDataGoKey(ctx, "NOT-REGISTERED-KEY-0123456789"); k.OK || !k.Pending {
		t.Fatalf("등록 전 키: %+v", k)
	}
	if k := s.VerifyDataGoKey(ctx, "short"); k.OK || k.Pending || k.Message == "" {
		t.Fatalf("짧은 키: %+v", k)
	}
	// 키는 통했지만 0품목 → 성공으로 보이지 않는다
	empty, _ := fakeMasterAPI(t, 0, func(i int) int { return 100 })
	defer empty.Close()
	crawler.DgamtURL = empty.URL + "/getDgamtList"
	if k := s.VerifyDataGoKey(ctx, "TESTKEY-0123456789-abcdefghij"); k.OK || k.Pending || !strings.Contains(k.Message, "0품목") {
		t.Fatalf("0품목 키 확인: %+v", k)
	}
	crawler.DgamtURL = srv.URL + "/getDgamtList"
	if err := s.SetDataGoKey("TESTKEY-0123456789-abcdefghij"); err != nil || !s.GetSettings().HasDataGoKey {
		t.Fatalf("저장: %v", err)
	}
	if err := s.SetDataGoKey(""); err != nil || s.GetSettings().HasDataGoKey {
		t.Fatalf("지우기: %v", err)
	}
}
