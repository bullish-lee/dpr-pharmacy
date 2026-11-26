package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"dpr/internal/service"
)

func phoneTestApp(t *testing.T) *App {
	t.Helper()
	svc, err := service.NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	return NewApp(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// 연결 비밀번호가 없거나 틀리면 자료를 주지 않는다
func TestPhoneAuth(t *testing.T) {
	a := phoneTestApp(t)
	h := a.phoneHandler()
	tok := a.phoneToken()
	if len(tok) < 20 {
		t.Fatalf("비밀번호가 너무 짧음: %q", tok)
	}
	for _, c := range []struct {
		name, tok string
		want      int
	}{{"없음", "", 401}, {"틀림", "abc", 401}, {"맞음", tok, 200}} {
		req := httptest.NewRequest("GET", "/api/cases", nil)
		req.RemoteAddr = "192.168.0.23:5555"
		if c.tok != "" {
			req.Header.Set("X-DPR-Token", c.tok)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != c.want {
			t.Errorf("%s: %d, 기대 %d (%s)", c.name, w.Code, c.want, w.Body.String())
		}
	}
	// 비밀번호를 새로 만들면 예전 것은 막힌다
	a.ResetPhoneToken()
	req := httptest.NewRequest("GET", "/api/cases?t="+tok, nil)
	req.RemoteAddr = "192.168.0.23:5555"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Errorf("예전 비밀번호가 통과함: %d", w.Code)
	}
}

// 사내망(사설 IP)·이 PC 에서 온 연결만 받는다
func TestPhonePrivateOnly(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.0.5:1": true, "10.1.2.3:1": true, "172.20.1.1:1": true, "127.0.0.1:1": true, "[::1]:1": true,
		"8.8.8.8:1": false, "211.234.1.1:1": false,
	} {
		if got := privateClient(addr); got != want {
			t.Errorf("%s → %v, 기대 %v", addr, got, want)
		}
	}
	a := phoneTestApp(t)
	req := httptest.NewRequest("GET", "/api/cases?t="+a.phoneToken(), nil)
	req.RemoteAddr = "8.8.8.8:1234"
	w := httptest.NewRecorder()
	a.phoneHandler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("바깥 주소가 통과함: %d", w.Code)
	}
}

// 휴대폰 사진은 PC 의 받은 목록에 쌓이고, 확인하면 빠진다 (읽지 못한 사진도 오류와 함께 쌓인다)
func TestPhoneLotPhotoInbox(t *testing.T) {
	a := phoneTestApp(t)
	body, _ := json.Marshal(map[string]string{"date": "2026-12-01", "code": "900000010", "name": "테스트정", "imageBase64": "bm90LWFuLWltYWdl", "mediaType": "image/jpeg"})
	req := httptest.NewRequest("POST", "/api/lot-photo", bytes.NewReader(body))
	req.RemoteAddr = "192.168.0.23:5555"
	req.Header.Set("X-DPR-Token", a.phoneToken())
	w := httptest.NewRecorder()
	a.phoneHandler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("응답 %d %s", w.Code, w.Body.String())
	}
	got := a.PhoneInbox("2026-12-01")
	if len(got) != 1 || got[0].Code != "900000010" || got[0].Error == "" {
		t.Fatalf("받은 목록: %+v", got)
	}
	if len(a.PhoneInbox("2027-01-01")) != 0 {
		t.Error("다른 회차에 보이면 안 됨")
	}
	a.DismissPhoneItem(got[0].ID)
	if len(a.PhoneInbox("")) != 0 {
		t.Error("확인한 사진이 남음")
	}
}

// 비밀번호를 여러 번 틀리면 그 연결(중계로 오면 휴대폰 주소)만 잠시 막는다
func TestPhoneLockout(t *testing.T) {
	a := phoneTestApp(t)
	h := a.phoneHandler()
	try := func(tok, cf string) int {
		req := httptest.NewRequest("GET", "/api/cases", nil)
		req.RemoteAddr = "127.0.0.1:5555" // 중계 프로그램은 이 PC 안에서 들어온다
		req.Header.Set("Cf-Connecting-Ip", cf)
		req.Header.Set("X-DPR-Token", tok)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < failMax; i++ {
		if c := try("wrong", "203.0.113.9"); c != 401 {
			t.Fatalf("%d번째: %d", i+1, c)
		}
	}
	if c := try(a.phoneToken(), "203.0.113.9"); c != http.StatusTooManyRequests {
		t.Fatalf("막힌 뒤에는 맞는 비밀번호도 막아야 함: %d", c)
	}
	if c := try(a.phoneToken(), "198.51.100.7"); c != 200 {
		t.Fatalf("다른 휴대폰은 막지 않아야 함: %d", c)
	}
}

// 휴대폰으로 가는 약품 목록에 재고·실계수 수량·가격이 없다
func TestPhoneCaseMinimal(t *testing.T) {
	a := phoneTestApp(t)
	req := httptest.NewRequest("GET", "/api/case?date=2026-11-01", nil)
	req.RemoteAddr = "192.168.0.23:5555"
	req.Header.Set("X-DPR-Token", a.phoneToken())
	w := httptest.NewRecorder()
	a.phoneHandler().ServeHTTP(w, req)
	body := w.Body.String()
	for _, k := range []string{"stock", "count\"", "price", "loss", "unit"} {
		if bytes.Contains([]byte(body), []byte(`"`+k)) {
			t.Errorf("보내면 안 되는 항목 %q: %s", k, body)
		}
	}
}
