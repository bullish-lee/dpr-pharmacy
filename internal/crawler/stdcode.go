package crawler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 심평원 「약가마스터_의약품표준코드」(바코드 ↔ 보험코드) 새 판 확인·내려받기.
//
// 공식 데이터는 공공데이터포털 15067462 하나뿐이고 새 판은 연 1~4회 나온다(2021-10, 2022-10, 2023-10, 2024-10, 2025-10, 2026-08).
// 그래서 '매일 확인'은 다음처럼 가볍게 한다.
//  1. 하루 한 번 odcloud 문서(약 18KB, 키 불필요)만 받아 최신 판(uddi)을 본다.
//  2. 판이 바뀐 날에만 파일(약 55MB)을 내려받는다: 목록 조회 → 다운로드 한도 확인 → 내려받기.
//  3. 보안문자(과도한 요청)가 뜨면 즉시 멈추고 사용자에게 직접 내려받아 달라고 알린다 — 다시 시도하지 않는다.

const (
	StdCodeDocsURL = "https://infuser.odcloud.kr/oas/docs?namespace=15067462/v1"
	DataGoBase     = "https://www.data.go.kr"
	StdCodePK      = "15067462"
	maxStdCodeSize = 200 << 20
)

var ErrNeedCaptcha = errors.New("공공데이터포털이 자동 내려받기를 막았습니다(보안문자). 포털에서 직접 내려받아 '데이터 가져오기 → 표준코드 매핑'으로 올려주세요")

type StdSnapshot struct {
	UDDI  string `json:"uddi"`  // 예: uddi:8116acc9-2f07-4a1f-bec0-ddc8047bec3e
	Date  string `json:"date"`  // 판 기준일 (YYYY-MM-DD)
	Title string `json:"title"` // 예: 건강보험심사평가원_약가마스터_의약품표준코드_20260831
}

var (
	reYMD = regexp.MustCompile(`(20\d{2})(\d{2})(\d{2})\s*$`)
	reMDY = regexp.MustCompile(`(\d{2})/(\d{2})/(20\d{2})\s*$`)
)

// ParseStdDocs 는 odcloud 문서(swagger)에서 가장 최근 판을 고른다.
func ParseStdDocs(data []byte) (StdSnapshot, error) {
	var doc struct {
		Paths map[string]map[string]struct {
			Summary string `json:"summary"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return StdSnapshot{}, fmt.Errorf("표준코드 목록 해석 실패: %w", err)
	}
	var snaps []StdSnapshot
	for p, ops := range doc.Paths {
		i := strings.Index(p, "uddi:")
		if i < 0 {
			continue
		}
		title := ops["get"].Summary
		date := ""
		if m := reYMD.FindStringSubmatch(title); m != nil {
			date = m[1] + "-" + m[2] + "-" + m[3]
		} else if m := reMDY.FindStringSubmatch(title); m != nil {
			date = m[3] + "-" + m[1] + "-" + m[2]
		}
		snaps = append(snaps, StdSnapshot{UDDI: p[i:], Date: date, Title: title})
	}
	if len(snaps) == 0 {
		return StdSnapshot{}, errors.New("표준코드 판 정보를 찾지 못했습니다")
	}
	sort.Slice(snaps, func(a, b int) bool {
		if snaps[a].Date != snaps[b].Date {
			return snaps[a].Date > snaps[b].Date
		}
		return snaps[a].UDDI > snaps[b].UDDI
	})
	return snaps[0], nil
}

func (c *Client) send(ctx context.Context, hc *http.Client, method, u string, form url.Values) (*http.Response, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	return hc.Do(req)
}

// LatestStdSnapshot : 최신 판 정보 (요청 1번, 약 18KB)
func (c *Client) LatestStdSnapshot(ctx context.Context, docsURL string) (StdSnapshot, error) {
	if docsURL == "" {
		docsURL = StdCodeDocsURL
	}
	resp, err := c.send(ctx, c.HTTP, http.MethodGet, docsURL, nil)
	if err != nil {
		return StdSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return StdSnapshot{}, fmt.Errorf("표준코드 판 확인 실패: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return StdSnapshot{}, err
	}
	return ParseStdDocs(data)
}

// DownloadStdCode 는 그 판의 CSV 를 내려받는다 (세션 쿠키 필요). base 가 비면 공공데이터포털.
func (c *Client) DownloadStdCode(ctx context.Context, base string, snap StdSnapshot) (string, []byte, error) {
	if base == "" {
		base = DataGoBase
	}
	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Timeout: 5 * time.Minute, Jar: jar}
	// 1) 파일 정보
	resp, err := c.send(ctx, hc, http.MethodPost, base+"/tcs/dss/selectFileDataDownload.do",
		url.Values{"publicDataPk": {StdCodePK}, "publicDataDetailPk": {snap.UDDI}, "fileDetailSn": {"1"}})
	if err != nil {
		return "", nil, err
	}
	var info struct {
		VO struct {
			AtchFileID   string `json:"atchFileId"`
			FileDetailSn any    `json:"fileDetailSn"`
			FileName     string `json:"orginlFileNm"`
		} `json:"fileDataRegistVO"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&info)
	resp.Body.Close()
	if err != nil || info.VO.AtchFileID == "" {
		return "", nil, fmt.Errorf("표준코드 파일 정보를 받지 못했습니다 (포털 화면이 바뀌었을 수 있음): %v", err)
	}
	sn := fmt.Sprint(info.VO.FileDetailSn)
	if sn == "" || sn == "<nil>" {
		sn = "1"
	}
	if err := Pause(ctx, c.Pause); err != nil {
		return "", nil, err
	}
	// 2) 다운로드 한도(보안문자) 확인
	resp, err = c.send(ctx, hc, http.MethodPost, base+"/cmm/cmm/check-limit.json", url.Values{"atchFileId": {info.VO.AtchFileID}, "fileDetailSn": {sn}})
	if err != nil {
		return "", nil, err
	}
	var lim struct {
		NeedCaptcha bool `json:"needCaptcha"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&lim)
	resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden || lim.NeedCaptcha {
		return "", nil, ErrNeedCaptcha
	}
	if err != nil {
		return "", nil, fmt.Errorf("다운로드 한도 확인 실패: %w", err)
	}
	if err := Pause(ctx, c.Pause); err != nil {
		return "", nil, err
	}
	// 3) 내려받기
	resp, err = c.send(ctx, hc, http.MethodGet, base+"/cmm/cmm/fileDownload.do?atchFileId="+url.QueryEscape(info.VO.AtchFileID)+"&fileDetailSn="+url.QueryEscape(sn), nil)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("표준코드 내려받기 실패: HTTP %d", resp.StatusCode)
	}
	name := info.VO.FileName
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		name = params["filename"]
	}
	if name == "" {
		name = "약가마스터_의약품표준코드_" + snap.Date + ".csv"
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxStdCodeSize+1))
	if err != nil {
		return "", nil, err
	}
	if len(data) > maxStdCodeSize { // 잘린 파일을 가져오면 매핑 일부가 사라진다
		return "", nil, fmt.Errorf("표준코드 파일이 예상보다 큽니다 (%dMB 넘음) — 직접 내려받아 올려 주세요", maxStdCodeSize>>20)
	}
	if len(data) < 1000 || strings.Contains(strings.ToLower(string(data[:min(len(data), 512)])), "<html") {
		return "", nil, errors.New("표준코드 파일 대신 웹 페이지가 왔습니다 (포털 점검 또는 차단)")
	}
	return name, data, nil
}
