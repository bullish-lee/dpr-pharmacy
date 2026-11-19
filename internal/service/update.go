package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"dpr/internal/platform"
	"dpr/internal/updater"
)

// 새 버전 확인·적용 (GitHub Releases). 저장소는 설정에서 바꾼다 (공개 저장소는 토큰 없이, 비공개 포크는 토큰으로).

type UpdateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes"`
	URL       string `json:"url"`
	Asset     string `json:"asset"`
	Size      int64  `json:"size"`
	Skipped   bool   `json:"skipped"`
	Message   string `json:"message"`
}

const updateCheckEvery = 6 * time.Hour

func (s *Service) updateClient() *updater.Client {
	return updater.New(s.DB.GetSetting(SetUpdateRepo, DefaultUpdateRepo), s.DB.GetSetting(SetUpdateToken, ""))
}

// CheckUpdate : force=false 면 설정이 꺼져 있거나 6시간 안에 확인했으면 건너뛴다.
func (s *Service) CheckUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	info := &UpdateInfo{Current: AppVersion}
	if !force {
		if s.DB.GetSetting(SetUpdateCheck, "1") != "1" {
			info.Skipped, info.Message = true, "업데이트 확인이 꺼져 있습니다"
			return info, nil
		}
		if t, err := time.Parse(time.RFC3339, s.DB.GetSetting(SetUpdateChecked, "")); err == nil && time.Since(t) < updateCheckEvery {
			info.Skipped, info.Message = true, "최근에 확인했습니다"
			return info, nil
		}
	}
	if !s.updMu.TryLock() {
		info.Skipped, info.Message = true, "이미 확인 중입니다"
		return info, nil
	}
	defer s.updMu.Unlock()
	job := s.startJob("update")
	rel, err := s.updateClient().Latest(ctx)
	_ = s.DB.SetSetting(SetUpdateChecked, time.Now().Format(time.RFC3339))
	if err != nil {
		s.finishJob(job, "error", err.Error())
		return nil, err
	}
	z, sum, sig := rel.WindowsAssets()
	newer := updater.Compare(AppVersion, rel.Tag) < 0
	// 알리기 전에 작은 서명 파일만 받아 개발자 서명인지 확인한다 — 서명 없는 릴리스의 이름·설명(아무나 쓸 수 있음)을 화면에 띄우지 않는다
	verified := false
	if newer && z != nil && sum != nil && sig != nil {
		if sigData, err := s.updateClient().DownloadSmall(ctx, sig); err != nil {
			s.Log.Warn("서명 파일을 받지 못함", "err", err)
		} else if _, err := updater.VerifySigFields(string(sigData), rel.Tag, z.Name); err != nil {
			s.Log.Warn("서명이 맞지 않는 릴리스", "err", err)
		} else {
			verified = true
		}
	}
	if verified {
		info.Latest, info.Notes, info.URL = updater.NormVersion(rel.Tag), rel.Body, rel.HTMLURL
		info.Asset, info.Size = z.Name, z.Size
	}
	info.Available = verified
	switch {
	case newer && !verified:
		info.Message = "최신 릴리스에 맞는 개발자 서명이 없어 알리지 않습니다 (설치하지 않음)"
	case info.Available:
		info.Message = fmt.Sprintf("새 버전 v%s 이 있습니다 (현재 v%s)", info.Latest, AppVersion)
		if s.DB.GetSetting("update_notified", "") != info.Latest { // 같은 버전은 한 번만 알린다
			_ = s.DB.SetSetting("update_notified", info.Latest)
			s.notify("info", "새 버전이 있습니다", info.Message+". 설정 → 업데이트에서 받을 수 있어요.", "settings")
		}
	default:
		info.Message = fmt.Sprintf("최신 버전입니다 (v%s)", AppVersion)
	}
	s.finishJob(job, "ok", info.Message)
	return info, nil
}

