// DPR — 급여약품 관리 데스크탑 앱 (약가인하 대응·재고·도매 보상).
//
//	DPR.exe              → 화면(데스크탑 앱) 실행
//	DPR.exe crawl        → 창 없이 고시 수집만 (Windows 작업 스케줄러용)
//	DPR.exe import …     → 명령줄 가져오기 / DPR.exe rank 2026-11-01 → 손실 순위 출력
package main

import (
	"crypto/sha1"
	"dpr/internal/applog"
	"embed"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"dpr/internal/platform"
	"dpr/internal/service"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// 보조 글자 인식(PaddleOCR)의 일꾼으로 실행된 것이면 그림만 읽고 끝낸다 (internal/platform/paddle.go)
	if platform.RunPaddleWorker(os.Args) {
		return
	}
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		os.Exit(runCLI(os.Args[1:]))
	}
	runGUI()
}

// setupLogger 는 data/logs/<name>.log 에 기록한다 (5MB 마다 돌려 최근 5개 보관, internal/applog).
// 앱은 dpr.log, 명령줄(작업 스케줄러)은 dpr-cli.log — 두 프로세스가 같은 파일을 돌리지 않게 나눈다.
func setupLogger(dataDir, name string, alsoStderr bool) *slog.Logger {
	log, _ := applog.New(dataDir, name, alsoStderr)
	return log
}

func runGUI() {
	waitForPrevious()
	dataDir, source, err := platform.Locate()
	if err != nil { // 옮긴 데이터 폴더가 없음 (드라이브가 빠짐 등) — 빈 자료로 열지 않는다
		platform.Fatal("급여약품 관리 — 데이터 폴더를 찾을 수 없습니다", err.Error())
		os.Exit(1)
	}
	log := setupLogger(dataDir, "dpr", true)
	svc, err := service.New(dataDir, log)
	if err != nil {
		platform.Fatal("DPR 을 시작할 수 없습니다", fmt.Sprintf("데이터 폴더: %s\n\n%v", dataDir, err))
		os.Exit(1)
	}
	svc.DataDirSource = source
	log.Info("시작", "version", service.AppVersion, "os", goruntime.GOOS+"/"+goruntime.GOARCH, "after_update", justUpdated)
	defer func() { // 예기치 못한 종료도 기록에 남긴다
		if r := recover(); r != nil {
			log.Error("비정상 종료", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			panic(r)
		}
	}()
	app := NewApp(svc, log)
	err = wails.Run(&options.App{
		Title:            "급여약품 관리",
		Width:            1440,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        560,
		WindowStartState: options.Maximised,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 247, G: 248, B: 250, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               instanceID(dataDir), // 데이터 폴더마다 하나만 실행 (개발·지원용으로 다른 폴더는 동시에 열 수 있다)
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		EnableDefaultContextMenu: true, // 입력칸 복사·붙여넣기 메뉴
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			IsZoomControlEnabled: false, // 글자 크기는 앱 설정(화면 배율)으로 조절
			ZoomFactor:           1.0,
			Theme:                windows.Light,
			BackdropType:         windows.None,
		},
		Mac: &mac.Options{
			About: &mac.AboutInfo{Title: "급여약품 관리 (DPR)", Message: "약국 급여약품 관리 v" + service.AppVersion},
		},
	})
	if err != nil {
		log.Error("앱 실행 오류", "err", err)
	}
	_ = svc.Close()
}

// justUpdated : 업데이트 직후 실행됨 (이전 버전 DPR.exe.old 를 이번에는 지우지 않는다 — 새 버전에 문제가 있으면 되돌릴 수 있게)
var justUpdated bool

// waitForPrevious : 업데이트('--after-update=<PID>')나 데이터 폴더 옮기기('--restart-after=<PID>') 뒤 다시 켜진 경우
// 이전 프로세스가 끝날 때까지 (최대 60초) 기다린다.
// 이전 프로세스가 살아 있는 채로 시작하면 '한 번에 하나만 실행' 규칙 때문에 새 창이 뜨지 않고 끝나 버린다.
func waitForPrevious() {
	for _, a := range os.Args[1:] {
		var v string
		switch {
		case strings.HasPrefix(a, "--after-update="):
			justUpdated = true
			v = strings.TrimPrefix(a, "--after-update=")
		case strings.HasPrefix(a, "--restart-after="):
			v = strings.TrimPrefix(a, "--restart-after=")
		default:
			continue
		}
		pid, err := strconv.Atoi(v)
		if err != nil || pid <= 0 {
			return
		}
		deadline := time.Now().Add(60 * time.Second)
		for platform.ProcessAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(200 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func instanceID(dataDir string) string {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		abs = dataDir
	}
	h := sha1.Sum([]byte(strings.ToLower(abs)))
	return "dpr-" + hex.EncodeToString(h[:6])
}
