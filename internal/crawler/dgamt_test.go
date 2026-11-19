package crawler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeDgamt(t *testing.T, total int, wantKey string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ServiceKey") != wantKey {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE_KEY_IS_NOT_REGISTERED_ERROR</errMsg><returnAuthMsg>SERVICE_KEY_IS_NOT_REGISTERED_ERROR</returnAuthMsg><returnReasonCode>30</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`))
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("pageNo"))
		rows, _ := strconv.Atoi(r.URL.Query().Get("numOfRows"))
		var b strings.Builder
		fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?><response><header><resultCode>00</resultCode><resultMsg>NORMAL SERVICE.</resultMsg></header><body><items>`)
		for i := (page - 1) * rows; i < page*rows && i < total; i++ {
			fmt.Fprintf(&b, `<item><mdsCd>6%08d</mdsCd><itmNm>약%d정</itmNm><mnfEntpNm>가나제약</mnfEntpNm><nomNm>1</nomNm><unit>정</unit><mxCprc>%d</mxCprc><adtStaDd>20261101</adtStaDd></item>`, i, i, 100+i)
		}
		fmt.Fprintf(&b, `</items><numOfRows>%d</numOfRows><pageNo>%d</pageNo><totalCount>%d</totalCount></body></response>`, rows, page, total)
		_, _ = w.Write([]byte(b.String()))
	}))
}

func TestFetchDgamtAll(t *testing.T) {
	const key = "abc+def/ghi==" // 포털의 '디코딩' 키 (+ / = 포함)
	srv := fakeDgamt(t, 2500, key)
	defer srv.Close()
	old := DgamtURL
	DgamtURL = srv.URL + "/getDgamtList"
	defer func() { DgamtURL = old }()
	c := New()
	c.Backoff = time.Millisecond
	items, err := c.FetchDgamtAll(context.Background(), key, nil)
	if err != nil || len(items) != 2500 || items[0].MdsCd != "600000000" || items[2499].MxCprc != "2599" {
		t.Fatalf("전체 받기: %d %v", len(items), err)
	}
	// '인코딩' 키(%2B…)를 붙여 넣어도 같은 키로 보낸다
	if items, err := c.FetchDgamtAll(context.Background(), "abc%2Bdef%2Fghi%3D%3D", nil); err != nil || len(items) != 2500 {
		t.Fatalf("인코딩 키: %d %v", len(items), err)
	}
	// 등록되지 않은 키 → 인증키 오류, 오류 문장에 키가 들어가지 않는다
	_, err = c.FetchDgamtAll(context.Background(), "WRONGKEY-1234567890", nil)
	if !errors.Is(err, ErrDgamtKey) || strings.Contains(err.Error(), "WRONGKEY") {
		t.Fatalf("인증키 오류: %v", err)
	}
	// 접속 실패 오류에도 주소(키)를 넣지 않는다
	DgamtURL = "http://127.0.0.1:1/getDgamtList"
	c.Retries = 1
	if _, err := c.FetchDgamtAll(context.Background(), "SECRETKEY-abcdef", nil); err == nil || strings.Contains(err.Error(), "SECRETKEY") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("접속 실패 오류에 주소가 들어감: %v", err)
	}
}
