// pubsync 는 이 저장소(비공개, 개발용)의 내용을 공개 저장소 작업 폴더로 내보낸다.
//
//	go run ./tools/pubsync <공개 저장소 폴더>        내보내고 바뀐 것을 보여 준다 (커밋·푸시는 하지 않는다)
//	go run ./tools/pubsync -check                   내보낼 내용만 점검한다
//
// 규칙
//   - git 이 추적하는 파일만 내보낸다. 아래 privateOnly 에 해당하는 파일은 내보내지 않는다.
//   - 내보내기 전에 점검한다. 하나라도 걸리면 아무것도 쓰지 않고 멈춘다.
//     1) '민감자료' 표시가 있는 파일
//     2) 실제 용어 목록(~/.config/dpr/pubsync-terms.txt — 약국 DB 에서 뽑은 거래처 이름·제품코드·바코드 등, 저장소 밖)에
//     있는 글자. 목록 파일이 없으면 멈춘다 (점검 없이 내보내지 않는다).
//   - 공개 저장소에만 있고 여기 없는 파일은 지운다 (.git 은 건드리지 않는다).
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// privateOnly : 공개 저장소로 내보내지 않는 경로 (앞부분 일치 또는 파일 이름 규칙)
var privateOnly = []string{
	"docs/DECISIONS.md",            // 소유자 결정 기록 (협업자·저장소 운영 이야기)
	"docs/PROGRESS.md",             // 작업 현황
	"docs/SIGNPATH_APPLICATION.md", // 서명 신청서 (연락처)
	"docs/PRIVATE_",                // 비공개 메모 (실데이터 분석, 민감자료 목록)
	"dist/",                        // 직접 빌드한 실행 파일
}

func isPrivate(path string) bool {
	for _, p := range privateOnly {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return strings.HasSuffix(path, "_private_test.go")
}

func main() {
	check := flag.Bool("check", false, "점검만 한다")
	flag.Parse()
	if err := run(*check, flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "pubsync:", err)
		os.Exit(1)
	}
}

func run(checkOnly bool, dst string) error {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(out)) > 0 {
		return errors.New("커밋하지 않은 변경이 있습니다 — 커밋한 내용만 내보냅니다")
	}
	out, err = exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		return err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && !isPrivate(f) {
			files = append(files, f)
		}
	}
	terms, err := loadTerms()
	if err != nil {
		return err
	}
	var problems []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			continue // 바이너리 (아이콘 등)
		}
		if bytes.Contains(b, []byte("민감자료:")) && f != "tools/pubsync/main.go" && f != "docs/SENSITIVE.md" && f != "Makefile" {
			problems = append(problems, f+": '민감자료' 표시가 있습니다 (비공개 전용이면 이름·위치를 privateOnly 규칙에 맞추세요)")
		}
		for _, t := range terms {
			if bytes.Contains(b, []byte(t)) {
				problems = append(problems, fmt.Sprintf("%s: 실제 용어 목록의 글자가 있습니다 (%d글자짜리 — 내용은 표시하지 않음)", f, len([]rune(t))))
				break
			}
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("내보내지 않았습니다 — 점검에서 %d건:\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Printf("점검 통과: 파일 %d개, 실제 용어 %d개 대조\n", len(files), len(terms))
	if checkOnly {
		return nil
	}
	if dst == "" {
		return errors.New("공개 저장소 폴더를 주세요: go run ./tools/pubsync <폴더>")
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); err != nil {
		return fmt.Errorf("%s 는 git 저장소가 아닙니다", dst)
	}
	keep := map[string]bool{}
	for _, f := range files {
		keep[f] = true
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, f)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		st, _ := os.Stat(f)
		if err := os.WriteFile(to, b, st.Mode().Perm()); err != nil {
			return err
		}
	}
	// 공개 저장소에만 남은 추적 파일은 지운다
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = dst
	out, err = cmd.Output()
	if err != nil {
		return err
	}
	removed := 0
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && !keep[f] {
			_ = os.Remove(filepath.Join(dst, f))
			removed++
		}
	}
	fmt.Printf("내보냄: %d개 (공개 저장소에서 지운 파일 %d개) → %s\n다음: 그 폴더에서 git status 로 확인한 뒤 커밋·푸시\n", len(files), removed, dst)
	return nil
}

func loadTerms() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".config", "dpr", "pubsync-terms.txt")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("실제 용어 목록(%s)이 없습니다 — 점검 없이 내보내지 않습니다 (docs/PRIVATE_SENSITIVE.md 참고)", path)
	}
	defer f.Close()
	var terms []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())
		if t != "" && !strings.HasPrefix(t, "#") && len([]rune(t)) >= 2 {
			terms = append(terms, t)
		}
	}
	if len(terms) < 10 {
		return nil, fmt.Errorf("실제 용어 목록(%s)이 비어 있습니다", path)
	}
	return terms, sc.Err()
}
