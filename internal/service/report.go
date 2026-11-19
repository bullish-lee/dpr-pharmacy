package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"dpr/internal/applog"
	"dpr/internal/updater"
)

// 오류 보고 — 버전·최근 자동 작업·최근 기록(로그)을 모아 GitHub 이슈로 보낸다.
// 약국 거래 자료(사입·조제·재고)는 넣지 않는다. 로그의 파일명·경로·토큰은 가린다.

type ReportResult struct {
	Path     string `json:"path"`     // 저장한 보고서 파일
	IssueURL string `json:"issueUrl"` // 만든 이슈 주소 (토큰으로 바로 보낸 경우)
	OpenURL  string `json:"openUrl"`  // 브라우저로 열 새 이슈 주소 (토큰이 없거나 실패한 경우)
	Message  string `json:"message"`
}

var (
	reFileKV     = regexp.MustCompile(`file=("(?:[^"\\]|\\.)*"|\S+)`)
	reToken      = regexp.MustCompile(`(github_pat_|ghp_|gho_|ghu_|ghs_|ghr_)[A-Za-z0-9_]+`)
	reServiceKey = regexp.MustCompile(`(?i)(servicekey=)[^&\s"']+`)
	reDataFile   = regexp.MustCompile(`(?i)[^"=\\/:\s](?:[^"=\\/:]{0,120}[^"=\\/:\s])?\.(xlsx|xlsm|xls|csv)\b`) // 점 바로 앞은 공백이 아님 ('… .xls 형식' 같은 설명은 두기)
)

