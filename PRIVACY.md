# 개인정보 처리 (Privacy)

급여약품 관리(DPR)는 약국 PC에서만 동작하는 프로그램입니다. **약국의 사입·조제·재고 자료는 PC의 데이터 폴더에만 저장되고, 어디로도 보내지 않습니다.**

## 인터넷을 쓰는 경우

| 무엇 | 접속하는 곳 | 보내는 정보 | 끄는 방법 |
|---|---|---|---|
| 약가 고시 확인 | 보건복지부 `www.mohw.go.kr` | 없음 (공개 게시판·첨부 파일을 받기만 함) | 설정 → 자동 확인 (고시 자동 확인 끄기) |
| 표준코드(바코드 ↔ 보험코드) 새 판 확인 | 공공데이터포털 `www.data.go.kr`, `infuser.odcloud.kr` | 없음 (공개 파일을 받기만 함) | 설정 → 자동 확인 (표준코드 자동 확인 끄기) |
| 약제급여목록 받기 | 공공데이터포털 API `apis.data.go.kr` | 사용자가 넣은 공공데이터포털 인증키 | 인증키를 넣지 않거나 매달 자동 받기를 끄면 접속하지 않음 |
| 새 버전 확인·업데이트 | GitHub `api.github.com`, `github.com` | 없음 (공개 릴리스 정보를 받기만 함) | 설정 → 업데이트 (새 버전 확인 끄기) |
| 오류 보고 | GitHub `github.com` (브라우저) | **사용자가 [오류 보고]를 누를 때만**, 프로그램 기록(로그) | 누르지 않으면 보내지 않음 |

- 위 접속은 일반 HTTPS 요청이라 접속하는 서버는 IP 주소 같은 접속 정보를 볼 수 있습니다.
- 오류 보고는 자동으로 보내지 않습니다. 브라우저에 GitHub 새 이슈 창을 열고, 사용자가 내용을 확인한 뒤 직접 올립니다. 공개 저장소이므로 **올린 이슈는 누구나 볼 수 있습니다.** 보고 내용에서 거래 자료는 빼고, 폴더 경로·파일 이름·토큰은 가립니다.
- 사용 통계, 광고, 추적 기능은 없습니다.

## 저장하는 정보

- 데이터 폴더(기본 `%APPDATA%\DPR`)에 가져온 엑셀 자료, 설정, 기록(로그), 하루 1번 자동 백업(30개)을 저장합니다.
- 공공데이터포털 인증키는 설정 DB에 저장되며 화면에 다시 보여주지 않습니다.
- 조제현황은 '환자정보 삭제본'만 사용합니다. 환자 정보는 다루지 않습니다.
- 프로그램을 지우려면 `DPR.exe`와 데이터 폴더를 지우면 됩니다. 작업 스케줄러에 등록했다면 설정에서 먼저 등록을 해제하세요.

---

**English summary.** DPR runs locally. Pharmacy data never leaves the PC. The app only downloads public government notices and code lists (mohw.go.kr, data.go.kr, odcloud.kr), calls the data.go.kr API with the user's own key if provided, checks GitHub for public releases, and opens a GitHub new-issue page in the browser only when the user clicks "Error report". Each automatic check can be turned off in Settings. No telemetry, ads or tracking.