// ApplyUpdate : 내려받기 → 체크섬·서명 확인 → 데이터 백업 → 실행 파일 교체. 다시 시작은 호출한 쪽(app.go)이 한다.
// want 는 사용자가 확인한 버전 — 그사이 '최신'이 다른 릴리스로 바뀌었으면 설치하지 않는다.
func (s *Service) ApplyUpdate(ctx context.Context, exePath, want string) (*UpdateInfo, error) {
	if !platform.IsWindows() {
		return nil, errors.New("자동 업데이트는 Windows 에서만 동작합니다 (맥은 개발용)")
	}
	if !s.updMu.TryLock() {
		return nil, errors.New("업데이트를 이미 진행 중입니다")
	}
	defer s.updMu.Unlock()
	job := s.startJob("update")
	fail := func(err error) (*UpdateInfo, error) {
		s.finishJob(job, "error", err.Error())
		s.Log.Error("업데이트 실패", "err", err)
		return nil, err
	}
	rel, z, exe, err := fetchVerifiedUpdate(ctx, s.updateClient(), want)
	if err != nil {
		return fail(err)
	}
	if _, err := s.Backup(); err != nil { // 교체 전에 데이터 백업
		return fail(fmt.Errorf("업데이트 전 백업 실패: %w", err))
	}
	if err := updater.Replace(exePath, exe); err != nil {
		return fail(err)
	}
	v := updater.NormVersion(rel.Tag)
	info := &UpdateInfo{Current: AppVersion, Latest: v, Available: true, Asset: z.Name, Size: z.Size,
		Message: fmt.Sprintf("v%s 로 업데이트했습니다. 다시 시작합니다.", v)}
	s.finishJob(job, "ok", info.Message)
	s.Log.Info("업데이트 적용", "from", AppVersion, "to", rel.Tag)
	return info, nil
}

// fetchVerifiedUpdate : 최신 릴리스를 받아 체크섬·개발자 서명을 확인하고 zip 안의 DPR.exe 를 꺼낸다 (교체는 하지 않는다).
// want(사용자가 확인한 버전)와 지금 최신이 다르면 받지 않는다.
func fetchVerifiedUpdate(ctx context.Context, c *updater.Client, want string) (*updater.Release, *updater.Asset, []byte, error) {
	rel, err := c.Latest(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	if updater.Compare(AppVersion, rel.Tag) >= 0 {
		return nil, nil, nil, errors.New("이미 최신 버전입니다")
	}
	if w := updater.NormVersion(want); w == "" || w != updater.NormVersion(rel.Tag) {
		return nil, nil, nil, errors.New("확인한 버전과 지금 최신 릴리스가 다릅니다 — 업데이트 확인을 다시 해 주세요")
	}
	z, sum, sig := rel.WindowsAssets()
	if z == nil || sum == nil {
		return nil, nil, nil, errors.New("릴리즈에 Windows zip 또는 체크섬 파일이 없습니다")
	}
	if sig == nil {
		return nil, nil, nil, fmt.Errorf("%w: 서명 파일(.sig)이 없음", updater.ErrBadSignature)
	}
	zipData, err := c.Download(ctx, z)
	if err != nil {
		return nil, nil, nil, err
	}
	sumData, err := c.DownloadSmall(ctx, sum)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := updater.VerifySHA256(zipData, string(sumData)); err != nil {
		return nil, nil, nil, err
	}
	sigData, err := c.DownloadSmall(ctx, sig)
	if err != nil {
		return nil, nil, nil, err
	}
	// 개발자 서명 확인 — 버전·파일 이름까지 맞아야 한다 (옛 버전 파일을 새 버전인 척 올린 것도 거부)
	if err := updater.VerifyRelease(zipData, string(sigData), rel.Tag, z.Name); err != nil {
		return nil, nil, nil, err
	}
	exe, err := updater.ExtractFile(zipData, "DPR.exe")
	if err != nil {
		return nil, nil, nil, err
	}
	return rel, z, exe, nil
}
