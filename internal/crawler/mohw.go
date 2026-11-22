// Package crawler 는 보건복지부 '훈령/예규/고시/지침' 게시판에서 약제 급여 목록 고시를 수집한다.
//
// 수집 방식 (서버 부담 최소화, robots.txt 존중):
//  1. RSS(robots.txt 허용 경로)로 새 게시글 감지 — 하루 2~3회
//  2. 제목이 '약제 급여 목록 … 상한금액표' 인 '새' 게시글만 상세 페이지 1회 조회 → 첨부 목록 확인
//  3. '목록표' xlsx 첨부 1개만 내려받음 (이미 받은 게시글은 다시 받지 않음)
package crawler

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	DefaultRSS  = "https://www.mohw.go.kr/rss/board.es?mid=a10409020000&bid=0026"
	DefaultBase = "https://www.mohw.go.kr"
	maxDownload = 50 << 20
)

type Client struct {
	HTTP      *http.Client
	RSSURL    string
	Base      string
	UserAgent string
	Retries   int
	Pause     time.Duration // 연속 요청 사이 대기
	Backoff   time.Duration // 재시도 기본 간격 (시도마다 제곱으로 늘고 ±25% 흔듦, 서버의 Retry-After 를 따름)
}

func New() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		RSSURL:    DefaultRSS,
		Base:      DefaultBase,
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) DPR/0.1 (pharmacy price-cut helper)",
		Retries:   3,
		Pause:     1500 * time.Millisecond,
		Backoff:   2 * time.Second,
	}
}

type Item struct {
	Title       string    `json:"title"`
	Link        string    `json:"link"`
	Description string    `json:"description"`
	Author      string    `json:"author"`
	Published   time.Time `json:"published"`
	ListNo      string    `json:"listNo"`
}

