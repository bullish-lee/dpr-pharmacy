package updater

// 릴리스 서명 — 업데이트 파일이 개발자가 만든 것인지 확인한다 (docs/RELEASING.md).
//
// .sha256 은 '받다가 깨지지 않았는지'만 알려 준다. 같은 릴리스에 들어 있으므로, 저장소에 릴리스를 올릴 수 있는 사람
// (유출된 토큰 등)이 만든 파일도 체크섬은 맞는다. 그래서 개발자 PC에만 있는 비밀 키(ed25519)로 서명하고,
// 앱에는 짝이 되는 공개 키(TrustedKeys)를 넣어 서명이 맞을 때만 알리고 설치한다.
//
// 서명 내용 = 버전 + 파일 이름 + zip 의 SHA-256. 버전을 함께 서명하므로 옛 버전 zip 을 새 버전인 척 다시 올려도
// (되돌리기 공격) 설치하지 않는다. 키를 바꾸는 동안에는 두 키로 서명한다(sig= 줄 여러 개).
//
// 서명 파일 'DPR-v<버전>-windows.zip.sig' (텍스트, 64KB 이하):
//
//	DPR-release-signature v1
//	key=<공개 키 ID>
//	version=0.2.0
//	file=DPR-v0.2.0-windows.zip
//	sha256=<hex>
//	sig=<base64 서명>            (키마다 한 줄)

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// TrustedKeys : 릴리스 서명을 확인하는 공개 키 (base64). 비밀 키는 개발자 PC의 ~/.config/dpr/release-signing.key 에만 있다.
// 이 목록은 저장소 밖의 허용 목록(~/.config/dpr/trusted-keys)과 맞아야 릴리스를 만들 수 있다 (tools/relsign).
// 키를 바꿀 때: 새 공개 키를 여기에 '추가'하고 한동안 두 키로 서명해 배포한 뒤 옛 키를 뺀다 (docs/RELEASING.md).
var TrustedKeys = []string{
	releaseKey2026,
}

const (
	sigHeader  = "DPR-release-signature v1"
	MaxSigFile = 64 << 10 // 서명·체크섬 파일 최대 크기
)

var reVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([.+-][0-9A-Za-z.-]+)?$`)

// NormVersion : 'v0.3.0'·'V0.3.0'·' 0.3.0 ' → '0.3.0' (앞의 v 는 한 번만 뗀다). 버전 형식이 아니면 "".
func NormVersion(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "v")
	if !reVersion.MatchString(v) {
		return ""
	}
	return v
}

// SignatureMessage : 서명하는 내용 (버전·파일 이름·SHA-256 을 묶는다). version 은 NormVersion 한 값.
func SignatureMessage(version, file, sha string) []byte {
	return []byte("DPR-release v1\nversion=" + version + "\nfile=" + file + "\nsha256=" + strings.ToLower(sha) + "\n")
}

// KeyID : 공개 키를 짧게 부르는 이름 (SHA-256 앞 8바이트)
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:8])
}

// Sign 은 zip 의 서명 파일 내용을 만든다 (릴리스 만들 때, tools/relsign). 키를 여러 개 주면 키마다 서명한다.
func Sign(version, file string, zipData []byte, privs ...ed25519.PrivateKey) (string, error) {
	v := NormVersion(version)
	if v == "" {
		return "", fmt.Errorf("버전 형식이 아닙니다: %q", version)
	}
	if len(privs) == 0 {
		return "", errors.New("서명 키가 없습니다")
	}
	sum := sha256.Sum256(zipData)
	sha := hex.EncodeToString(sum[:])
	msg := SignatureMessage(v, file, sha)
	var ids, sigs []string
	for _, priv := range privs {
		ids = append(ids, KeyID(priv.Public().(ed25519.PublicKey)))
		sigs = append(sigs, "sig="+base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)))
	}
	return fmt.Sprintf("%s\nkey=%s\nversion=%s\nfile=%s\nsha256=%s\n%s\n", sigHeader, strings.Join(ids, ","), v, file, sha, strings.Join(sigs, "\n")), nil
}

// ErrBadSignature : 서명이 없거나 맞지 않음 — 알리지도 설치하지도 않는다
var ErrBadSignature = errors.New("업데이트 파일의 서명이 맞지 않아 설치하지 않았습니다 (개발자가 만든 파일인지 확인할 수 없음)")

// VerifySigFields 는 zip 없이 서명 파일만으로, 이 서명이 tag·fileName 에 대한 개발자 서명인지 확인하고 서명된 SHA-256 을 돌려준다.
// (새 버전을 알리기 전에 작은 .sig 만 받아 확인한다 — 서명 없는 릴리스 이름으로 알림을 띄우지 못하게)
func VerifySigFields(sigText, tag, fileName string) (string, error) {
	if len(sigText) > MaxSigFile {
		return "", fmt.Errorf("%w: 서명 파일이 너무 큼", ErrBadSignature)
	}
	lines := strings.Split(strings.ReplaceAll(sigText, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != sigHeader {
		return "", fmt.Errorf("%w: 서명 파일 형식이 다름", ErrBadSignature)
	}
	fields := map[string]string{}
	var sigs [][]byte
	for _, l := range lines[1:] {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		if k == "sig" {
			if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) == ed25519.SignatureSize {
				sigs = append(sigs, b)
			}
			continue
		}
		if _, dup := fields[k]; dup { // 같은 항목이 두 번 — 어느 쪽을 믿을지 모호하므로 거부
			return "", fmt.Errorf("%w: '%.20s' 항목이 두 번 있음", ErrBadSignature, k)
		}
		fields[k] = v
	}
	want := NormVersion(tag)
	switch {
	case want == "":
		return "", fmt.Errorf("%w: 릴리스 버전 형식이 아님 (%.40q)", ErrBadSignature, tag)
	case fields["version"] != want:
		return "", fmt.Errorf("%w: 서명한 버전(%.40q)과 릴리스 버전(%s)이 다름", ErrBadSignature, fields["version"], want)
	case !strings.EqualFold(fields["file"], fileName):
		return "", fmt.Errorf("%w: 서명한 파일 이름(%.60q)과 다름", ErrBadSignature, fields["file"])
	case len(fields["sha256"]) != 64:
		return "", fmt.Errorf("%w: 서명한 SHA-256 형식이 아님", ErrBadSignature)
	case len(sigs) == 0:
		return "", fmt.Errorf("%w: 서명 값을 읽지 못함", ErrBadSignature)
	}
	sha := strings.ToLower(fields["sha256"])
	if _, err := hex.DecodeString(sha); err != nil {
		return "", fmt.Errorf("%w: 서명한 SHA-256 형식이 아님", ErrBadSignature)
	}
	msg := SignatureMessage(want, fields["file"], sha)
	for _, k := range TrustedKeys {
		pub, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			continue
		}
		for _, sig := range sigs {
			if ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
				return sha, nil
			}
		}
	}
	return "", fmt.Errorf("%w: 앱이 아는 키로 서명하지 않음 (%.40q)", ErrBadSignature, fields["key"])
}

// VerifyRelease 는 zip 이 tag·fileName 으로 개발자 키로 서명되었는지 확인한다.
func VerifyRelease(zipData []byte, sigText, tag, fileName string) error {
	sha, err := VerifySigFields(sigText, tag, fileName)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(zipData)
	if hex.EncodeToString(sum[:]) != sha {
		return fmt.Errorf("%w: 파일 내용이 서명한 것과 다름", ErrBadSignature)
	}
	return nil
}

// ParsePrivateKey : 비밀 키 파일 내용 (설명 줄 + base64 한 줄)
func ParsePrivateKey(text string) (ed25519.PrivateKey, error) {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(l)
		if err == nil && len(b) == ed25519.PrivateKeySize {
			return ed25519.PrivateKey(b), nil
		}
	}
	return nil, errors.New("비밀 키 파일 형식이 아닙니다")
}

// TrustedKeyIDs : 앱에 든 공개 키 ID 목록 (형식이 틀린 키는 "invalid")
func TrustedKeyIDs() []string {
	var out []string
	for _, k := range TrustedKeys {
		pub, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			out = append(out, "invalid")
			continue
		}
		out = append(out, KeyID(ed25519.PublicKey(pub)))
	}
	return out
}
