package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dpr/internal/updater"
)

func TestCheckUpdate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	oldKeys := updater.TrustedKeys
	updater.TrustedKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	defer func() { updater.TrustedKeys = oldKeys }()
	sigText := "" // "" = 서명 파일 없음
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/bullish-lee/dpr-pharmacy/releases/latest":
		case "/a/sig":
			_, _ = w.Write([]byte(sigText))
			return
		default:
			http.NotFound(w, r)
			return
		}
		assets := `{"id":1,"name":"DPR-v9.9.9-windows.zip","size":10,"url":"x"},{"id":2,"name":"DPR-v9.9.9-windows.zip.sha256","size":1,"url":"y"}`
		if sigText != "" {
			assets += `,{"id":3,"name":"DPR-v9.9.9-windows.zip.sig","size":1,"url":"http://` + r.Host + `/a/sig"}`
		}
		_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","body":"변경","assets":[` + assets + `]}`))
	}))
	defer srv.Close()
	updater.APIBase = srv.URL
	defer func() { updater.APIBase = "https://api.github.com" }()
	old := AppVersion
	AppVersion = "0.2.0"
	defer func() { AppVersion = old }()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// 서명(.sig) 없는 릴리스·엉터리 서명은 알리지 않는다 (릴리스 이름·설명도 화면에 내보내지 않음)
	info, err := s.CheckUpdate(context.Background(), true)
	if err != nil || info.Available || !strings.Contains(info.Message, "서명") || info.Latest != "" {
		t.Fatalf("서명 없는 릴리스: %+v %v", info, err)
	}
	sigText = "DPR-release-signature v1\nversion=9.9.9\nfile=DPR-v9.9.9-windows.zip\nsha256=" + strings.Repeat("0", 64) + "\nsig=garbage\n"
	if info, _ := s.CheckUpdate(context.Background(), true); info.Available {
		t.Fatalf("엉터리 서명은 알리지 않음: %+v", info)
	}
	var notes int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '새 버전이 있습니다'").Scan(&notes)
	if notes != 0 {
		t.Fatalf("서명 없는 릴리스 알림 %d건", notes)
	}
	sigText, _ = updater.Sign("v9.9.9", "DPR-v9.9.9-windows.zip", []byte("zip"), priv)
	_ = s.DB.SetSetting(SetUpdateChecked, "")
	info, err = s.CheckUpdate(context.Background(), false)
	if err != nil || !info.Available || info.Latest != "9.9.9" {
		t.Fatalf("%+v %v", info, err)
	}
	if again, _ := s.CheckUpdate(context.Background(), false); !again.Skipped {
		t.Fatalf("6시간 안에는 건너뛰어야 함: %+v", again)
	}
	if _, err := s.CheckUpdate(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM notification WHERE title = '새 버전이 있습니다'").Scan(&n)
	if n != 1 {
		t.Fatalf("같은 버전 알림은 한 번만: %d", n)
	}
}

// 업데이트 받기: 체크섬과 개발자 서명이 모두 맞아야 실행 파일을 꺼낸다
func TestFetchVerifiedUpdate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	oldKeys := updater.TrustedKeys
	updater.TrustedKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	defer func() { updater.TrustedKeys = oldKeys }()
	exe := bytes.Repeat([]byte("MZ-new"), 300000)
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("DPR.exe")
	_, _ = w.Write(exe)
	_ = zw.Close()
	zipData := zbuf.Bytes()
	sum := sha256.Sum256(zipData)
	var sigText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v9.9.9","assets":[{"id":1,"name":"DPR-v9.9.9-windows.zip","url":"` + base + `/a/zip"},` +
				`{"id":2,"name":"DPR-v9.9.9-windows.zip.sha256","url":"` + base + `/a/sum"},{"id":3,"name":"DPR-v9.9.9-windows.zip.sig","url":"` + base + `/a/sig"}]}`))
		case "/a/zip":
			_, _ = w.Write(zipData)
		case "/a/sum":
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  DPR-v9.9.9-windows.zip\n"))
		case "/a/sig":
			_, _ = w.Write([]byte(sigText))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	updater.APIBase = srv.URL
	defer func() { updater.APIBase = "https://api.github.com" }()
	old := AppVersion
	AppVersion = "0.2.0"
	defer func() { AppVersion = old }()
	c := updater.New("o/r", "")
	ctx := context.Background()

	sign := func(version string, k ed25519.PrivateKey) string {
		t.Helper()
		v, err := updater.Sign(version, "DPR-v9.9.9-windows.zip", zipData, k)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	sigText = sign("v9.9.9", priv)
	if _, _, got, err := fetchVerifiedUpdate(ctx, c, "9.9.9"); err != nil || !bytes.Equal(got, exe) {
		t.Fatalf("서명이 맞으면 받음: %v", err)
	}
	if _, _, _, err := fetchVerifiedUpdate(ctx, c, "9.9.8"); err == nil || !strings.Contains(err.Error(), "확인한 버전") {
		t.Fatalf("확인한 버전과 지금 최신이 다르면 받지 않음: %v", err)
	}
	sigText = sign("v0.1.0", priv) // 옛 버전으로 서명한 파일
	if _, _, _, err := fetchVerifiedUpdate(ctx, c, "9.9.9"); !errors.Is(err, updater.ErrBadSignature) {
		t.Fatalf("버전이 다른 서명은 거부: %v", err)
	}
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	sigText = sign("v9.9.9", other) // 다른 사람의 키
	if _, _, _, err := fetchVerifiedUpdate(ctx, c, "9.9.9"); !errors.Is(err, updater.ErrBadSignature) {
		t.Fatalf("모르는 키는 거부: %v", err)
	}
}
