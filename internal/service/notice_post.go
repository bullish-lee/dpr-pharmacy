package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// NoticePostsResult : 지난 고시 여러 개 가져오기 결과
type NoticePostsResult struct {
	Imported int      `json:"imported"` // 새로 가져온 고시
	Already  int      `json:"already"`  // 이미 있던 고시
	Failed   []string `json:"failed"`   // 실패한 줄과 이유
	Message  string   `json:"message"`
}

// SplitPostInputs : 여러 줄(또는 쉼표·공백으로 나눈) 게시글 주소·번호를 하나씩 나눈다 (빈 것·중복 제거, 순서 유지)
func SplitPostInputs(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t' }) {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// ImportNoticePosts 는 게시글 주소(또는 번호) 여러 개를 차례로 가져온다 — 지난 1년 고시를 한 번에 넣을 때.
// 글마다 사이를 두고 하나씩 받는다 (서버에 몰아서 요청하지 않음). 한 줄이 실패해도 나머지는 계속한다.
func (s *Service) ImportNoticePosts(ctx context.Context, inputs []string) (*NoticePostsResult, error) {
	const maxPosts = 40
	if len(inputs) == 0 {
		return nil, errors.New("게시글 주소나 번호를 넣어 주세요")
	}
	if len(inputs) > maxPosts {
		return nil, fmt.Errorf("한 번에 %d개까지만 가져옵니다 (%d개 입력)", maxPosts, len(inputs))
	}
	res := &NoticePostsResult{}
	pause := NewCrawler().Pause
	for i, in := range inputs {
		if i > 0 {
			if err := crawler.Pause(ctx, pause); err != nil {
				return res, err
			}
		}
		r, err := s.ImportNoticePost(ctx, in)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			res.Failed = append(res.Failed, fmt.Sprintf("%s — %v", in, err))
		case r.Already:
			res.Already++
		default:
			res.Imported++
		}
	}
	res.Message = fmt.Sprintf("고시 %d건을 가져왔습니다", res.Imported)
	if res.Already > 0 {
		res.Message += fmt.Sprintf(" · 이미 있던 것 %d건", res.Already)
	}
	if len(res.Failed) > 0 {
		res.Message += fmt.Sprintf(" · 실패 %d건", len(res.Failed))
	}
	return res, nil
}