type Attachment struct {
	Seq  int    `json:"seq"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (c *Client) get(ctx context.Context, rawURL string) (*http.Response, error) {
	var lastErr error
	var retryAfter time.Duration
	for attempt := 0; attempt < max(1, c.Retries); attempt++ {
		if attempt > 0 {
			wait := time.Duration(attempt*attempt) * c.Backoff
			wait += time.Duration(rand.Int64N(int64(wait/2)+1)) - wait/4 // ±25% (동시에 몰리지 않게)
			if retryAfter > wait {
				wait = retryAfter
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		retryAfter = 0
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("Accept-Language", "ko-KR,ko;q=0.9")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			if sec, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && sec > 0 {
				retryAfter = min(time.Duration(sec)*time.Second, 30*time.Second)
			}
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, rawURL)
		}
		return resp, nil
	}
	return nil, fmt.Errorf("요청 실패(%d회 시도): %w", c.Retries, lastErr)
}

type rssDoc struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			Description string `xml:"description"`
			Author      string `xml:"author"`
			PubDate     string `xml:"pubDate"`
		} `xml:"item"`
	} `xml:"channel"`
}

// ParseRSS 는 RSS XML 을 해석한다. (encoding/xml 은 외부 엔티티를 처리하지 않아 XXE 에 안전)
func ParseRSS(data []byte) ([]Item, error) {
	var doc rssDoc
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("RSS 해석 실패: %w", err)
	}
	var out []Item
	for _, it := range doc.Channel.Items {
		item := Item{Title: strings.TrimSpace(it.Title), Link: strings.TrimSpace(it.Link), Description: strings.TrimSpace(it.Description), Author: it.Author}
		for _, layout := range []string{time.RFC1123, time.RFC1123Z, "Mon, 2 Jan 2006 15:04:05 MST"} {
			if t, err := time.Parse(layout, strings.TrimSpace(it.PubDate)); err == nil {
				item.Published = t
				break
			}
		}
		if u, err := url.Parse(item.Link); err == nil {
			item.ListNo = u.Query().Get("list_no")
		}
		out = append(out, item)
	}
	return out, nil
}

func (c *Client) FetchRSS(ctx context.Context) ([]Item, error) {
	resp, err := c.get(ctx, c.RSSURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	return ParseRSS(data)
}

// ParseAttachments 는 게시글 상세 HTML 에서 첨부 다운로드 링크를 찾는다.
func ParseAttachments(page []byte, base string) ([]Attachment, error) {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, err
	}
	baseURL, _ := url.Parse(base)
	seen := map[int]bool{}
	var out []Attachment
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			var href, title string
			for _, a := range n.Attr {
				switch a.Key {
				case "href":
					href = a.Val
				case "title":
					title = a.Val
				}
			}
			if strings.Contains(href, "boardDownload") {
				if u, err := url.Parse(href); err == nil {
					abs := baseURL.ResolveReference(u)
					seq, _ := strconv.Atoi(abs.Query().Get("seq"))
					if !seen[seq] {
						seen[seq] = true
						out = append(out, Attachment{Seq: seq, Name: strings.TrimSpace(title), URL: abs.String()})
					}
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(doc)
	return out, nil
}

func (c *Client) FetchAttachments(ctx context.Context, link string) ([]Attachment, error) {
	atts, _, err := c.FetchPost(ctx, link)
	return atts, err
}

// FetchPost 는 게시글 상세 페이지 1번으로 첨부 목록과 작성일(YYYY-MM-DD, 못 읽으면 "")을 돌려준다.
func (c *Client) FetchPost(ctx context.Context, link string) ([]Attachment, string, error) {
	resp, err := c.get(ctx, link)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	page, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, "", err
	}
	atts, err := ParseAttachments(page, c.Base)
	return atts, ParsePostDate(page), err
}

var (
	rePostTag  = regexp.MustCompile(`<[^>]+>`)
	rePostDate = regexp.MustCompile(`작성일\s*[:：]?\s*(20\d{2})[-.](\d{2})[-.](\d{2})`)
)

// ParsePostDate : 게시글 상세 HTML 의 '작성일 2025-11-25' (없으면 "")
func ParsePostDate(page []byte) string {
	text := rePostTag.ReplaceAllString(string(page), " ")
	if m := rePostDate.FindStringSubmatch(text); m != nil {
		return m[1] + "-" + m[2] + "-" + m[3]
	}
	return ""
}

// 고시를 받는 게시판 (훈령/예규/고시/지침)
const (
	noticeBoardMid = "a10409020000"
	noticeBoardBid = "0026"
)

var reListNo = regexp.MustCompile(`^\d{4,12}$`)

// NoticePostLink 는 사용자가 넣은 게시글 주소(또는 게시글 번호)를 확인해 게시글 번호와 상세 페이지 주소를 돌려준다.
// 복지부 훈령/예규/고시/지침 게시판의 글만 받는다 — 다른 사이트·게시판 주소로 요청하지 않게.
func NoticePostLink(base, raw string) (listNo, link string, err error) {
	raw = strings.TrimSpace(raw)
	if reListNo.MatchString(raw) {
		listNo = raw
	} else {
		u, perr := url.Parse(raw)
		if perr != nil || u.Host == "" {
			return "", "", errors.New("복지부 게시글 주소(https://www.mohw.go.kr/board.es?…list_no=…)나 게시글 번호를 넣어 주세요")
		}
		if h := strings.ToLower(u.Hostname()); h != "www.mohw.go.kr" && h != "mohw.go.kr" {
			return "", "", errors.New("보건복지부(www.mohw.go.kr) 게시글 주소만 넣을 수 있습니다")
		}
		q := u.Query()
		if q.Get("mid") != noticeBoardMid || q.Get("bid") != noticeBoardBid {
			return "", "", errors.New("보건복지부 '훈령/예규/고시/지침' 게시판의 글 주소가 아닙니다")
		}
		listNo = q.Get("list_no")
		if !reListNo.MatchString(listNo) {
			return "", "", errors.New("주소에 게시글 번호(list_no)가 없습니다")
		}
	}
	link = strings.TrimRight(base, "/") + "/board.es?mid=" + noticeBoardMid + "&bid=" + noticeBoardBid + "&list_no=" + listNo + "&act=view"
	return listNo, link, nil
}

// NoticeTitleFromFile : 첨부 파일 이름에서 고시 제목 ('…일부개정고시(2025-188호)_별표1 목록표.xlsx' → '…일부개정고시(2025-188호)')
func NoticeTitleFromFile(name string) string {
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".xlsx"), ".XLSX")
	if i := strings.Index(name, "_별표"); i > 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}

// Download 는 첨부 파일을 받는다. 파일명은 Content-Disposition 에서 읽는다.
func (c *Client) Download(ctx context.Context, rawURL string) ([]byte, string, error) {
	resp, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxDownload {
		return nil, "", errors.New("첨부 파일이 너무 큽니다")
	}
	name := ""
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		name = params["filename"]
		if dec, err := url.QueryUnescape(name); err == nil {
			name = dec
		}
	}
	return data, name, nil
}

var titleNoise = strings.NewReplacer(" ", "", "「", "", "」", "", "『", "", "』", "", "\"", "", "'", "", "“", "", "”", "", "·", "", "ㆍ", "")

func normTitle(s string) string { return strings.ToLower(titleNoise.Replace(s)) }

// MatchTitle : include 키워드가 모두 포함되고 exclude 키워드가 하나도 없으면 true (공백·괄호 무시).
func MatchTitle(title string, include, exclude []string) bool {
	t := normTitle(title)
	for _, k := range include {
		if k = normTitle(k); k != "" && !strings.Contains(t, k) {
			return false
		}
	}
	for _, k := range exclude {
		if k = normTitle(k); k != "" && strings.Contains(t, k) {
			return false
		}
	}
	return true
}

// PickNoticeAttachment : '목록표' xlsx 우선, 없으면 아무 xlsx.
func PickNoticeAttachment(atts []Attachment) *Attachment {
	var anyXLSX *Attachment
	for i := range atts {
		name := strings.ToLower(atts[i].Name)
		if !strings.HasSuffix(name, ".xlsx") {
			continue
		}
		if strings.Contains(name, "목록표") {
			return &atts[i]
		}
		if anyXLSX == nil {
			anyXLSX = &atts[i]
		}
	}
	return anyXLSX
}

// SplitKeywords : "약제 급여 목록|상한금액표" → ["약제 급여 목록", "상한금액표"]
func SplitKeywords(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "|") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Pause : 요청 사이 쉬는 시간. 취소(앱 종료·시간 한도)되면 바로 돌아온다.
func Pause(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
