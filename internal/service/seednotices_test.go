package service

import (
	"testing"

	"dpr/internal/seed"
)

// 프로그램에 넣어 둔 지난 고시가 모두 읽히고, 한 번만 들어간다
func TestSeedPastNotices(t *testing.T) {
	list, err := seed.Notices()
	if err != nil || len(list) < 10 {
		t.Fatalf("넣어 둔 고시 %d건 %v", len(list), err)
	}
	for i, n := range list {
		if n.ListNo == "" || n.Published == "" || n.FileName == "" {
			t.Fatalf("%d번째 항목이 비어 있음: %+v", i, n)
		}
		if i > 0 && list[i-1].Published > n.Published {
			t.Fatal("게시일 순이어야 함 (오래된 것부터 넣어야 '변경 전 가격'을 이어 받는다)")
		}
		data, err := seed.Data(n)
		if err != nil || shaHex(data) != n.SHA256 {
			t.Fatalf("%s: 파일이 목록과 다름 (%v)", n.ListNo, err)
		}
	}
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	added, err := s.SeedPastNotices()
	if err != nil || added != len(list) {
		t.Fatalf("넣은 고시 %d / %d건 %v", added, len(list), err)
	}
	var notices, noNumber, changes, downs, news, dels int
	_ = s.DB.QueryRow("SELECT COUNT(*), SUM(notice_no = '') FROM notice WHERE source = 'mohw' AND status = 'parsed'").Scan(&notices, &noNumber)
	_ = s.DB.QueryRow(`SELECT COUNT(*), SUM(detail = 'price_down'), SUM(kind = 'new'), SUM(kind = 'delete') FROM price_change`).Scan(&changes, &downs, &news, &dels)
	if notices != len(list) || noNumber != 0 {
		t.Fatalf("고시 %d건 (번호 없는 것 %d건)", notices, noNumber)
	}
	if downs == 0 || news == 0 || dels == 0 {
		t.Fatalf("인하 %d · 신설 %d · 삭제 %d (전체 %d)", downs, news, dels, changes)
	}
	t.Logf("고시 %d건: 항목 %d (인하 %d · 신설 %d · 삭제 %d)", notices, changes, downs, news, dels)
	// 다시 불러도 넣지 않는다 / 판 표시를 지워도 같은 고시는 다시 넣지 않는다
	if again, _ := s.SeedPastNotices(); again != 0 {
		t.Fatalf("두 번째: %d", again)
	}
	_ = s.DB.SetSetting(setSeedNotices, "")
	if again, _ := s.SeedPastNotices(); again != 0 {
		t.Fatalf("판 표시를 지운 뒤: %d", again)
	}
}
