// noticeseed 는 복지부 고시 게시글에서 '별표1 목록표' 엑셀을 내려받아 internal/seed/notices 에 넣는다 (개발자용).
// 프로그램에 지난 고시를 미리 넣어 배포하기 위한 도구다 — 앱의 수집 코드(crawler)를 그대로 쓴다.
//
//	go run ./tools/noticeseed <게시글 번호 또는 주소> ...
//
// 글 하나에 요청 2번(상세·첨부), 글 사이에 3초씩 쉰다. 이미 받은 글(manifest.json 에 있는 번호)은 다시 받지 않는다.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"dpr/internal/crawler"
)

// Entry : manifest.json 의 고시 하나
type Entry struct {
	ListNo    string `json:"listNo"`
	Published string `json:"published"` // 게시글 작성일
	Title     string `json:"title"`
	File      string `json:"file"`     // 이 폴더 안의 파일 이름 (<글 번호>.xlsx)
	FileName  string `json:"fileName"` // 원래 첨부 파일 이름
	SHA256    string `json:"sha256"`
}

const dir = "internal/seed/notices"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "사용: go run ./tools/noticeseed <게시글 번호 또는 주소> ...")
		os.Exit(2)
	}
	var entries []Entry
	if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.ListNo] = true
	}
	c := crawler.New()
	ctx := context.Background()
	failed := 0
	for i, in := range os.Args[1:] {
		listNo, link, err := crawler.NoticePostLink(c.Base, in)
		if err != nil {
			fmt.Fprintln(os.Stderr, in, "—", err)
			failed++
			continue
		}
		if have[listNo] {
			fmt.Println(listNo, "이미 있음")
			continue
		}
		if i > 0 {
			time.Sleep(3 * time.Second)
		}
		atts, published, err := c.FetchPost(ctx, link)
		if err != nil {
			fmt.Fprintln(os.Stderr, listNo, "— 게시글:", err)
			failed++
			continue
		}
		att := crawler.PickNoticeAttachment(atts)
		if att == nil {
			fmt.Fprintln(os.Stderr, listNo, "— 목록표 첨부 없음")
			failed++
			continue
		}
		time.Sleep(2 * time.Second)
		data, fname, err := c.Download(ctx, att.URL)
		if err != nil {
			fmt.Fprintln(os.Stderr, listNo, "— 내려받기:", err)
			failed++
			continue
		}
		if att.Name != "" {
			fname = att.Name
		}
		sum := sha256.Sum256(data)
		file := listNo + ".xlsx"
		if err := os.WriteFile(filepath.Join(dir, file), data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		entries = append(entries, Entry{ListNo: listNo, Published: published, Title: crawler.NoticeTitleFromFile(fname), File: file, FileName: fname, SHA256: hex.EncodeToString(sum[:])})
		have[listNo] = true
		fmt.Printf("%s %s %s (%d KB)\n", listNo, published, fname, len(data)/1024)
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].Published < entries[b].Published })
	b, _ := json.MarshalIndent(entries, "", " ")
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("manifest: %d건 (실패 %d건)\n", len(entries), failed)
	if failed > 0 {
		os.Exit(1)
	}
}
