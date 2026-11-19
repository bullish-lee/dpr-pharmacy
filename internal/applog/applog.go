// Package applog 는 앱 기록(로그) 파일을 관리한다.
//
//   - 파일: data/logs/dpr.log (앱), data/logs/dpr-cli.log (작업 스케줄러 등 명령줄) — 두 프로세스가 같은 파일을 돌리지 않게 나눈다.
//   - 회전: 5MB 를 넘으면 dpr.log → dpr.log.1 → … → dpr.log.5 (가장 오래된 것은 지움). 최대 약 30MB.
//   - 수준: 기본 INFO, 설정의 '자세한 기록'을 켜면 DEBUG (프로그램을 다시 켜지 않아도 바로 적용).
//   - 앱 화면에서 끝부분을 읽어 보여준다 (ReadTail).
package applog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	MaxSize = 5 << 20
	Keep    = 5
)

// Level 은 모든 로거가 함께 쓰는 수준 (SetDebug 로 바꾼다)
var Level = new(slog.LevelVar)

func SetDebug(on bool) {
	if on {
		Level.Set(slog.LevelDebug)
	} else {
		Level.Set(slog.LevelInfo)
	}
}

// RotatingFile 은 크기를 넘으면 스스로 돌리는 기록 파일 (여러 고루틴에서 안전)
type RotatingFile struct {
	mu        sync.Mutex
	path      string
	f         *os.File
	size      int64
	nextRetry time.Time // 돌리기(이름 바꾸기)에 실패한 뒤 다시 시도할 시각
}

func OpenRotating(path string) (*RotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	r := &RotatingFile{path: path}
	if err := r.open(); err != nil {
		return nil, err
	}
	if r.size > MaxSize {
		_ = r.rotate()
	}
	return r, nil
}

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *RotatingFile) rotate() error {
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, Keep))
	for i := Keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	// Windows 에서는 다른 프로그램(기록 보기·백신 등)이 파일을 열어 두면 이름을 바꾸지 못한다.
	// 그래도 같은 파일에 계속 쓰고, 한 줄마다 다시 시도하지 않도록 1분 뒤에 다시 시도한다.
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		r.nextRetry = time.Now().Add(time.Minute)
	} else {
		r.nextRetry = time.Time{}
	}
	return r.open()
}

func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	if r.size+int64(len(p)) > MaxSize && !time.Now().Before(r.nextRetry) {
		_ = r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// New 는 dataDir/logs/<name>.log 에 쓰는 로거를 만든다. 파일을 열 수 없으면 표준 오류로만 쓴다.
func New(dataDir, name string, alsoStderr bool) (*slog.Logger, io.Closer) {
	var w io.Writer = io.Discard
	var c io.Closer = io.NopCloser(nil)
	if rf, err := OpenRotating(filepath.Join(dataDir, "logs", name+".log")); err == nil {
		w, c = rf, rf
	}
	if alsoStderr {
		w = io.MultiWriter(w, os.Stderr)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: Level})), c
}

// ── 화면에서 읽기 ────────────────────────────────────────────────

type Line struct {
	Time  string `json:"time"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Attrs string `json:"attrs"`
	Raw   string `json:"raw"`
}

var reHead = regexp.MustCompile(`^time=(\S+) level=(\S+) msg=("(?:[^"\\]|\\.)*"|\S+)\s?(.*)$`)

func rank(level string) int {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return 0
	case "INFO":
		return 1
	case "WARN":
		return 2
	case "ERROR":
		return 3
	}
	return 1
}

// ReadTail 은 기록 파일 끝에서 minLevel 이상인 줄을 최근 것부터 n개 돌려준다 (돌린 파일 .1 까지 본다).
func ReadTail(dataDir, name string, n int, minLevel string) ([]Line, error) {
	if n <= 0 || n > 5000 {
		n = 500
	}
	base := filepath.Join(dataDir, "logs", name+".log")
	var out []Line
	for _, path := range []string{base, base + ".1"} {
		lines, err := readLines(path, 4<<20)
		if err != nil {
			continue
		}
		for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
			l := parse(lines[i])
			if rank(l.Level) >= rank(minLevel) {
				out = append(out, l)
			}
		}
		if len(out) >= n {
			break
		}
	}
	return out, nil
}

// readLines : 파일 끝 tail 바이트를 줄로 나눈다. 중간부터 읽었으면 잘린 첫 줄은 버린다.
// 아주 긴 줄이 있어도 나머지 줄을 잃지 않게 한 번에 읽어 나눈다 (bufio.Scanner 는 긴 줄에서 멈춘다).
func readLines(path string, tail int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	seeked := false
	if st, err := f.Stat(); err == nil && st.Size() > tail {
		if _, err := f.Seek(st.Size()-tail, io.SeekStart); err == nil {
			seeked = true
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, tail))
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if seeked && len(lines) > 0 {
		lines = lines[1:]
	}
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	for i, l := range lines {
		if len(l) > 8000 { // 화면에 보일 만큼만 (한글이 깨지지 않게 글자 경계에서)
			cut := 8000
			for cut > 0 && !utf8.RuneStart(l[cut]) {
				cut--
			}
			lines[i] = l[:cut] + "…"
		}
	}
	return lines, nil
}

func parse(s string) Line {
	m := reHead.FindStringSubmatch(s)
	if m == nil {
		return Line{Level: "INFO", Msg: s, Raw: s}
	}
	msg := m[3]
	if strings.HasPrefix(msg, `"`) {
		if u, err := strconv.Unquote(msg); err == nil { // slog 는 strconv.Quote 로 감싼다
			msg = u
		} else {
			msg = strings.TrimSuffix(strings.TrimPrefix(msg, `"`), `"`)
		}
	}
	return Line{Time: m[1], Level: m[2], Msg: msg, Attrs: m[4], Raw: s}
}

// Prune 은 폴더에서 이름이 prefix 로 시작하는 파일 중 오래된 것을 keep 개만 남기고 지운다 (보고서·원본 파일 정리).
func Prune(dir, prefix string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return
	}
	type fi struct {
		name string
		mod  int64
	}
	var files []fi
	for _, n := range names {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil {
			files = append(files, fi{n, st.ModTime().UnixNano()})
		}
	}
	for i := 0; i < len(files); i++ { // 최신 순 정렬 (작은 목록)
		for j := i + 1; j < len(files); j++ {
			if files[j].mod > files[i].mod {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
	for _, f := range files[keep:] {
		_ = os.Remove(filepath.Join(dir, f.name))
	}
}
