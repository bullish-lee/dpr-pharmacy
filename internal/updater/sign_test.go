package updater

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func trust(t *testing.T, pubs ...ed25519.PublicKey) {
	t.Helper()
	old := TrustedKeys
	TrustedKeys = nil
	for _, p := range pubs {
		TrustedKeys = append(TrustedKeys, base64.StdEncoding.EncodeToString(p))
	}
	t.Cleanup(func() { TrustedKeys = old })
}

func mustSign(t *testing.T, version, file string, data []byte, privs ...ed25519.PrivateKey) string {
	t.Helper()
	s, err := Sign(version, file, data, privs...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestReleaseSignature(t *testing.T) {
	pub, priv := newKey(t)
	trust(t, pub)
	zip := []byte("PK\x03\x04 진짜 릴리스 내용")
	sig := mustSign(t, "v0.3.0", "DPR-v0.3.0-windows.zip", zip, priv)
	for _, tag := range []string{"v0.3.0", "V0.3.0", "0.3.0"} { // 태그 표기 차이는 같은 버전
		if err := VerifyRelease(zip, sig, tag, "DPR-v0.3.0-windows.zip"); err != nil {
			t.Fatalf("정상 서명 (%s): %v", tag, err)
		}
	}
	bad := func(label string, data []byte, sigText, tag, file string) {
		t.Helper()
		if err := VerifyRelease(data, sigText, tag, file); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("%s: 거부해야 함 (%v)", label, err)
		}
	}
	bad("내용 바뀜", append(append([]byte(nil), zip...), 'x'), sig, "v0.3.0", "DPR-v0.3.0-windows.zip")
	bad("옛 파일을 새 버전으로 (되돌리기)", zip, sig, "v0.9.0", "DPR-v0.3.0-windows.zip")
	bad("다른 파일 이름", zip, sig, "v0.3.0", "DPR-v0.9.0-windows.zip")
	bad("서명 파일 형식", zip, "아무 내용", "v0.3.0", "DPR-v0.3.0-windows.zip")
	bad("서명 값 손상", zip, strings.Replace(sig, "sig=", "sig=AAAA", 1), "v0.3.0", "DPR-v0.3.0-windows.zip")
	bad("버전 줄 고침", zip, strings.Replace(sig, "version=0.3.0", "version=0.9.0", 1), "v0.9.0", "DPR-v0.3.0-windows.zip")
	bad("v 두 번 (vv0.3.0)", zip, strings.Replace(sig, "version=0.3.0", "version=v0.3.0", 1), "vv0.3.0", "DPR-v0.3.0-windows.zip")
	bad("같은 항목 두 번", zip, strings.Replace(sig, "version=0.3.0\n", "version=0.3.0\nversion=0.9.0\n", 1), "v0.3.0", "DPR-v0.3.0-windows.zip")
	_, other := newKey(t) // 다른 사람의 키
	bad("모르는 키", zip, mustSign(t, "v0.3.0", "DPR-v0.3.0-windows.zip", zip, other), "v0.3.0", "DPR-v0.3.0-windows.zip")
	// 큰 서명 파일·긴 필드는 오류 문장을 채우지 못한다
	huge := strings.Replace(sig, "version=0.3.0", "version="+strings.Repeat("9", 200), 1)
	if err := VerifyRelease(zip, huge, "v0.3.0", "DPR-v0.3.0-windows.zip"); err == nil || len(err.Error()) > 400 {
		t.Fatalf("긴 필드 오류 문장: %d자", len(err.Error()))
	}
	if _, err := VerifySigFields(strings.Repeat("x", MaxSigFile+1), "v0.3.0", "a.zip"); !errors.Is(err, ErrBadSignature) {
		t.Fatal("너무 큰 서명 파일은 거부")
	}
}

// 키를 바꾸는 동안 두 키로 서명하면 옛 키만 아는 앱도, 새 키만 아는 앱도 받아들인다
func TestDualSignatureForKeyRotation(t *testing.T) {
	oldPub, oldPriv := newKey(t)
	newPub, newPriv := newKey(t)
	zip := []byte("PK release")
	sig := mustSign(t, "v1.0.0", "DPR-v1.0.0-windows.zip", zip, oldPriv, newPriv)
	if strings.Count(sig, "\nsig=") != 2 {
		t.Fatalf("서명 두 줄: %q", sig)
	}
	for label, pub := range map[string]ed25519.PublicKey{"옛 키만 아는 앱": oldPub, "새 키만 아는 앱": newPub} {
		trust(t, pub)
		if err := VerifyRelease(zip, sig, "v1.0.0", "DPR-v1.0.0-windows.zip"); err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	}
}

// 서명 파일만으로(zip 없이) 확인 — 알리기 전에 쓴다
func TestVerifySigFieldsWithoutZip(t *testing.T) {
	pub, priv := newKey(t)
	trust(t, pub)
	zip := []byte("PK release")
	sig := mustSign(t, "0.4.0", "DPR-v0.4.0-windows.zip", zip, priv)
	sha, err := VerifySigFields(sig, "v0.4.0", "DPR-v0.4.0-windows.zip")
	if err != nil || len(sha) != 64 {
		t.Fatalf("%q %v", sha, err)
	}
	if _, err := VerifySigFields(sig, "v0.4.0-URGENT-install-manually", "DPR-v0.4.0-windows.zip"); err == nil {
		t.Fatal("서명하지 않은 태그는 거부")
	}
}

func TestPrivateKeyFileFormat(t *testing.T) {
	_, priv := newKey(t)
	text := "# 설명 줄\n" + base64.StdEncoding.EncodeToString(priv) + "\n"
	got, err := ParsePrivateKey(text)
	if err != nil || !got.Equal(priv) {
		t.Fatalf("비밀 키 읽기: %v", err)
	}
	if _, err := ParsePrivateKey("# 설명만\n"); err == nil {
		t.Fatal("키 없는 파일은 오류")
	}
}

// 앱에 든 공개 키 — 바뀌면 이 테스트도 바꿔야 하므로 코드 리뷰에서 바로 눈에 띈다 (허용 목록 확인은 tools/relsign)
func TestEmbeddedKeysArePinned(t *testing.T) {
	ids := TrustedKeyIDs()
	if strings.Join(ids, ",") != "6aa440c9dc8e78e0" {
		t.Fatalf("앱에 든 공개 키가 바뀌었습니다: %v — 의도한 키 바꾸기인지 확인하세요 (docs/RELEASING.md)", ids)
	}
}

// zip 맨 위의 DPR.exe 하나만 받아들인다
func TestExtractOnlyRootExe(t *testing.T) {
	mk := func(names ...string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, n := range names {
			w, _ := zw.Create(n)
			_, _ = w.Write([]byte("content of " + n))
		}
		_ = zw.Close()
		return buf.Bytes()
	}
	if got, err := ExtractFile(mk("docs/DPR.exe", "DPR.exe"), "DPR.exe"); err != nil || string(got) != "content of DPR.exe" {
		t.Fatalf("맨 위 파일: %q %v", got, err)
	}
	if _, err := ExtractFile(mk("docs/DPR.exe"), "DPR.exe"); err == nil {
		t.Fatal("폴더 안의 DPR.exe 는 받지 않음")
	}
	if _, err := ExtractFile(mk("DPR.exe", "dpr.EXE"), "DPR.exe"); err == nil {
		t.Fatal("맨 위에 둘이면 거부")
	}
}
