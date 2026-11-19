// relsign — 릴리스 서명 도구 (개발자 PC 전용). 자세한 것은 docs/RELEASING.md.
//
//	go run ./tools/relsign keygen            비밀 키 만들기 (~/.config/dpr/release-signing.key) + 허용 목록에 공개 키 추가
//	go run ./tools/relsign trust             지금 비밀 키의 공개 키를 허용 목록에 넣기 (키를 백업에서 되살린 뒤 등)
//	go run ./tools/relsign sign   -version 0.2.0 build/release/DPR-v0.2.0-windows.zip   → .sha256 + .sig
//	go run ./tools/relsign verify -version 0.2.0 build/release/DPR-v0.2.0-windows.zip   ← 앱에 든 공개 키로 확인
//
// 서명·확인 전에 앱에 든 공개 키(internal/updater/keys.go)가 저장소 밖 허용 목록(~/.config/dpr/trusted-keys)에 모두 있는지 본다.
// 저장소에 쓰기 권한이 있는 다른 사람이 자기 공개 키를 슬쩍 넣으면 그 사람이 다음부터 업데이트를 서명할 수 있게 되기 때문이다.
//
// 비밀 키 위치는 DPR_SIGNING_KEY 로 바꿀 수 있다 (키 바꾸는 동안에는 ':' 로 여러 개 — 두 키로 서명).
// 비밀 키 내용은 화면·기록에 내보내지 않는다.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dpr/internal/updater"
)

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config", "dpr")
}

func keyPaths() []string {
	if p := os.Getenv("DPR_SIGNING_KEY"); p != "" {
		return filepath.SplitList(p)
	}
	return []string{filepath.Join(configDir(), "release-signing.key")}
}

func allowPath() string {
	if p := os.Getenv("DPR_TRUSTED_KEYS"); p != "" {
		return p
	}
	return filepath.Join(configDir(), "trusted-keys")
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen()
	case "trust":
		err = trust()
	case "precheck":
		err = precheck()
	case "sign", "verify":
		fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
		version := fs.String("version", "", "릴리스 버전 (예: 0.2.0)")
		_ = fs.Parse(os.Args[2:])
		if *version == "" || fs.NArg() != 1 {
			usage()
		}
		if err = checkAllowlist(); err == nil {
			if os.Args[1] == "sign" {
				err = sign(*version, fs.Arg(0))
			} else {
				err = verify(*version, fs.Arg(0))
			}
		}
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "relsign:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "사용법: relsign keygen | relsign trust | relsign precheck | relsign sign -version 0.2.0 <zip> | relsign verify -version 0.2.0 <zip>")
	os.Exit(2)
}

// sensitivePaths : 업데이트·빌드 결과에 영향을 주는 파일 — 릴리스 전에 지난 릴리스 뒤 바뀐 것을 꼭 읽어 본다
var sensitivePaths = []string{"internal/updater", "tools/relsign", "Makefile", "app.go", "main.go",
	"frontend/package.json", "frontend/package-lock.json", "frontend/vite.config.ts", "go.mod", "go.sum"}

// precheck : 릴리스 전 점검 — 커밋하지 않은 변경이 있으면 멈추고(wails.json 버전 표시는 make release 가 고친다),
// 허용 목록을 확인하고, 지난 릴리스 태그 뒤 바뀐 업데이트·빌드 관련 커밋을 보여 준다.
func precheck() error {
	out, err := exec.Command("git", "status", "--porcelain", "--", ".", ":(exclude)wails.json").Output()
	if err != nil {
		return fmt.Errorf("git 상태를 볼 수 없습니다: %w", err)
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return fmt.Errorf("커밋하지 않은 변경이 있습니다 — 커밋한 뒤 릴리스하세요:\n%s", s)
	}
	if err := checkAllowlist(); err != nil {
		return err
	}
	last, _ := exec.Command("git", "describe", "--tags", "--abbrev=0").Output()
	tag := strings.TrimSpace(string(last))
	args := []string{"log", "--oneline"}
	if tag != "" {
		args = append(args, tag+"..HEAD")
		fmt.Printf("── 지난 릴리스(%s) 뒤 바뀐 업데이트·빌드 관련 파일 — 모두 직접 한 변경인지 확인하세요:\n", tag)
	} else {
		args = append(args, "-n", "15")
		fmt.Println("── 첫 릴리스 — 업데이트·빌드 관련 파일의 최근 커밋:")
	}
	args = append(append(args, "--"), sensitivePaths...)
	log, err := exec.Command("git", args...).Output()
	if err != nil {
		return fmt.Errorf("git 기록을 볼 수 없습니다: %w", err)
	}
	if s := strings.TrimSpace(string(log)); s != "" {
		fmt.Println(s)
	} else {
		fmt.Println("(바뀐 것 없음)")
	}
	fmt.Println("── 앱에 든 공개 키:", strings.Join(updater.TrustedKeyIDs(), ", "), "(허용 목록과 일치)")
	return nil
}

