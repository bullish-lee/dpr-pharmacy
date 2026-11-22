package crawler

import "testing"

func TestNoticePostLink(t *testing.T) {
	const want = "https://www.mohw.go.kr/board.es?mid=a10409020000&bid=0026&list_no=1487997&act=view"
	for _, in := range []string{
		"1487997",
		" https://www.mohw.go.kr/board.es?mid=a10409020000&bid=0026&list_no=1487997&act=view ",
		"https://mohw.go.kr/board.es?act=view&bid=0026&list_no=1487997&mid=a10409020000&tag=&nPage=2",
	} {
		no, link, err := NoticePostLink(DefaultBase, in)
		if err != nil || no != "1487997" || link != want {
			t.Errorf("%q → %q %q %v", in, no, link, err)
		}
	}
	for _, in := range []string{
		"",
		"abc",
		"https://example.com/board.es?mid=a10409020000&bid=0026&list_no=1487997",     // 다른 사이트
		"https://www.mohw.go.kr/board.es?mid=a10503000000&bid=0027&list_no=1487997",  // 다른 게시판
		"https://www.mohw.go.kr/board.es?mid=a10409020000&bid=0026&list_no=1487997x", // 번호 아님
		"https://www.mohw.go.kr/board.es?mid=a10409020000&bid=0026&act=list&nPage=2", // 목록 페이지
	} {
		if _, _, err := NoticePostLink(DefaultBase, in); err == nil {
			t.Errorf("%q 를 받아들임", in)
		}
	}
}

func TestParsePostDateAndTitle(t *testing.T) {
	page := []byte(`<ul><li><strong>작성일</strong><span>2025-11-25</span></li><li>조회수 120</li></ul>`)
	if got := ParsePostDate(page); got != "2025-11-25" {
		t.Errorf("작성일: %q", got)
	}
	if got := ParsePostDate([]byte(`<p>본문 2025-11-25</p>`)); got != "" {
		t.Errorf("작성일 표시가 없으면 빈 값: %q", got)
	}
	if got := NoticeTitleFromFile("약제 급여 목록 및 급여 상한금액표 일부개정고시(2025-188호)_별표1 목록표.xlsx"); got != "약제 급여 목록 및 급여 상한금액표 일부개정고시(2025-188호)" {
		t.Errorf("제목: %q", got)
	}
}
