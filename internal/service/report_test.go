package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dpr/internal/updater"
)

func TestReport(t *testing.T) {
	var got map[string]any
	private := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/o/r" && r.Header.Get("Authorization") == "Bearer github_pat_TEST" {
			fmt.Fprintf(w, `{"full_name":"o/r","private":%v}`, private)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/repos/o/r/issues" && r.Header.Get("Authorization") == "Bearer github_pat_TEST" {
			_ = json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"html_url":"https://github.com/o/r/issues/7"}`))
			return
		}
		http.Error(w, "no", 403)
	}))
	defer srv.Close()
	updater.APIBase = srv.URL
	defer func() { updater.APIBase = "https://api.github.com" }()
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	logDir := filepath.Join(s.DataDir, "logs")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.WriteFile(filepath.Join(logDir, "dpr.log"), []byte(`level=INFO msg=가져오기 완료 kind=purchase file="가나약국_사입현황.xlsx" rows=10
level=WARN msg=x path=`+s.DataDir+`/raw token=github_pat_ABCDEF123
`), 0o644)
	rep := s.BuildReport("사입 올리다 오류", 50)
	for _, bad := range []string{"가나약국", "github_pat_ABCDEF123", s.DataDir} {
		if strings.Contains(rep, bad) {
			t.Fatalf("보고서에 가려야 할 내용이 남음: %q\n%s", bad, rep)
		}
	}
	// 토큰 없음 → 브라우저 주소
	_ = s.DB.SetSetting(SetUpdateRepo, "o/r")
	res, err := s.SubmitReport(context.Background(), "사입 올리다 오류")
	if err != nil || res.OpenURL == "" || !strings.Contains(res.OpenURL, "github.com/o/r/issues/new") {
		t.Fatalf("%+v %v", res, err)
	}
	// 토큰 있음 → 이슈 바로 생성 (라벨 오류보고·민감자료)
	_ = s.DB.SetSetting(SetUpdateToken, "github_pat_TEST")
	res, err = s.SubmitReport(context.Background(), "사입 올리다 오류")
	if err != nil || res.IssueURL != "https://github.com/o/r/issues/7" {
		t.Fatalf("%+v %v", res, err)
	}
	if labels, _ := got["labels"].([]any); len(labels) != 2 || labels[0] != "오류보고" || labels[1] != "민감자료" {
		t.Fatalf("labels %v", got["labels"])
	}
	// 공개 저장소면 기록이 담긴 보고서를 바로 올리지 않고 브라우저 창으로
	private = false
	got = nil
	res, err = s.SubmitReport(context.Background(), "사입 올리다 오류")
	if err != nil || res.IssueURL != "" || res.OpenURL == "" || got != nil {
		t.Fatalf("공개 저장소에 바로 올림: %+v %v", res, err)
	}
}

// 기록(slog)이 따옴표로 감싸며 역슬래시를 두 번 쓴 Windows 경로·공백 든 파일 이름·작업 스케줄러 기록까지 가린다
func TestReportMasksEscapedWindowsPaths(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	logDir := filepath.Join(s.DataDir, "logs")
	_ = os.MkdirAll(logDir, 0o755)
	write := func(name string, fn func(l *slog.Logger)) {
		f, err := os.Create(filepath.Join(logDir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fn(slog.New(slog.NewTextHandler(f, nil)))
	}
	write("dpr.log", func(l *slog.Logger) {
		l.Error("화면 오류", "kind", "action", "msg", `open C:\Users\testuser\Desktop\테스트약국 10월 사입.xlsx: The process cannot access the file`)
		l.Warn("원본 저장 실패", "path", `D:\약국 자료\가나약국\재고 현황.xlsx`)
		l.Info("가져오기", "file", "테스트약국 사입.xlsx", "rows", 10)
		l.Info("읽음", "msg", "가나약국_조제현황 11월.csv 를 읽었습니다")
	})
	write("dpr-cli.log", func(l *slog.Logger) {
		l.Warn("수집 실패", "err", `token github_pat_SECRET123 C:\Users\testuser\AppData\Roaming\DPR\raw`)
	})
	rep := s.BuildReport("10월~11월 사입 올리다 오류", 50)
	for _, bad := range []string{"testuser", "테스트약국", "가나약국", "약국 자료", "재고 현황", "SECRET123"} {
		if strings.Contains(rep, bad) {
			t.Fatalf("보고서에 가려야 할 내용이 남음: %q\n%s", bad, rep)
		}
	}
	if !strings.Contains(rep, "<경로>") || !strings.Contains(rep, "작업 스케줄러") {
		t.Fatalf("경로 가림·작업 스케줄러 기록이 있어야 함:\n%s", rep)
	}
	// 네트워크 공유 경로·따옴표 밖 경로(뒤 값은 남김)·'.xls 형식' 같은 설명(가리지 않음)
	for _, tc := range []struct{ in, mustNot, must string }{
		{`level=WARN msg="열기 실패" err="open \\\\NAS\\가상공유\\재고 10월.xlsx: denied"`, "가상공유", "<경로>"},
		{`level=INFO msg=백업 path=C:\DPR\backup rows=10`, `DPR`, "rows=10"},
		{`level=WARN msg="엑셀 파일을 열 수 없습니다 — .xls 형식은 다시 저장해 주세요"`, "<파일명>", ".xls 형식"},
	} {
		got := s.sanitize(tc.in)
		if strings.Contains(got, tc.mustNot) || !strings.Contains(got, tc.must) {
			t.Fatalf("가림 결과가 다름:\n  입력 %s\n  결과 %s", tc.in, got)
		}
	}
	u := issueURL("o/r", "제목", strings.Repeat("긴 내용 ~ ", 3000), []string{"오류보고"})
	if len(u) > 6100 || strings.Contains(u, "~") {
		t.Fatalf("이슈 주소 길이 %d, '~' 포함 %v", len(u), strings.Contains(u, "~"))
	}
}
