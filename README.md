# 급여약품 관리 (DPR)

약국의 **급여 의약품**을 관리하는 데스크탑 앱입니다. 약가인하 대응·재고 추정·도매 보상을 다룹니다. Windows 단일 실행 파일(`DPR.exe`)로 동작합니다.

- 보건복지부 「약제 급여 목록 및 급여 상한금액표」 고시를 **하루 3회 자동 확인**하고, 신설·삭제·변경(인하·인상·제품명·약가유연계약)을 시행일별로 정리합니다.
- 이팜(eFarm) 엑셀(초기재고·사입현황·조제현황)로 **추정재고**를 계산합니다. 실계수를 입력하면 실계수가 우선합니다.
- 품목별 **예상 손실 순위**를 보여주고, 도매별 **서류보상 / 실물보상**을 판정합니다. 도매별 보상 기준(비율·기간·마감일·결제일)은 화면에서 관리합니다.
- 바코드(GS1 DataMatrix)를 **스캐너나 사진**으로 읽어 제조번호·유효기간으로 사입 도매를 매칭합니다.
- 도매 앱 등록용 바코드를 화면에 한 개씩 다시 그려 보여주고, 스캔완료를 체크합니다.
- 처리 결과(약·도매·수량·금액)를 저장하고 집계하며, CSV로 내보냅니다.
- 모든 수량을 **고시 단위**(상한금액 단위 = 청구 단위)로 맞춥니다. 팩 단위·g 단위 고시나 이팜 단위수량 오기도 자동으로 환산합니다.
- 심평원 표준코드(바코드 매핑) 새 판을 하루 한 번 확인하고, GitHub 릴리즈로 **자동 업데이트**하며, 문제가 생기면 **오류 보고**로 기록을 보냅니다.
- 대시보드 **데이터 점검**이 음수 재고(원인 추정)·단위 이상·과다 재고를 자동으로 찾아 알려주고, 그 자리에서 실계수·단위 배율·'확인함'으로 바로 고칩니다. 고친 내역은 **수정 기록**에 남습니다.

