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

func withDgamt(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := DgamtURL
	DgamtURL = srv.URL + "/getDgamtList"
	t.Cleanup(func() { DgamtURL = old })
	c := New()
	c.Backoff = time.Millisecond
	c.Retries = 1
	return c
}

// 포털 응답 형식마다 오류 코드가 든 칸이 다르다 — 어느 형식이든 인증키 문제·한도 초과를 알아본다
func TestDgamtErrorEnvelopes(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		key      bool // ErrDgamtKey
		notReady bool // ErrDgamtKeyNotReady ([그래도 저장])
		want     string
	}{
		// 2026-11-18 실제 서버 (인증키 없음·미등록)
		{"real-null", 401, `<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE_KEY_IS_NULL</errMsg><returnAuthMsg>서비스 접근거부</returnAuthMsg><returnReasonCode>20</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`, true, false, "인증키가 없습니다"},
		{"real-unregistered", 403, `<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE_KEY_IS_NOT_REGISTERED_ERROR</errMsg><returnAuthMsg>등록되지 않은 서비스키</returnAuthMsg><returnReasonCode>30</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`, true, true, "등록되지 않았"},
		// 예전 형식: errMsg 는 'SERVICE ERROR', 코드는 returnAuthMsg
		{"classic-unregistered", 200, `<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE ERROR</errMsg><returnAuthMsg>SERVICE_KEY_IS_NOT_REGISTERED_ERROR</returnAuthMsg><returnReasonCode>30</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`, true, true, "등록되지 않았"},
		{"classic-expired", 200, `<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE ERROR</errMsg><returnAuthMsg>DEADLINE_HAS_EXPIRED_ERROR</returnAuthMsg><returnReasonCode>31</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`, true, false, "기간이 끝났"},
		{"classic-denied", 200, `<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE ERROR</errMsg><returnAuthMsg>SERVICE_ACCESS_DENIED_ERROR</returnAuthMsg><returnReasonCode>20</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`, true, true, "활용신청"},
		{"classic-quota", 200, `<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE ERROR</errMsg><returnAuthMsg>LIMITED_NUMBER_OF_SERVICE_REQUESTS_EXCEEDS_ERROR</returnAuthMsg><returnReasonCode>22</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`, false, false, "호출 한도"},
		// 일반 응답 머리의 오류 코드
		{"header-code", 200, `<response><header><resultCode>30</resultCode><resultMsg>SERVICE_KEY_IS_NOT_REGISTERED_ERROR</resultMsg></header></response>`, true, true, "등록되지 않았"},
		// 글자만 온 401·403
		{"plain-401", 401, `Unauthorized`, true, false, "거부"},
		{"html-403", 403, `<html><body>Forbidden</body></html>`, true, false, "거부"},
		// 모르는 오류는 그대로 보여 준다 (인증키 문제로 보지 않는다)
		{"unknown", 200, `<OpenAPI_ServiceResponse><cmmMsgHeader><errMsg>SERVICE ERROR</errMsg><returnAuthMsg>APPLICATION_ERROR</returnAuthMsg><returnReasonCode>1</returnReasonCode></cmmMsgHeader></OpenAPI_ServiceResponse>`, false, false, "APPLICATION_ERROR"},
	}
	for _, tc := range cases {
		c := withDgamt(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		})
		_, err := c.CheckDgamtKey(context.Background(), "SOMEKEY-0123456789-abcdef")
		if err == nil {
			t.Fatalf("%s: 오류가 없습니다", tc.name)
		}
		if got := errors.Is(err, ErrDgamtKey); got != tc.key {
			t.Errorf("%s: ErrDgamtKey=%v, want %v (%v)", tc.name, got, tc.key, err)
		}
		if got := errors.Is(err, ErrDgamtKeyNotReady); got != tc.notReady {
			t.Errorf("%s: ErrDgamtKeyNotReady=%v, want %v (%v)", tc.name, got, tc.notReady, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %q 에 %q 가 없습니다", tc.name, err, tc.want)
		}
		if strings.Contains(err.Error(), "SOMEKEY") {
			t.Errorf("%s: 오류에 인증키가 들어 있습니다: %v", tc.name, err)
		}
	}
}

// 마지막 쪽이 비어 온 경우(전체 수보다 적게 받음) — 끊긴 목록을 받아들이지 않는다 (예전에는 95% 면 통과)
func TestDgamtPartialRejected(t *testing.T) {
	const total = 2200
	for _, emptyPage := range []int{3, 2} {
		c := withDgamt(t, func(w http.ResponseWriter, r *http.Request) {
			page, _ := strconv.Atoi(r.URL.Query().Get("pageNo"))
			rows, _ := strconv.Atoi(r.URL.Query().Get("numOfRows"))
			var b strings.Builder
			b.WriteString(`<response><header><resultCode>00</resultCode></header><body><items>`)
			if page != emptyPage {
				for i := (page - 1) * rows; i < page*rows && i < total; i++ {
					fmt.Fprintf(&b, `<item><mdsCd>6%08d</mdsCd></item>`, i)
				}
			}
			fmt.Fprintf(&b, `</items><totalCount>%d</totalCount></body></response>`, total)
			_, _ = w.Write([]byte(b.String()))
		})
		items, err := c.FetchDgamtAll(context.Background(), "K", nil)
		if err == nil || !strings.Contains(err.Error(), "다 받지 못했습니다") {
			t.Fatalf("빈 쪽 %d: got %d items, err=%v", emptyPage, len(items), err)
		}
	}
	// 전체 수가 없는 응답도 받아들이지 않는다
	c := withDgamt(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<response><header><resultCode>00</resultCode></header><body><items><item><mdsCd>600000001</mdsCd></item></items></body></response>`))
	})
	if _, err := c.FetchDgamtAll(context.Background(), "K", nil); err == nil {
		t.Fatal("전체 수 없는 응답을 받아들였습니다")
	}
}