func keygen() error {
	path := keyPaths()[0]
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("이미 키가 있습니다: %s (덮어쓰지 않습니다 — 키를 바꾸려면 docs/RELEASING.md 의 '키 바꾸기')", path)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := "# DPR 릴리스 서명 비밀 키 (ed25519) — 공유·커밋 금지. 잃어버리면 자동 업데이트용 새 키를 만들어야 합니다 (docs/RELEASING.md).\n" +
		base64.StdEncoding.EncodeToString(priv) + "\n"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	if err := addAllowed(pubB64); err != nil {
		return err
	}
	fmt.Println("비밀 키를 만들었습니다:", path, "(권한 600 — 따로 안전한 곳에 백업하세요)")
	fmt.Println("허용 목록에 공개 키를 넣었습니다:", allowPath())
	fmt.Println("공개 키 ID:", updater.KeyID(pub))
	fmt.Println("앱에 넣을 공개 키 (internal/updater/keys.go):", pubB64)
	return nil
}

// trust : 비밀 키 파일에서 공개 키를 구해 허용 목록에 넣는다 (저장소의 keys.go 를 믿지 않고 비밀 키에서 직접)
func trust() error {
	privs, err := loadKeys()
	if err != nil {
		return err
	}
	for _, p := range privs {
		pub := p.Public().(ed25519.PublicKey)
		if err := addAllowed(base64.StdEncoding.EncodeToString(pub)); err != nil {
			return err
		}
		fmt.Println("허용 목록에 넣었습니다:", updater.KeyID(pub), "→", allowPath())
	}
	return nil
}

// addAllowed : 저장소 밖 허용 목록에 공개 키를 더한다
func addAllowed(pubB64 string) error {
	path := allowPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	allowed, _ := readAllowed()
	if allowed[pubB64] {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if len(allowed) == 0 {
		_, _ = f.WriteString("# 앱에 넣어도 되는 릴리스 서명 공개 키 (한 줄에 하나, base64). 저장소 밖에 두어 다른 사람이 키를 몰래 넣지 못하게 한다.\n")
	}
	_, err = f.WriteString(pubB64 + "\n")
	return err
}

func readAllowed() (map[string]bool, error) {
	b, err := os.ReadFile(allowPath())
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out[l] = true
		}
	}
	return out, nil
}

// checkAllowlist : 앱에 든 공개 키가 모두 허용 목록에 있어야 한다 (일부만 있는 것은 키 바꾸기 중 허용)
func checkAllowlist() error {
	allowed, err := readAllowed()
	if err != nil {
		return fmt.Errorf("허용 목록이 없습니다 (%s) — 'make keygen' 으로 만들거나 백업에서 되살리세요", allowPath())
	}
	for i, k := range updater.TrustedKeys {
		if !allowed[k] {
			return fmt.Errorf("앱에 든 공개 키 %s(%s)가 허용 목록에 없습니다 — internal/updater/keys.go 를 누가 바꿨는지 git log 로 확인하세요",
				updater.TrustedKeyIDs()[i], firstN(k, 12))
		}
	}
	return nil
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func loadKeys() ([]ed25519.PrivateKey, error) {
	var out []ed25519.PrivateKey
	for _, path := range keyPaths() {
		st, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("서명 키가 없습니다 (%s) — 'make keygen' 으로 만들거나 백업에서 되살리세요", path)
		}
		if st.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("서명 키 파일을 다른 사용자가 읽을 수 있습니다 (%s) — chmod 600 으로 바꾼 뒤 다시 하세요", path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		k, err := updater.ParsePrivateKey(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, k)
	}
	return out, nil
}

func sign(version, zipPath string) error {
	privs, err := loadKeys()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return err
	}
	name := filepath.Base(zipPath)
	text, err := updater.Sign(version, name, data, privs...)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if err := os.WriteFile(zipPath+".sha256", []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(zipPath+".sig", []byte(text), 0o644); err != nil {
		return err
	}
	var ids []string
	for _, p := range privs {
		ids = append(ids, updater.KeyID(p.Public().(ed25519.PublicKey)))
	}
	fmt.Println("서명했습니다:", zipPath+".sig", "(키", strings.Join(ids, ", ")+")")
	return nil
}

// verify 는 앱이 하는 것과 같은 확인을 한다 — 체크섬 파일 + 앱에 든 공개 키(updater.TrustedKeys)로 서명. 릴리스를 올리기 전·후에 쓴다.
func verify(version, zipPath string) error {
	data, err := os.ReadFile(zipPath)
	if err != nil {
		return err
	}
	sum, err := os.ReadFile(zipPath + ".sha256")
	if err != nil {
		return errors.New("체크섬 파일(.sha256)이 없습니다")
	}
	if err := updater.VerifySHA256(data, string(sum)); err != nil {
		return err
	}
	sig, err := os.ReadFile(zipPath + ".sig")
	if err != nil {
		return errors.New("서명 파일(.sig)이 없습니다")
	}
	if err := updater.VerifyRelease(data, string(sig), version, filepath.Base(zipPath)); err != nil {
		return err
	}
	fmt.Println("서명·체크섬 확인: 앱이 이 파일을 받아들입니다 —", filepath.Base(zipPath), "v"+updater.NormVersion(version))
	return nil
}
