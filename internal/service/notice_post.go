package service

import (
	"context"
	"errors"
	"fmt"

	"dpr/internal/crawler"
)

// 지난 고시 하나 가져오기 — 사용자가 복지부 게시글 주소(또는 번호)를 넣을 때만.
// 자동 수집은 RSS 에 보이는 최근 글만 보므로, 프로그램을 쓰기 전에 고시됐지만 시행일이 앞으로 남은 인하
// (예: 2025-188호 별지5 '26.11.23. 시행 젤잔즈 가산 종료)는 이렇게 직접 가져온다.
// 복지부 robots.txt 는 게시판(/board.es) 자동 수집을 막으므로 목록을 거슬러 훑지 않는다 — 글 하나에 요청 2번(상세·첨부)뿐.

type NoticePostResult struct {
	NoticeID int64  `json:"noticeId"`
	Already  bool   `json:"already"` // 이미 가져온 고시 (요청하지 않음)
	Message  string `json:"message"`
}

func (s *Service) ImportNoticePost(ctx context.Context, input string) (*NoticePostResult, error) {
	c := NewCrawler()
	listNo, link, err := crawler.NoticePostLink(c.Base, input)
	if err != nil {
		return nil, err
	}
	var id int64
	var status string
	if s.DB.QueryRow("SELECT id, status FROM notice WHERE list_no = ?", listNo).Scan(&id, &status) == nil && (status == "parsed" || status == "duplicate") {
		return &NoticePostResult{NoticeID: id, Already: true, Message: "이미 가져온 고시입니다"}, nil
	}
	if !s.crawlMu.TryLock() { // 자동 수집과 겹치지 않게 (같은 서버에 동시에 요청하지 않음)
		return nil, errors.New("고시 확인이 진행 중입니다 — 잠시 뒤 다시 눌러 주세요")
	}
	defer s.crawlMu.Unlock()

	atts, published, err := c.FetchPost(ctx, link)
	if err != nil {
		return nil, fmt.Errorf("게시글을 열지 못했습니다: %w", err)
	}
	att := crawler.PickNoticeAttachment(atts)
	if att == nil {
		return nil, errors.New("이 게시글에 '목록표' 엑셀 첨부가 없습니다 — 약제 급여 목록 고시 게시글인지 확인하세요")
	}
	if err := crawler.Pause(ctx, c.Pause); err != nil {
		return nil, err
	}
	data, fname, err := c.Download(ctx, att.URL)
	if err != nil {
		return nil, fmt.Errorf("목록표를 내려받지 못했습니다: %w", err)
	}
	if att.Name != "" {
		fname = att.Name
	}
	title := crawler.NoticeTitleFromFile(fname)
	id, msg, dup, err := s.ingestNoticeFile("mohw", listNo, title, link, published, fname, data)
	if err != nil {
		return nil, fmt.Errorf("목록표 해석 실패: %w", err)
	}
	if dup {
		return &NoticePostResult{NoticeID: id, Already: true, Message: "같은 목록표를 이미 가져왔습니다 — 게시글만 연결했습니다"}, nil
	}
	msg += s.heldSummary(id)
	s.Log.Info("지난 고시 가져옴", "list_no", listNo, "notice", id, "published", published)
	if s.OnDataChanged != nil {
		s.OnDataChanged("notice")
	}
	return &NoticePostResult{NoticeID: id, Message: msg}, nil
}
