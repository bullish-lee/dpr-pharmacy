# Third-Party Notices

DPR 에 포함되었거나 링크되는 주요 서드파티 구성요소와 라이선스입니다.

| 구성요소 | 라이선스 | 비고 |
|---|---|---|
| Wails v2 (github.com/wailsapp/wails) | MIT | 데스크탑 앱 셸 |
| go-toast v2 (git.sr.ht/~jackmordaunt/go-toast) | MIT / Unlicense | Windows 알림 |
| excelize v2 (github.com/xuri/excelize) | BSD-3-Clause | 엑셀 읽기 |
| modernc.org/sqlite | BSD-3-Clause | SQLite (순수 Go) |
| golang.org/x/net, golang.org/x/text | BSD-3-Clause | HTML 파싱, CP949, 유니코드 정규화 |
| React, React DOM | MIT | UI |
| TanStack Query, TanStack Virtual | MIT | 데이터 조회·가상 스크롤 |
| Tailwind CSS | MIT | 스타일 |
| lucide-react | ISC | 아이콘 |
| sonner | MIT | 알림 토스트 |
| clsx | MIT | |
| zxing-wasm | MIT | 바코드 판독 (WASM) |
| zxing-cpp (zxing-wasm 에 포함) | Apache-2.0 | 바코드 판독 엔진 |
| bwip-js (BWIPP 포함) | MIT | GS1 DataMatrix 생성 |
| es-hangul | MIT | 한글 → QWERTY 복원 |
| Pretendard | SIL Open Font License 1.1 | 글꼴 (앱에 내장) |
| PaddleOCR 모델 PP-OCRv5_mobile_det · korean_PP-OCRv5_mobile_rec (PaddlePaddle, ONNX) | Apache-2.0 | 사입정보 사진 글자 인식 모델·글자 사전 (앱에 내장, 받은 그대로 — 고치지 않음) |
| ONNX Runtime 1.22.1 (Microsoft, onnxruntime.dll) | MIT | 글자 인식 모델 실행 (앱에 내장, Microsoft 전자서명 파일 그대로) |
| purego (github.com/ebitengine/purego) | Apache-2.0 | macOS·Linux 개발·시험에서만 사용 (Windows 배포본에는 들어가지 않음) |

## 데이터 출처 표시
- 보건복지부 「약제 급여 목록 및 급여 상한금액표」 고시 (공공저작물)
- 건강보험심사평가원 「약가마스터_의약품표준코드」 — 공공누리 제1유형 (출처 표시)

## 글자 인식 모델·실행기 출처
- PaddleOCR 모델: Copyright (c) PaddlePaddle Authors. Apache License 2.0 (전문: https://www.apache.org/licenses/LICENSE-2.0)
  - https://huggingface.co/PaddlePaddle/PP-OCRv5_mobile_det_onnx (판 e6f4fa85)
  - https://huggingface.co/PaddlePaddle/korean_PP-OCRv5_mobile_rec_onnx (판 5c6f574b)
- ONNX Runtime: Copyright (c) Microsoft Corporation. MIT License — NuGet `Microsoft.ML.OnnxRuntime` 1.22.1 의 `runtimes/win-x64/native/onnxruntime.dll`.
  ONNX Runtime 에 포함된 구성요소의 고지는 그 패키지의 `ThirdPartyNotices.txt` 를 참고하세요.
- 파일별 주소와 SHA-256 은 `internal/platform/paddle.go` 에 있습니다.

각 라이선스 전문은 해당 패키지(node_modules/, Go 모듈 캐시)의 LICENSE 파일을 참고하세요.
