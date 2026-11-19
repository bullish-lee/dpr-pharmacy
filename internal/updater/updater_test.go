package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"0.2.0", "v0.2.1", -1}, {"v1.0.0", "0.9.9", 1}, {"0.2.0", "0.2.0", 0}, {"dev", "0.1.0", -1}, {"0.10.0", "0.9.0", 1}, {"0.2.0-rc1", "0.2.0", 0}}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s,%s)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func makeZip(t *testing.T, name string, content []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name)
	_, _ = w.Write(content)
	r, _ := zw.Create("README.txt")
	_, _ = r.Write([]byte("hi"))
	_ = zw.Close()
	return buf.Bytes()
}

func TestLatestDownloadVerifyReplace(t *testing.T) {
	exe := bytes.Repeat([]byte("MZ-new-exe"), 200000) // 2MB
	z := makeZip(t, "DPR.exe", exe)
	sum := sha256.Sum256(z)
	sumFile := hex.EncodeToString(sum[:]) + "  DPR-v0.3.0-windows.zip\n"
	var authSeen string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			_, _ = w.Write([]byte(`{"tag_name":"v0.3.0","name":"0.3.0","body":"변경 사항","assets":[
				{"id":1,"name":"DPR-v0.3.0-windows.zip","size":1,"url":"` + srv.URL + `/assets/1"},
				{"id":2,"name":"DPR-v0.3.0-windows.zip.sha256","size":1,"url":"` + srv.URL + `/assets/2"}]}`))
		case "/assets/1":
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "accept", 400)
				return
			}
			_, _ = w.Write(z)
		case "/assets/2":
			_, _ = w.Write([]byte(sumFile))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	APIBase = srv.URL
	defer func() { APIBase = "https://api.github.com" }()
	c := New("o/r", "tok")
	rel, err := c.Latest(context.Background())
	if err != nil || rel.Tag != "v0.3.0" || authSeen != "Bearer tok" {
		t.Fatalf("latest %+v %v auth=%q", rel, err, authSeen)
	}
	za, sa, _ := rel.WindowsAssets()
	if za == nil || sa == nil {
		t.Fatal("assets")
	}
	zd, _ := c.Download(context.Background(), za)
	sd, _ := c.Download(context.Background(), sa)
	if err := VerifySHA256(zd, string(sd)); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(append(zd, 0), string(sd)); err == nil {
		t.Fatal("변조된 파일은 거부해야 함")
	}
	got, err := ExtractFile(zd, "dpr.exe")
	if err != nil || !bytes.Equal(got, exe) {
		t.Fatalf("extract %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "DPR.exe")
	_ = os.WriteFile(path, []byte("old"), 0o755)
	if err := Replace(path, got); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, exe) {
		t.Fatal("교체되지 않음")
	}
	if b, _ := os.ReadFile(path + ".old"); string(b) != "old" {
		t.Fatal(".old 보관")
	}
	CleanupOld(path)
	if _, err := os.Stat(path + ".old"); !os.IsNotExist(err) {
		t.Fatal(".old 삭제")
	}
	if err := Replace(path, []byte("tiny")); err == nil || !strings.Contains(err.Error(), "작습니다") {
		t.Fatal("너무 작은 파일 거부")
	}
}
