package main

// 외부 중계 (휴대폰을 모바일 데이터·약국 밖에서 쓸 때) — 클라우드플레어 무료 중계(Quick Tunnel).
//
//   - 이 PC 의 DPR 이 cloudflared 를 켜서 클라우드플레어에 '나가는' 연결을 만든다. 그래서 공유기·Windows 방화벽(공용)을 바꿀 필요가 없다.
//   - 휴대폰은 https://<임의 이름>.trycloudflare.com 주소로 들어온다. 주소는 DPR 을 켤 때마다 바뀐다 (QR 을 다시 찍는다).
//   - 중계를 켜면 휴대폰 연결 서버는 이 PC 안(127.0.0.1)에서만 열어, 사내망에서도 직접 들어올 수 없게 한다.
//   - cloudflared 는 처음 한 번 GitHub 의 클라우드플레어 공식 배포에서 받고, Windows 가 확인한 클라우드플레어 전자서명이 있을 때만 쓴다.
//   - 중계로 오가는 자료는 휴대폰 화면이 요청한 것뿐이다 (phone.go: 회차 목록·약품 이름 목록·찍은 사진·바코드 글자).

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"dpr/internal/platform"
)

const setPhoneRelay = "phone_relay" // "1" = 외부 중계 사용

type relayState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	status string // "" 꺼짐 | downloading | starting | ready | error
	url    string
	err    string
}

var trycloudflareURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (a *App) relayEnabled() bool { return a.svc.DB.GetSetting(setPhoneRelay, "0") == "1" }

func (a *App) relayDir() string { return filepath.Join(a.svc.DataDir, "tools") }

func (r *relayState) set(status, url, err string) {
	r.mu.Lock()
	r.status, r.url, r.err = status, url, err
	r.mu.Unlock()
}

func (r *relayState) snapshot() (status, url, err string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.url, r.err
}

// startRelay : 중계를 켠다 (이미 켜져 있으면 그대로). 받기·시작은 뒤에서 하고 상태는 PhoneStatus 로 본다.
func (a *App) startRelay(port int) {
	r := &a.relay
	r.mu.Lock()
	if r.cancel != nil {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.status, r.url, r.err = "starting", "", ""
	r.mu.Unlock()
	go a.runRelay(ctx, port)
}

func (a *App) stopRelay() {
	r := &a.relay
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.status, r.url, r.err = "", "", ""
	r.mu.Unlock()
	if cancel != nil {
		cancel()
		a.log.Info("외부 중계 끔")
	}
}

func (a *App) runRelay(ctx context.Context, port int) {
	exe, err := a.ensureCloudflared(ctx)
	if err != nil {
		if ctx.Err() == nil {
			a.log.Warn("중계 프로그램 준비 실패", "err", err)
			a.relay.set("error", "", err.Error())
			a.emit("phone-status", nil)
		}
		return
	}
	// 끊기면 잠시 뒤 다시 (인터넷이 잠깐 끊긴 경우 등). 연속 실패하면 간격을 늘린다.
	wait := 5 * time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := a.runCloudflared(ctx, exe, port)
		if ctx.Err() != nil {
			return
		}
		msg := "외부 중계가 끊겼습니다. 다시 연결하는 중입니다 (인터넷 연결을 확인하세요)"
		if err != nil {
			a.log.Warn("외부 중계 끊김", "err", err)
		}
		a.relay.set("error", "", msg)
		a.emit("phone-status", nil)
		if time.Since(started) > 2*time.Minute {
			wait = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < 2*time.Minute {
			wait *= 2
		}
		a.relay.set("starting", "", "")
		a.emit("phone-status", nil)
	}
}

// runCloudflared : cloudflared 한 번 실행 — 주소가 나오면 ready, 끝나면 돌아온다
func (a *App) runCloudflared(ctx context.Context, exe string, port int) error {
	pidFile := filepath.Join(a.relayDir(), "cloudflared.pid")
	if b, err := os.ReadFile(pidFile); err == nil { // 지난번 비정상 종료로 남은 것 정리
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 {
			platform.KillIfNamed(pid, "cloudflared")
		}
	}
	cmd := exec.CommandContext(ctx, exe, "tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://127.0.0.1:%d", port))
	platform.HideWindow(cmd)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("중계 프로그램을 실행하지 못했습니다: %w", err)
	}
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	defer os.Remove(pidFile)
	a.log.Info("외부 중계 시작", "pid", cmd.Process.Pid)
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		if u := trycloudflareURL.FindString(line); u != "" {
			if st, cur, _ := a.relay.snapshot(); st != "ready" || cur != u {
				a.relay.set("ready", u, "")
				a.log.Info("외부 중계 주소 준비됨")
				a.emit("phone-status", nil)
			}
		}
	}
	return cmd.Wait()
}

// ensureCloudflared : 받아 둔 cloudflared 가 없으면 공식 배포에서 받고, 클라우드플레어 전자서명을 확인한다
func (a *App) ensureCloudflared(ctx context.Context) (string, error) {
	dir := a.relayDir()
	exe := filepath.Join(dir, platform.CloudflaredExe)
	if st, err := os.Stat(exe); err == nil && st.Size() > 1<<20 {
		if err := platform.VerifySigner(exe, platform.CloudflareSigner); err == nil {
			return exe, nil
		}
		_ = os.Remove(exe) // 서명이 맞지 않으면 버리고 다시 받는다
	}
	a.relay.set("downloading", "", "")
	a.emit("phone-status", nil)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	a.log.Info("중계 프로그램 받는 중", "url", platform.CloudflaredDownloadURL)
	dctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(dctx, http.MethodGet, platform.CloudflaredDownloadURL, nil)
	req.Header.Set("User-Agent", "DPR")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("중계 프로그램을 받지 못했습니다 (인터넷 연결 확인): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("중계 프로그램을 받지 못했습니다 (응답 %d)", resp.StatusCode)
	}
	tmp := exe + ".download.exe" // 서명 확인이 실행 파일로 인식하도록 .exe 로 끝나게
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, 300<<20))
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("중계 프로그램을 받는 중 끊겼습니다: %w", err)
	}
	if n < 1<<20 {
		os.Remove(tmp)
		return "", errors.New("받은 중계 프로그램이 올바르지 않습니다")
	}
	if err := platform.VerifySigner(tmp, platform.CloudflareSigner); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("받은 중계 프로그램을 믿을 수 없어 쓰지 않았습니다: %w", err)
	}
	_ = os.Chmod(tmp, 0o755)
	if err := os.Rename(tmp, exe); err != nil {
		return "", err
	}
	a.log.Info("중계 프로그램 받음", "bytes", n)
	return exe, nil
}
