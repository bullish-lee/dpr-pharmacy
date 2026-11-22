package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"dpr/internal/platform"
	"dpr/internal/service"
)

const cliUsage = `DPR 급여약품 관리 — 명령줄 사용법

  DPR crawl                                  복지부 고시 확인·수집 + 표준코드 새 판 확인 (창 없이, 작업 스케줄러용)
  DPR import <종류> <파일> [옵션]             파일 가져오기
       종류: purchase | dispense | initial_stock | stock_count | lot | notice | drug_master | gtin
       -baseline 2026-02-02   초기재고 기준일      -timing start|end   기준 시점(영업 전/후)
       -asof 2026-10-31       실사일               -product 987654320  사입 근거 대상 약품
       -force                 같은 기간 기록을 크게 줄이는 교체도 진행 (안전장치 무시)
  DPR notice-post <게시글 주소|번호>          복지부 게시글 하나의 고시 목록표 가져오기 (지난 고시)
  DPR rank <시행일> [-all]                   손실 순위 출력 (기본: 보유 품목만)
  DPR backup                                 DB 백업
  DPR datadir [reset]                        데이터 폴더 위치 (reset: 옮긴 위치 기록을 지워 기본 위치로)
  DPR version
`

func runCLI(args []string) int {
	platform.AttachConsole()
	cmd := args[0]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Print(cliUsage)
		return 0
	case "version":
		fmt.Println("DPR", service.AppVersion)
		return 0
	case "datadir":
		if len(args) > 1 && args[1] == "reset" { // 옮긴 위치 기록을 지워 기본 위치로
			if err := platform.SetDataLocation(""); err != nil {
				fmt.Fprintln(os.Stderr, "되돌리기 실패:", err)
				return 1
			}
			fmt.Println("기본 위치로 되돌렸습니다:", platform.DefaultDataDir())
			return 0
		}
		dir, src, err := platform.Locate()
		fmt.Printf("%s (%s)\n", dir, map[string]string{platform.SourceEnv: "환경변수 DPR_DATA_DIR", platform.SourcePortable: "휴대용 — exe 옆 dpr-data",
			platform.SourceCustom: "옮긴 위치 — 기록: " + platform.LocationFileInUse(), platform.SourceDefault: "기본 위치"}[src])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	dataDir, source, err := platform.Locate()
	if err != nil { // 옮긴 데이터 폴더가 없음 — 빈 자료를 새로 만들지 않는다 (작업 스케줄러로 실행돼도)
		fmt.Fprintln(os.Stderr, err)
		if cmd == "crawl" {
			_ = platform.Toast("급여약품 관리", "데이터 폴더를 찾을 수 없어 고시를 확인하지 못했습니다 — 드라이브 연결을 확인하세요")
		}
		return 1
	}
	log := setupLogger(dataDir, "dpr-cli", false)
	svc, err := service.New(dataDir, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, "시작 실패:", err)
		return 1
	}
	svc.DataDirSource = source
	defer svc.Close()
	switch cmd {
	case "crawl":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		svc.OnNotify = func(n service.Notification) { _ = platform.Toast(n.Title, n.Body) }
		res, err := svc.RunCrawl(ctx)
		if err != nil {
			log.Error("수집 실패", "err", err)
			fmt.Fprintln(os.Stderr, "수집 실패:", err)
			return 2
		}
		svc.AutoBackupIfDue()
		fmt.Printf("RSS %d건 확인, 대상 %d건, 신규 %d건\n", res.Checked, res.Matched, res.New)
		for _, n := range res.Notices {
			fmt.Println(" -", n)
		}
		if svc.DB.GetSetting(service.SetStdAuto, "1") == "1" { // 표준코드 새 판 (하루 1번)
			sctx, scancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer scancel()
			if sr, err := svc.CheckStdCode(sctx, false); err != nil {
				fmt.Fprintln(os.Stderr, "표준코드 확인 실패:", err)
			} else if !sr.Skipped {
				fmt.Println(sr.Message)
			}
		}
		if svc.DB.GetSetting(service.SetMasterAuto, "1") == "1" && svc.DB.GetSetting(service.SetDataGoKey, "") != "" { // 약제급여목록 (한 달에 1번)
			mctx, mcancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer mcancel()
			if mr, err := svc.CheckDrugMaster(mctx, false); err != nil {
				fmt.Fprintln(os.Stderr, "약제급여목록 확인 실패:", err)
			} else if !mr.Skipped {
				fmt.Println(mr.Message)
			}
		}
		return 0
	case "import":
		fs := flag.NewFlagSet("import", flag.ContinueOnError)
		baseline := fs.String("baseline", "", "초기재고 기준일")
		timing := fs.String("timing", "", "start|end")
		asOf := fs.String("asof", "", "실사일")
		product := fs.String("product", "", "사입 근거 대상 약품코드")
		force := fs.Bool("force", false, "기간 교체 안전장치 무시")
		if len(args) < 3 {
			fmt.Print(cliUsage)
			return 1
		}
		if err := fs.Parse(args[3:]); err != nil {
			return 1
		}
		res, err := svc.Import(service.ImportRequest{Kind: args[1], Path: args[2], BaselineDate: *baseline, Timing: *timing, AsOf: *asOf, ProductCode: *product, Force: *force})
		if err != nil {
			fmt.Fprintln(os.Stderr, "가져오기 실패:", err)
			return 1
		}
		if res.NeedsConfirm { // 기간 교체 안전장치 — 아무것도 바꾸지 않았다
			fmt.Fprintln(os.Stderr, res.Message, "— 그래도 교체하려면 -force 를 붙이세요.")
			return 4
		}
		if res.NeedsMapping {
			fmt.Fprintln(os.Stderr, res.Message, "— 화면에서 열 매핑을 지정해 주세요.")
			return 3
		}
		fmt.Println(res.Message)
		for i, is := range res.Issues {
			if i >= 20 {
				fmt.Printf("  … 외 %d건\n", res.IssueCount-20)
				break
			}
			fmt.Printf("  [%s %d행] %s\n", is.Sheet, is.Row, is.Message)
		}
		return 0
	case "notice-post":
		if len(args) < 2 {
			fmt.Print(cliUsage)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		res, err := svc.ImportNoticePost(ctx, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "고시 가져오기 실패:", err)
			return 1
		}
		fmt.Println(res.Message)
		return 0
	case "rank":
		fs := flag.NewFlagSet("rank", flag.ContinueOnError)
		all := fs.Bool("all", false, "보유하지 않은 품목도 출력")
		if len(args) < 2 {
			fmt.Print(cliUsage)
			return 1
		}
		_ = fs.Parse(args[2:])
		rk, err := svc.Ranking(args[1], !*all)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("%s 시행 (재고 기준 %s) — 보유 %d / 전체 %d, 예상 손실 %s원, 예상 보상 %s원, 순손실 %s원\n",
			rk.EffectiveDate, rk.AsOf, rk.Totals.Held, rk.Totals.Items, won(rk.Totals.GrossLoss), won(rk.Totals.Recovery), won(rk.Totals.NetLoss))
		for _, w := range rk.Warnings {
			fmt.Println("  !", w)
		}
		for i, r := range rk.Rows {
			fmt.Printf("%3d. %-40s 재고 %8s × %8.1f = %10s원  서류 %6s  %s\n", i+1, trunc(r.Name, 40), r.Stock, r.Delta, won(r.GrossLoss), r.TotalDocQty, recLabel(r.Recommendation))
		}
		return 0
	case "backup":
		p, err := svc.Backup()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("백업:", p)
		return 0
	}
	fmt.Print(cliUsage)
	return 1
}

func won(v int64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := fmt.Sprintf("%d", v)
	out := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += ","
		}
		out += string(c)
	}
	if neg {
		return "-" + out
	}
	return out
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func recLabel(r string) string {
	switch r {
	case "document":
		return "서류보상"
	case "physical_review":
		return "실물보상 검토"
	case "no_stock":
		return "재고 없음"
	case "no_loss":
		return "손실 없음"
	}
	return r
}
