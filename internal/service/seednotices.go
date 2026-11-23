package service

import (
	"dpr/internal/crawler"
	"dpr/internal/seed"
)

const setSeedNotices = "seed_notices_version"

// SeedPastNotices 는 프로그램에 넣어 둔 지난 1년 치 고시를 한 번 넣는다 (판이 바뀌면 다시 확인).
// 자동 수집과 같은 방법(ingestNoticeFile)으로 읽으므로, 이미 가져온 고시(같은 게시글·같은 파일)는 건너뛴다.
// 돌려주는 값: 새로 넣은 고시 수.
func (s *Service) SeedPastNotices() (int, error) {
	if s.DB.GetSetting(setSeedNotices, "") == seed.Version {
		return 0, nil
	}
	list, err := seed.Notices()
	if err != nil {
		return 0, err
	}
	s.importMu.Lock()
	defer s.importMu.Unlock()
	base := crawler.New().Base
	added := 0
	for _, n := range list {
		var have int
		_ = s.DB.QueryRow("SELECT COUNT(*) FROM notice WHERE list_no = ? AND status IN ('parsed', 'duplicate')", n.ListNo).Scan(&have)
		if have > 0 {
			continue
		}
		data, err := seed.Data(n)
		if err != nil {
			return added, err
		}
		_, link, _ := crawler.NoticePostLink(base, n.ListNo)
		if _, _, dup, err := s.ingestNoticeFile("mohw", n.ListNo, n.Title, link, n.Published, n.FileName, data); err != nil {
			s.Log.Warn("넣어 둔 고시를 읽지 못함", "list_no", n.ListNo, "err", err) // 하나가 실패해도 나머지는 넣는다
			continue
		} else if !dup {
			added++
		}
	}
	if err := s.DB.SetSetting(setSeedNotices, seed.Version); err != nil {
		return added, err
	}
	if added > 0 {
		s.Log.Info("지난 고시(프로그램에 든 것) 반영", "added", added, "version", seed.Version)
		if s.OnDataChanged != nil {
			s.OnDataChanged("notice")
		}
	}
	return added, nil
}
