package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateAndTail(t *testing.T) {
	dir := t.TempDir()
	log, c := New(dir, "dpr", false)
	big := strings.Repeat("x", 300<<10) // 300KB 줄 → 5MB 넘으면 돌림
	for i := 0; i < 40; i++ {
		log.Info("큰 줄", "i", i, "data", big)
	}
	log.Warn("마지막 경고", "file", "a.xlsx")
	_ = c.Close()
	files, _ := filepath.Glob(filepath.Join(dir, "logs", "dpr.log*"))
	if len(files) < 2 || len(files) > Keep+1 {
		t.Fatalf("돌린 파일 수 %d: %v", len(files), files)
	}
	for _, f := range files {
		if st, _ := os.Stat(f); st.Size() > MaxSize+int64(len(big))+1024 {
			t.Fatalf("%s 크기 %d", f, st.Size())
		}
	}
	lines, err := ReadTail(dir, "dpr", 5, "WARN")
	if err != nil || len(lines) != 1 || lines[0].Msg != "마지막 경고" || lines[0].Level != "WARN" {
		t.Fatalf("%+v %v", lines, err)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(filepath.Join(dir, "report-"+string(rune('a'+i))+".md"), []byte("x"), 0o644)
	}
	Prune(dir, "report-", 2)
	left, _ := filepath.Glob(filepath.Join(dir, "report-*"))
	if len(left) != 2 {
		t.Fatalf("남은 파일 %d", len(left))
	}
}

// 아주 긴 줄이 있어도 나머지 줄을 잃지 않고, 중간부터 읽은 잘린 첫 줄은 버리고, 따옴표 안 메시지를 제대로 푼다
func TestReadTailLongLinesAndQuotes(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	var b strings.Builder
	b.WriteString("time=2026-11-18T09:00:00+09:00 level=INFO msg=처음\n")
	b.WriteString("time=2026-11-18T09:00:01+09:00 level=ERROR msg=긴줄 detail=" + strings.Repeat("x", 2<<20) + "\n")
	b.WriteString(`time=2026-11-18T09:00:02+09:00 level=WARN msg="say \"hi\" 끝" k=v` + "\n")
	if err := os.WriteFile(filepath.Join(dir, "logs", "dpr.log"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadTail(dir, "dpr", 10, "DEBUG")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0].Msg != `say "hi" 끝` || lines[1].Msg != "긴줄" || lines[2].Msg != "처음" {
		t.Fatalf("줄 %d개: %+v", len(lines), func() []string {
			var m []string
			for _, l := range lines {
				m = append(m, l.Level+" "+l.Msg)
			}
			return m
		}())
	}
	// 끝 4MB 만 읽으면 앞의 잘린 줄은 버린다 (가짜 '처음' 줄이 생기지 않음)
	big := strings.Repeat("time=2026-11-18T09:00:03+09:00 level=INFO msg=채움 k="+strings.Repeat("y", 100)+"\n", 50000)
	_ = os.WriteFile(filepath.Join(dir, "logs", "dpr.log"), []byte("partial-garbage-line "+big), 0o644)
	lines, _ = ReadTail(dir, "dpr", 5000, "DEBUG")
	for _, l := range lines {
		if l.Msg != "채움" {
			t.Fatalf("잘린 줄이 섞임: %q", l.Raw[:min(80, len(l.Raw))])
		}
	}
}

// 이름 바꾸기에 실패하면(Windows 에서 다른 프로그램이 열어 둔 경우 등) 한 줄마다 다시 시도하지 않는다
func TestRotateFailureBacksOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dpr.log")
	r, err := OpenRotating(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	chunk := []byte(strings.Repeat("z", 1<<20) + "\n")
	for i := 0; i < 4; i++ {
		if _, err := r.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o555); err != nil { // 폴더를 읽기 전용으로 → 이름 바꾸기 실패
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	for i := 0; i < 3; i++ {
		if _, err := r.Write(chunk); err != nil {
			t.Fatalf("돌리기에 실패해도 계속 써야 함: %v", err)
		}
	}
	if r.nextRetry.IsZero() {
		t.Fatal("실패 뒤 다시 시도 시각이 정해져야 함")
	}
}
