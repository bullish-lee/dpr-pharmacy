// Package updater 는 GitHub Releases 에서 새 버전을 확인하고 실행 파일을 교체한다.
//
//	확인: GET api.github.com/repos/{owner}/{repo}/releases/latest (비공개 저장소는 읽기 전용 토큰)
//	받기: 자산 'DPR-v<버전>-windows.zip' + '.sha256' + '.sig' → SHA-256·개발자 서명 확인(sign.go) → zip 안의 DPR.exe
//	교체: 실행 중인 DPR.exe → DPR.exe.old 로 이름 바꾸기(Windows 는 실행 중 파일의 이름 변경을 허용) → 새 파일을 DPR.exe 로
//	      다음 실행 때 .old 를 지운다. 실패하면 원래 파일로 되돌린다.
package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var APIBase = "https://api.github.com" // 테스트에서 바꾼다

const maxAsset = 200 << 20

type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"url"` // API 주소 (Accept: application/octet-stream 으로 내려받기)
}

type Release struct {
	Tag        string  `json:"tag_name"`
	Name       string  `json:"name"`
	Body       string  `json:"body"`
	HTMLURL    string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Published  string  `json:"published_at"`
	Assets     []Asset `json:"assets"`
}

type Client struct {
	HTTP  *http.Client
	Repo  string // owner/name
	Token string // 비공개 저장소용 (없으면 공개 저장소)
}

func New(repo, token string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 5 * time.Minute}, Repo: strings.Trim(repo, "/"), Token: strings.TrimSpace(token)}
}

func (c *Client) do(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "DPR-updater")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return c.HTTP.Do(req)
}

// Latest 는 최신 정식 릴리즈 (초안·사전 릴리즈 제외).
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	if strings.Count(c.Repo, "/") != 1 {
		return nil, fmt.Errorf("업데이트 저장소 형식이 잘못되었습니다: %q", c.Repo)
	}
	resp, err := c.do(ctx, APIBase+"/repos/"+c.Repo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		if c.Token == "" {
			return nil, errors.New("릴리즈를 찾지 못했습니다 (아직 릴리즈가 없거나, 비공개 저장소라면 설정에 읽기 전용 토큰이 필요합니다)")
		}
		return nil, errors.New("릴리즈를 찾지 못했습니다 (아직 릴리즈가 없거나 토큰 권한이 없습니다)")
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("GitHub 접근이 거부되었습니다 (HTTP %d) — 토큰을 확인하세요", resp.StatusCode)
	default:
		return nil, fmt.Errorf("업데이트 확인 실패: HTTP %d", resp.StatusCode)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WindowsAssets 는 Windows zip · 체크섬 · 서명 자산을 찾는다.
func (r *Release) WindowsAssets() (zipAsset, sumAsset, sigAsset *Asset) {
	for i := range r.Assets {
		a := &r.Assets[i]
		n := strings.ToLower(a.Name)
		switch {
		case strings.HasSuffix(n, "-windows.zip"):
			zipAsset = a
		case strings.HasSuffix(n, "-windows.zip.sha256"):
			sumAsset = a
		case strings.HasSuffix(n, "-windows.zip.sig"):
			sigAsset = a
		}
	}
	return
}

func (c *Client) Download(ctx context.Context, a *Asset) ([]byte, error) {
	return c.download(ctx, a, maxAsset)
}

// DownloadSmall : 서명·체크섬처럼 작은 파일 (64KB 넘으면 거부 — 큰 파일로 메모리·기록을 채우지 못하게)
func (c *Client) DownloadSmall(ctx context.Context, a *Asset) ([]byte, error) {
	return c.download(ctx, a, MaxSigFile)
}

func (c *Client) download(ctx context.Context, a *Asset, limit int64) ([]byte, error) {
	resp, err := c.do(ctx, a.URL, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%.80s 내려받기 실패: HTTP %d", a.Name, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%.80s 이(가) 너무 큽니다", a.Name)
	}
	return data, nil
}

// VerifySHA256 : 'shasum -a 256' 형식("<hex>  <파일명>") 또는 hex 만.
func VerifySHA256(data []byte, sumFile string) error {
	f := strings.Fields(sumFile)
	if len(f) == 0 {
		return errors.New("체크섬 파일이 비어 있습니다")
	}
	want := strings.ToLower(f[0])
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return errors.New("내려받은 파일의 체크섬이 맞지 않습니다 (손상되었거나 변조됨) — 업데이트를 중단합니다")
	}
	return nil
}

// ExtractFile 은 zip 맨 위에 있는 이름이 name 인 파일(대소문자 무시)을 꺼낸다. 없거나 둘 이상이면 오류.
func ExtractFile(zipData []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, err
	}
	var found *zip.File
	for _, f := range zr.File {
		if strings.ContainsAny(f.Name, `/\`) || !strings.EqualFold(f.Name, name) {
			continue // 폴더 안의 같은 이름은 보지 않는다
		}
		if found != nil {
			return nil, fmt.Errorf("zip 안에 %s 가 둘 이상 있습니다", name)
		}
		found = f
	}
	if found == nil {
		return nil, fmt.Errorf("zip 맨 위에 %s 가 없습니다", name)
	}
	rc, err := found.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxAsset+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAsset {
		return nil, fmt.Errorf("%s 이(가) 너무 큽니다", name)
	}
	return data, nil
}

// Replace 는 실행 파일을 교체한다. 실패하면 원래대로 되돌린다.
func Replace(exePath string, newExe []byte) error {
	if len(newExe) < 1<<20 {
		return errors.New("새 실행 파일이 너무 작습니다 — 업데이트를 중단합니다")
	}
	tmp := exePath + ".new"
	old := exePath + ".old"
	if err := os.WriteFile(tmp, newExe, 0o755); err != nil {
		return err
	}
	_ = os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("실행 중인 파일 이름을 바꾸지 못했습니다: %w", err)
	}
	if err := os.Rename(tmp, exePath); err != nil {
		_ = os.Rename(old, exePath) // 되돌리기
		_ = os.Remove(tmp)
		return fmt.Errorf("새 실행 파일을 놓지 못했습니다: %w", err)
	}
	_ = os.WriteFile(exePath+".updated", nil, 0o644) // 새 버전으로 처음 켤 때 .old 를 지우지 않게 (ConsumeUpdateMarker)
	return nil
}

// ConsumeUpdateMarker : 업데이트한 뒤 처음 켜졌는가 — 그렇다면 표시를 지운다 (다음에 켤 때 .old 를 지운다).
// 다시 시작하지 못해 사용자가 손으로 켜서 --after-update 가 없어도 이전 버전을 한 번은 남겨 되돌릴 수 있게.
func ConsumeUpdateMarker(exePath string) bool {
	if _, err := os.Stat(exePath + ".updated"); err != nil {
		return false
	}
	_ = os.Remove(exePath + ".updated")
	return true
}

// CleanupOld 는 지난 업데이트가 남긴 .old 파일을 지운다 (켤 때 호출).
func CleanupOld(exePath string) { _ = os.Remove(exePath + ".old") }

// Compare 는 버전 a 와 b 를 비교한다 (a<b → -1). 'v' 접두와 '-dev' 같은 꼬리는 무시, 'dev' 는 가장 낮다.
func Compare(a, b string) int {
	pa, pb := parse(a), parse(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parse(v string) [3]int {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	if v == "" || v == "dev" {
		return [3]int{-1, -1, -1}
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{-1, -1, -1}
		}
		out[i] = n
	}
	return out
}