// sanitize : 보고서로 내보내는 줄에서 약국을 알아볼 수 있는 것을 가린다.
//
//	토큰 → <토큰 생략>, 데이터 폴더·홈 폴더 → <데이터폴더>·<홈폴더>, 드라이브 문자로 시작하는 경로 → <경로>,
//	file= 값과 엑셀·CSV 파일 이름(공백이 든 이름 포함) → <파일명>
//
// 기록(slog)은 공백이 든 값을 따옴표로 감싸며 역슬래시를 두 번 쓴다(C:\\Users\\…) — 두 형태 모두 가린다.
func (s *Service) sanitize(line string) string {
	line = reToken.ReplaceAllString(line, "<토큰 생략>")
	line = reServiceKey.ReplaceAllString(line, "${1}<인증키 생략>")
	if k := s.serviceKey(); len(k) >= 16 { // 공공데이터포털 인증키가 그대로 들어간 경우
		line = strings.ReplaceAll(line, k, "<인증키 생략>")
		line = strings.ReplaceAll(line, url.QueryEscape(k), "<인증키 생략>")
	}
	for _, d := range []struct{ path, name string }{{s.DataDir, "<데이터폴더>"}, {homeDir(), "<홈폴더>"}} {
		if len(d.path) < 3 {
			continue
		}
		line = strings.ReplaceAll(line, strings.ReplaceAll(d.path, `\`, `\\`), d.name) // 따옴표 안 (역슬래시 두 번)
		line = strings.ReplaceAll(line, d.path, d.name)
	}
	line = maskDrivePaths(line)
	line = reFileKV.ReplaceAllString(line, "file=<파일명 생략>")
	line = reDataFile.ReplaceAllString(line, "<파일명>.$1")
	return line
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// maskDrivePaths : 드라이브 문자(C:\\·D:/)·네트워크 공유(\\\\서버\\공유) 경로를 통째로 가린다.
// 따옴표 안이면 따옴표 끝·': ' 까지(공백이 든 폴더·파일 이름도), 따옴표 밖이면 공백까지 (뒤의 다른 값은 남긴다).
func maskDrivePaths(line string) string {
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(line); {
		c := line[i]
		if c == '"' && (i == 0 || line[i-1] != '\\') {
			inQuote = !inQuote
			b.WriteByte(c)
			i++
			continue
		}
		if isPathStart(line, i) {
			end := len(line)
			if inQuote {
				for j := i; j < len(line); j++ {
					if line[j] == '"' && line[j-1] != '\\' {
						end = j
						break
					}
				}
				if k := strings.Index(line[i:], ": "); k >= 0 && i+k < end {
					end = i + k
				}
			} else if k := strings.IndexAny(line[i:], " \t"); k >= 0 {
				end = i + k
			}
			b.WriteString("<경로>")
			i = end
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// isPathStart : 'C:\\'·'C:/' (앞이 글자가 아님) 또는 '\\\\서버' (역슬래시 2~4개 + 이름)
func isPathStart(line string, i int) bool {
	c := line[i]
	if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
		return i+2 < len(line) && line[i+1] == ':' && (line[i+2] == '\\' || line[i+2] == '/') && (i == 0 || !isWordByte(line[i-1]))
	}
	if c != '\\' || (i > 0 && line[i-1] == '\\') {
		return false
	}
	n := 0
	for i+n < len(line) && line[i+n] == '\\' {
		n++
	}
	if n < 2 || n > 4 || i+n >= len(line) {
		return false
	}
	next := line[i+n]
	return next != ' ' && next != '"' && next != '\t'
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// logTail : 기록 파일 끝 n줄 (가림 처리). name: dpr(앱) | dpr-cli(작업 스케줄러)
func (s *Service) logTail(name string, n int) []string {
	path := filepath.Join(s.DataDir, "logs", name+".log")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	seeked := false
	if st, err := f.Stat(); err == nil && st.Size() > 512<<10 {
		_, _ = f.Seek(st.Size()-512<<10, io.SeekStart) // 끝부분 512KB 만
		seeked = true
	}
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if seeked { // 중간에서 읽기 시작한 첫 줄은 잘려 있다
			seeked = false
			continue
		}
		lines = append(lines, s.sanitize(sc.Text()))
		if len(lines) > n*2 {
			lines = lines[len(lines)-n:]
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// BuildReport 는 보고서 본문(마크다운)을 만든다. maxLog: 로그 줄 수.
func (s *Service) BuildReport(note string, maxLog int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 오류 보고\n\n- 버전: v%s (%s/%s)\n- 보낸 시각: %s\n", AppVersion, runtime.GOOS, runtime.GOARCH, s.NowKST().Format("2006-01-02 15:04"))
	var schema int
	_ = s.DB.QueryRow("PRAGMA user_version").Scan(&schema)
	fmt.Fprintf(&b, "- DB 스키마: %d\n\n### 무슨 일이 있었나요\n\n%s\n\n", schema, strings.TrimSpace(orDefaultStr(note, "(메모 없음)")))
	b.WriteString("### 최근 자동 작업\n\n")
	if jobs, err := s.JobRuns(10); err == nil {
		for _, j := range jobs {
			msg := j.Message
			if len([]rune(msg)) > 160 {
				msg = string([]rune(msg)[:160]) + "…"
			}
			fmt.Fprintf(&b, "- %s %s %s %s\n", j.StartedAt, j.Job, j.Status, s.sanitize(msg))
		}
	}
	if maxLog > 0 {
		fmt.Fprintf(&b, "\n### 최근 기록 — 앱 (끝 %d줄, 파일명·경로·토큰은 가림)\n\n```\n", maxLog)
		for _, l := range s.logTail("dpr", maxLog) {
			b.WriteString(l + "\n")
		}
		b.WriteString("```\n")
		if cli := s.logTail("dpr-cli", min(maxLog, 80)); len(cli) > 0 { // 작업 스케줄러(창 없이 실행한 수집) 기록
			fmt.Fprintf(&b, "\n### 최근 기록 — 작업 스케줄러 (끝 %d줄)\n\n```\n", len(cli))
			for _, l := range cli {
				b.WriteString(l + "\n")
			}
			b.WriteString("```\n")
		}
	}
	b.WriteString("\n> 이 보고에는 약국 거래 자료(사입·조제·재고·가격)가 들어 있지 않습니다.\n")
	return b.String()
}

func orDefaultStr(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// SubmitReport : 보고서를 파일로 저장하고, 토큰이 있으면 이슈를 바로 만든다. 없거나 실패하면 브라우저로 열 주소를 돌려준다.
func (s *Service) SubmitReport(ctx context.Context, note string) (*ReportResult, error) {
	s.Log.Info("오류 보고 작성")
	full := s.BuildReport(note, 300)
	dir := filepath.Join(s.DataDir, "reports")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "report-"+time.Now().Format("20060102-150405")+".md")
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		return nil, err
	}
	applog.Prune(dir, "report-", 30) // 보고서는 최근 30개만
	res := &ReportResult{Path: path}
	repo := s.DB.GetSetting(SetUpdateRepo, DefaultUpdateRepo)
	title := "[오류보고] v" + AppVersion + " " + firstRunesStr(strings.TrimSpace(strings.SplitN(note, "\n", 2)[0]), 40)
	labels := []string{"오류보고", "민감자료"}
	if tok := s.DB.GetSetting(SetUpdateToken, ""); tok != "" && repoIsPrivate(ctx, repo, tok) {
		if u, err := createIssue(ctx, repo, tok, title, full, labels); err == nil {
			res.IssueURL, res.Message = u, "오류 보고를 보냈습니다"
			return res, nil
		} else {
			s.Log.Warn("오류 보고 자동 전송 실패 — 브라우저로 안내", "err", err)
		}
	}
	res.OpenURL = issueURL(repo, title, s.BuildReport(note, 0)+"\n(기록이 담긴 전체 보고서 파일 "+filepath.Base(path)+" 을 이 칸에 끌어다 붙여 주세요)", labels)
	res.Message = "GitHub 새 이슈 창을 엽니다. 보고서 파일(" + filepath.Base(path) + ")을 이슈에 끌어다 붙여 주세요."
	return res, nil
}

// issueURL : 브라우저로 여는 새 이슈 주소. 주소가 너무 길면 GitHub 가 열지 못하므로 본문을 줄이고(기록은 파일로 붙임),
// '~' 는 화면 쪽 주소 검사에서 거부되므로 %7E 로 바꾼다.
func issueURL(repo, title, body string, labels []string) string {
	const maxURL = 6000
	build := func(b string) string {
		q := url.Values{"title": {title}, "body": {b}, "labels": {strings.Join(labels, ",")}}
		return strings.ReplaceAll("https://github.com/"+repo+"/issues/new?"+q.Encode(), "~", "%7E")
	}
	u := build(body)
	for r := []rune(body); len(u) > maxURL && len(r) > 200; {
		r = r[:len(r)*3/4]
		u = build(string(r) + "\n…(줄임 — 보고서 파일을 붙여 주세요)")
	}
	return u
}

func firstRunesStr(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// repoIsPrivate : 저장소가 비공개인가 (확인하지 못하면 false). 기록이 담긴 보고서는 비공개 저장소(포크)에만 바로 올린다 —
// 공개 저장소에는 브라우저 새 이슈 창으로만 보낸다 (사용자가 내용을 확인한 뒤 올림).
func repoIsPrivate(ctx context.Context, repo, token string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updater.APIBase+"/repos/"+repo, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "DPR-report")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var out struct {
		Private bool `json:"private"`
	}
	return resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&out) == nil && out.Private
}

func createIssue(ctx context.Context, repo, token, title, body string, labels []string) (string, error) {
	payload, _ := json.Marshal(map[string]any{"title": title, "body": body, "labels": labels})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, updater.APIBase+"/repos/"+repo+"/issues", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "DPR-report")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("이슈 만들기 실패: HTTP %d (토큰에 Issues 쓰기 권한이 필요합니다)", resp.StatusCode)
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.HTMLURL, nil
}

// serviceKey : 저장된 공공데이터포털 인증키 (보고서에서 가리기 위해서만 읽는다)
func (s *Service) serviceKey() string {
	if s.DB == nil {
		return ""
	}
	return strings.TrimSpace(s.DB.GetSetting(SetDataGoKey, ""))
}
