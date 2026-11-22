# 빌드는 바뀐 부분만 다시 한다.
#   - npm install: package.json·lock 이 바뀔 때만
#   - 화면 빌드(vite): frontend 소스가 바뀔 때만 (타입 검사는 make check 에서)
#   - Wails 바인딩: app.go 의 함수·구조체를 바꿨을 때만 make bindings
# 변경 없는 make windows ≈ 0.5초 (이전 5~7초)

WAILS   ?= $(shell command -v wails 2>/dev/null || echo $(HOME)/go/bin/wails)
# VERSION 의 공백·\r 은 잘라 낸다 (Windows 에서 CRLF 로 받아도 버전에 섞이지 않게)
VERSION := $(shell tr -d " \r\n" < VERSION)
LDFLAGS := -X dpr/internal/service.AppVersion=$(VERSION)
FRONT_SRC := $(shell find frontend/src frontend/public -type f 2>/dev/null) frontend/index.html frontend/vite.config.ts frontend/tsconfig.json
WBUILD  := $(WAILS) build -s -skipbindings -m -nosyncgomod -ldflags "$(LDFLAGS)"
# Windows: mingw-w64 가 있으면 C SQLite(cgo, 조회 약 2배 빠름)로, 없으면 순수 Go SQLite 로 빌드한다
WIN_CC  := $(shell command -v x86_64-w64-mingw32-gcc 2>/dev/null)
ifneq ($(WIN_CC),)
WINENV  := CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++
WBUILDW := $(WAILS) build -s -skipbindings -m -nosyncgomod -ldflags "$(LDFLAGS) -extldflags=-static"
else
WINENV  := CGO_ENABLED=0
WBUILDW := $(WBUILD)
endif

.PHONY: dev test check vet-mac vet-win gotest tsc build windows web bindings release sensitive clean keygen public

dev:
	$(WAILS) dev

frontend/node_modules/.stamp: frontend/package.json frontend/package-lock.json
	cd frontend && npm install --no-audit --no-fund
	@touch $@

frontend/dist/index.html: frontend/node_modules/.stamp $(FRONT_SRC)
	cd frontend && npx vite build --logLevel warn

web: frontend/dist/index.html

# app.go 의 바인딩(함수·구조체)을 바꾼 뒤 한 번 실행 → frontend/wailsjs 갱신
bindings:
	$(WAILS) generate module
	@chmod 644 frontend/wailsjs/runtime/* 2>/dev/null || true

test:
	go test ./internal/... -timeout 300s

# 검사 4가지를 동시에 (Go vet ×2 · Go 테스트 · 화면 타입 검사)
check:
	@$(MAKE) -j4 --no-print-directory vet-mac vet-win gotest tsc && echo "check OK"
vet-mac:
	@go vet ./...
vet-win:
	@GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet ./...
gotest:
	@go test ./internal/... -timeout 300s
tsc: frontend/node_modules/.stamp
	@cd frontend && npx tsc --noEmit

build: web
	$(WBUILD)

windows: web
	$(WINENV) $(WBUILDW) -platform windows/amd64 -o DPR.exe

# 배포용 zip: build/release/DPR-v<버전>-windows.zip (DPR.exe + 사용법 + 고지)
release: web
	@# 커밋하지 않은 변경이 있으면 멈추고, 지난 릴리스 뒤 바뀐 업데이트·빌드 관련 파일을 보여 준다 (docs/RELEASING.md)
	@go run ./tools/relsign precheck
	@sed -i '' -e 's/"productVersion": "[^"]*"/"productVersion": "$(VERSION)"/' wails.json
	$(WINENV) $(WBUILDW) -platform windows/amd64 -o DPR.exe
	@rm -rf build/release/pkg && mkdir -p build/release/pkg
	@cp build/bin/DPR.exe build/release/pkg/
	@cp docs/RELEASE_README.txt build/release/pkg/README.txt
	@cp THIRD_PARTY_NOTICES.md build/release/pkg/
	@[ -f LICENSE ] && cp LICENSE build/release/pkg/ || true
	@cd build/release/pkg && rm -f ../DPR-v$(VERSION)-windows.zip && zip -qr ../DPR-v$(VERSION)-windows.zip .
	@# 체크섬 + 개발자 서명 (비밀 키 ~/.config/dpr/release-signing.key, 허용 목록 ~/.config/dpr/trusted-keys) → 앱과 같은 방법으로 바로 확인
	@go run ./tools/relsign sign -version $(VERSION) build/release/DPR-v$(VERSION)-windows.zip
	@go run ./tools/relsign verify -version $(VERSION) build/release/DPR-v$(VERSION)-windows.zip
	@echo "→ build/release/DPR-v$(VERSION)-windows.zip (+ .sha256, .sig)"
	@cat build/release/DPR-v$(VERSION)-windows.zip.sha256

# 릴리스 서명 키 만들기 (한 번만, 이미 있으면 거부). 만든 뒤 출력된 공개 키를 internal/updater/keys.go 에 넣는다
keygen:
	go run ./tools/relsign keygen

clean:
	rm -rf build/bin build/release frontend/dist/assets

# 공개 저장소(오픈소스)로 내보내기: 점검(민감자료 표시·실제 용어 대조) → 복사. 커밋·푸시는 그 폴더에서 확인 뒤에.
#   make public PUBLIC=../dpr-pharmacy
PUBLIC ?= ../dpr-pharmacy
public:
	@go run ./tools/pubsync $(PUBLIC)

# 실데이터 표시 목록 (docs/SENSITIVE.md — 비공개 저장소의 목록·절차는 docs/PRIVATE_SENSITIVE.md)
sensitive:
	@grep -rn --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=dist --exclude-dir=wailsjs --exclude=SENSITIVE.md --exclude=Makefile "민감자료" . || true
	@echo "--- GitHub 이슈 (라벨 민감자료)"; gh issue list --label 민감자료 --state all 2>/dev/null || echo "(gh 없음)"
