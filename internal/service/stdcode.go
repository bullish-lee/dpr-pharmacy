package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dpr/internal/applog"
	"dpr/internal/crawler"
)

// 표준코드(바코드 ↔ 보험코드) 매핑 자동 갱신 — 하루 한 번 새 판이 있는지만 보고, 바뀐 날에만 내려받는다.

type StdCodeResult struct {
	Skipped  bool                `json:"skipped"` // 오늘 이미 확인함 / 다른 확인 실행 중
	Updated  bool                `json:"updated"`
	Snapshot crawler.StdSnapshot `json:"snapshot"`
	Rows     int                 `json:"rows"`
	Message  string              `json:"message"`
}

const setStdBadUDDI = "stdcode_bad_uddi" // 형식을 알아보지 못한 판 (새 판이 나오면 다시 시도)

// 테스트에서 가짜 서버로 바꾼다 (빈 값 = 실제 주소)
var stdDocsURL, stdBaseURL = "", ""

const stdCheckEvery = 20 * time.Hour

// CheckStdCode : force=false 면 20시간 안에 확인했으면 건너뛴다.
func (s *Service) CheckStdCode(ctx context.Context, force bool) (*StdCodeResult, error) {
	if !s.stdMu.TryLock() {
		return &StdCodeResult{Skipped: true, Message: "이미 확인 중입니다"}, nil
	}
	defer s.stdMu.Unlock()
	if !force {
		if t, err := time.Parse(time.RFC3339, s.DB.GetSetting(SetStdChecked, "")); err == nil && time.Since(t) < stdCheckEvery {
			return &StdCodeResult{Skipped: true, Message: "오늘 이미 확인했습니다"}, nil
		}
		if t, err := time.Parse(time.RFC3339, s.DB.GetSetting("stdcode_failed_at", "")); err == nil && time.Since(t) < time.Hour {
			return &StdCodeResult{Skipped: true, Message: "최근 실패 — 1시간 뒤 다시 시도합니다"}, nil
		}
	}
	job, ok := s.tryStartJob("stdcode") // 앱과 작업 스케줄러가 동시에 55MB 를 받지 않게
	if !ok {
		return &StdCodeResult{Skipped: true, Message: "다른 창(작업 스케줄러)에서 확인 중입니다"}, nil
	}
	c := NewCrawler()
	failed := func(err error) (*StdCodeResult, error) { // 실패하면 1시간 뒤 다시 (성공 기록은 남기지 않음)
		if ctx.Err() != nil { // 앱 종료·시간 한도로 중단 → 실패로 세지 않고 다음에 바로 다시
			s.finishJob(job, "error", "중단됨 (프로그램 종료 또는 시간 초과)")
			return nil, err
		}
		_ = s.DB.SetSetting("stdcode_failed_at", time.Now().Format(time.RFC3339))
		s.finishJob(job, "error", err.Error())
		return nil, err
	}
	snap, err := c.LatestStdSnapshot(ctx, stdDocsURL)
	if err != nil {
		return failed(err)
	}
	res := &StdCodeResult{Snapshot: snap}
	var official int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM gtin_map WHERE source = 'official'").Scan(&official)
	// 직접 올린 표준코드 파일이 같은 판이면(파일명의 연·월이 같으면) 다시 받지 않는다
	if s.DB.GetSetting(SetStdUDDI, "") == "" && official > 0 && len(snap.Date) >= 7 && strings.HasPrefix(s.DB.GetSetting(SetStdDate, ""), snap.Date[:7]) {
		_ = s.DB.SetSetting(SetStdUDDI, snap.UDDI)
	}
	if snap.UDDI == s.DB.GetSetting(SetStdUDDI, "") && official > 0 {
		_ = s.DB.SetSetting(SetStdChecked, time.Now().Format(time.RFC3339))
		res.Message = fmt.Sprintf("표준코드 매핑이 최신입니다 (%s 판)", dateOrUnknown(snap.Date))
		s.finishJob(job, "ok", res.Message)
		return res, nil
	}
	if snap.UDDI != "" && snap.UDDI == s.DB.GetSetting(setStdBadUDDI, "") { // 이 판은 형식을 알아보지 못했다 → 새 판이 나올 때까지 다시 받지 않는다
		_ = s.DB.SetSetting(SetStdChecked, time.Now().Format(time.RFC3339))
		res.Message = fmt.Sprintf("표준코드 %s 판은 파일 형식을 알아보지 못해 건너뜁니다 — '가져오기'에서 직접 올려 열을 지정하세요", dateOrUnknown(snap.Date))
		s.finishJob(job, "error", res.Message)
		return res, nil
	}
	name, data, err := c.DownloadStdCode(ctx, stdBaseURL, snap)
	if err != nil {
		if errors.Is(err, crawler.ErrNeedCaptcha) {
			s.notify("warn", "표준코드 자동 내려받기 실패", err.Error(), "imports")
			_ = s.DB.SetSetting(SetStdChecked, time.Now().Format(time.RFC3339)) // 보안문자면 오늘은 더 시도하지 않는다
		}
		return failed(err)
	}
	dir := filepath.Join(s.DataDir, "raw", "stdcode")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return failed(err)
	}
	applog.Prune(dir, "", 2) // 원본 CSV(약 55MB)는 최근 2개만
	imp, err := s.Import(ImportRequest{Kind: "gtin", Path: path})
	if err != nil {
		return failed(err)
	}
	if imp.NeedsMapping || imp.Rows == 0 { // 열 이름이 바뀐 파일 — '반영했다'고 기록하면 매핑이 조용히 낡는다
		msg := fmt.Sprintf("표준코드 %s 판 파일의 형식(열 이름)을 알아보지 못했습니다. '가져오기'에서 이 파일을 직접 올려 열을 지정해 주세요: %s",
			dateOrUnknown(snap.Date), filepath.Base(path))
		_ = s.DB.SetSetting(setStdBadUDDI, snap.UDDI)
		_ = s.DB.SetSetting(SetStdChecked, time.Now().Format(time.RFC3339))
		s.notify("warn", "표준코드 자동 반영 실패", msg, "imports")
		s.finishJob(job, "error", msg)
		s.Log.Warn("표준코드 형식 불일치", "date", snap.Date, "needs_mapping", imp.NeedsMapping, "rows", imp.Rows)
		return nil, errors.New(msg)
	}
	_ = s.DB.SetSetting(SetStdChecked, time.Now().Format(time.RFC3339))
	_ = s.DB.SetSetting(SetStdUDDI, snap.UDDI)
	_ = s.DB.SetSetting(SetStdDate, snap.Date)
	res.Updated, res.Rows = true, imp.Rows
	res.Message = fmt.Sprintf("표준코드 매핑 새 판(%s)을 반영했습니다: %s건", dateOrUnknown(snap.Date), comma(imp.Rows))
	s.finishJob(job, "ok", res.Message)
	s.notify("info", "표준코드 매핑 갱신", res.Message, "imports")
	s.Log.Info("표준코드 갱신", "date", snap.Date, "rows", imp.Rows)
	return res, nil
}

var reStdFileYM = regexp.MustCompile(`(20\d{2})[.\-_]?(0[1-9]|1[0-2])`)

// noteManualStdFile : 표준코드 파일을 직접 올리면 파일명('…_2026.08.csv')의 연·월을 판 기준일로 기록한다.
func (s *Service) noteManualStdFile(name string) {
	if m := reStdFileYM.FindStringSubmatch(name); m != nil {
		_ = s.DB.SetSetting(SetStdDate, m[1]+"-"+m[2])
		_ = s.DB.SetSetting(SetStdUDDI, "") // 어느 판인지는 다음 확인 때 연·월로 맞춘다
	}
}

func dateOrUnknown(d string) string {
	if d == "" {
		return "날짜 미상"
	}
	return strings.ReplaceAll(d, "-", ".")
}