> 설계: [docs/DESIGN.md](docs/DESIGN.md) · 배포·서명: [docs/RELEASING.md](docs/RELEASING.md) · [코드 서명 정책](#code-signing-policy) · [개인정보 처리](PRIVACY.md)
> UI 규칙: [docs/UI_GUIDE.md](docs/UI_GUIDE.md) · 데이터 출처: [docs/DATA_SOURCES.md](docs/DATA_SOURCES.md) · 실데이터 규칙: [docs/SENSITIVE.md](docs/SENSITIVE.md)

## 약국에서 쓰기 (Windows)

1. 릴리즈의 `DPR-v<버전>-windows.zip`을 원하는 폴더(예: `C:\DPR`)에 풀고 `DPR.exe`를 실행합니다. 설치는 필요 없습니다. 이후 새 버전은 프로그램이 알려줍니다.
   - Windows 10/11에 기본으로 들어 있는 WebView2를 사용합니다.
   - 데이터는 `%APPDATA%\DPR`에 저장됩니다. exe 옆에 `dpr-data` 폴더를 만들면 그 폴더를 씁니다(휴대용 모드).
2. **데이터 가져오기**에서 이팜 엑셀을 올립니다.
   - 순서: 사입현황 → 조제현황 → 초기재고(기준일 지정)
   - 권장: '약제급여목록표'(심평원, 월별 전체)를 먼저 올리면 단위 환산과 이름 매칭이 정확해집니다.
   - 표준코드 매핑은 자동으로 받습니다(설정 → 자동 확인). 직접 올려도 됩니다.
3. **도매처·보상기준**에서 도매별 기준을 입력합니다. 처음에는 30%·3개월 기본값이 '확인 필요'로 표시돼 있습니다.
4. 고시가 수집되면 **약가인하 대응**에서 시행일을 고릅니다.
   - 손실이 큰 품목부터 실계수를 넣습니다.
   - 판정을 확인합니다.
   - 실물보상이 유리하면 바코드를 스캔하고, 도매 앱에 등록합니다.
   - 처리 완료를 기록합니다.
5. 확인 시점은 **설정 → 자동 확인**에서 고릅니다 (켤 때 / 정해진 시각). 창을 닫아도 확인되게 하려면 **작업 스케줄러에 등록**을 누릅니다.

명령줄(작업 스케줄러·지원용):
```
DPR.exe crawl                         # 창 없이 고시 확인·수집 (새 고시면 Windows 알림)
DPR.exe import purchase 사입현황.xlsx  # 가져오기 (종류: purchase dispense initial_stock stock_count lot notice drug_master gtin)
DPR.exe rank 2026-11-01               # 손실 순위 출력
DPR.exe backup | datadir | version
```

## 개발

필요: Go 1.26+, Node 22+, [Wails v2 CLI](https://wails.io) (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)
권장: mingw-w64 (`brew install mingw-w64`) — 있으면 Windows exe 를 C SQLite 로 빌드합니다(조회 약 2배 빠름). 없으면 순수 Go SQLite 로 자동 전환됩니다.

```bash
make dev          # 개발 실행 (wails dev, 화면 자동 새로고침)
make test         # Go 테스트
make check        # Go vet(맥·Windows) + 테스트 + 프론트 타입 검사 (병렬)
make build        # 현재 OS용 빌드 → build/bin/
make windows      # Windows exe 교차 빌드 → build/bin/DPR.exe (변경 없으면 약 0.5초)
make bindings     # app.go 의 바인딩(함수·구조체)을 바꿨을 때 화면용 코드 갱신
make release      # 배포 zip + sha256 → build/release/ (버전은 VERSION 파일)
make sensitive    # 실데이터 표시가 남았는지 점검
```

- 실데이터 회귀 테스트 (실제 약국 파일은 **절대 커밋하지 않음**):
  ```bash
  DPR_REALDATA=/path/to/folder go test ./internal/... -run RealData -v
  # 폴더 안: purchase.xlsx dispense.xlsx initial.xlsx notice.xlsx
  ```
- 개발·캡처용으로 시작 화면을 지정하려면 `DPR_START_ROUTE=/cuts/2026-11-01`을 쓰고, 데이터 폴더를 지정하려면 `DPR_DATA_DIR=...`을 씁니다.

### 구조
```
main.go / app.go / cli.go   진입점 · 화면 바인딩 · 명령줄
internal/domain     순수 계산 (보상 규칙·판정·GS1·단가·로트 매칭) — I/O 없음
internal/ingest     엑셀 스키마·열 자동 매핑·이팜/고시 파서
internal/sheet      xlsx/CSV 리더
internal/crawler    복지부 RSS·첨부 수집
internal/store      SQLite(WAL)·마이그레이션·백업
internal/service    유스케이스 (가져오기·재고·분석·스캔·처리결과·수집)
internal/platform   OS 기능 (작업 스케줄러·알림·폴더)
frontend/           React + TypeScript + Tailwind (Wails 바인딩: frontend/wailsjs)
docs/               설계·배포·UI·데이터 출처
```

## 데이터·개인정보
- 약국 데이터는 PC의 데이터 폴더에만 저장되고 외부로 전송되지 않습니다. 인터넷은 복지부 고시·심평원 표준코드 확인, 새 버전 확인, (사용자가 누를 때) 오류 보고에만 씁니다. 오류 보고에는 거래 자료를 넣지 않고 경로·파일 이름을 가립니다.
- 조제현황은 '환자정보 삭제본'만 사용합니다.
- 실제 약국 자료는 저장소에 넣지 않습니다. 테스트 데이터는 모두 합성입니다([docs/SENSITIVE.md](docs/SENSITIVE.md)).
- 프로그램이 인터넷으로 무엇을 주고받는지는 [PRIVACY.md](PRIVACY.md)에 정리했습니다.
- 데이터 출처: 보건복지부 고시, 건강보험심사평가원 약가마스터(공공누리 제1유형). 서드파티 라이선스는 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)에 있습니다.

## Code signing policy

> 코드 서명 정책 · 현재 **SignPath Foundation 무료 서명 신청 준비 중**입니다. 승인 전 릴리스는 서명되지 않았습니다.

Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org) (once approved).

- 서명 대상: 이 저장소의 소스에서 GitHub Actions로 빌드한 `DPR.exe`만 서명합니다. 다른 프로젝트의 바이너리는 서명하지 않습니다.
- 모든 릴리스는 승인자가 직접 승인한 뒤에만 서명됩니다.

| 역할 | 구성원 |
|---|---|
| 작성자(Committers) | [bullish-lee](https://github.com/bullish-lee) |
| 검토자(Reviewers) | [bullish-lee](https://github.com/bullish-lee) |
| 승인자(Approvers) | [bullish-lee](https://github.com/bullish-lee) |

개인정보: [PRIVACY.md](PRIVACY.md) — 이 프로그램은 사용자가 요청하거나 아래에 적은 경우가 아니면 어떤 정보도 다른 시스템으로 보내지 않습니다.

## 라이선스
[MIT](LICENSE) © 2026 bullish-lee. 함께 쓰는 라이브러리의 라이선스는 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)에 있습니다.
