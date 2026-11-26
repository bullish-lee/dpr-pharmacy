package main

// 휴대폰 연결 — 같은 와이파이(사내망)의 휴대폰 브라우저에서 사입정보 사진·포장 바코드를 찍어 이 PC 의 DPR 로 바로 보낸다.
//
//   - DPR 이 켜져 있는 동안만, 설정에서 켠 경우에만 작은 웹 서버를 연다 (기본 꺼짐).
//   - 휴대폰은 QR 코드로 들어온다. 주소에 연결 비밀번호(토큰)가 들어 있고, 비밀번호가 없거나 틀리면 자료를 주지 않는다.
//   - 사내망(사설 IP) 에서 온 연결만 받는다. 인터넷으로 나가거나 외부 서비스를 쓰지 않는다.
//   - 사입정보 사진: PC 에서 글자 인식 → '휴대폰에서 온 사진'으로 3단계 화면에 쌓아 두고, PC 에서 확인한 뒤 저장한다.
//   - 바코드: 휴대폰이 사진에서 바코드를 읽어 보내면 바로 5단계 스캔 기록에 넣는다 (스캐너로 찍은 것과 같다).

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"dpr/internal/service"
)

const (
	setPhoneEnabled  = "phone_enabled" // "1" | "0"
	setPhoneToken    = "phone_token"   // 연결 비밀번호 (QR 주소에 들어감)
	setPhonePort     = "phone_port"    // 기본 17345
	defaultPhonePort = 17345
	phoneMaxBody     = 40 << 20 // 사진 한 장 (base64) 최대 40MB
	phoneInboxMax    = 40
)

// PhoneItem : 휴대폰에서 온 사입정보 사진 한 장 (PC 에서 확인·저장하기 전까지 보관, 앱을 끄면 사라짐)
type PhoneItem struct {
	ID          int64                   `json:"id"`
	Date        string                  `json:"date"` // 회차 시행일
	Code        string                  `json:"code"`
	Name        string                  `json:"name"`
	ReceivedAt  string                  `json:"receivedAt"`
	ImageBase64 string                  `json:"imageBase64"`
	MediaType   string                  `json:"mediaType"`
	Result      *service.LotPhotoResult `json:"result"`
	Error       string                  `json:"error"`
}

// PhoneStatus : 설정·연결 창에 보여 줄 상태
type PhoneStatus struct {
	Enabled     bool     `json:"enabled"`
	Running     bool     `json:"running"`
	Port        int      `json:"port"`
	URLs        []string `json:"urls"` // 휴대폰에서 열 주소 (첫 번째를 QR 로). 비밀번호는 # 뒤 — 서버·중계로 전송되지 않는다
	Error       string   `json:"error"`
	Pending     int      `json:"pending"`     // 아직 PC 에서 확인하지 않은 휴대폰 사진
	Relay       bool     `json:"relay"`       // 외부 중계(모바일 데이터·약국 밖) 사용
	RelayStatus string   `json:"relayStatus"` // "" | downloading | starting | ready | error
	RelayError  string   `json:"relayError"`
}

type phoneHub struct {
	mu    sync.Mutex
	srv   *http.Server
	port  int
	err   string
	inbox []*PhoneItem
	seq   int64
	fails map[string]*failCount // 비밀번호를 틀린 횟수 (연결한 곳마다)
}

// 비밀번호를 10분 안에 8번 틀리면 그 연결은 15분 동안 막는다 (중계로 인터넷에 열려 있을 때 대비)
const (
	failWindow = 10 * time.Minute
	failMax    = 8
	failBlock  = 15 * time.Minute
)

type failCount struct {
	n            int
	first, until time.Time
}

func (a *App) phoneToken() string {
	t := a.svc.DB.GetSetting(setPhoneToken, "")
	if t == "" {
		t = newPhoneToken()
		_ = a.svc.DB.SetSetting(setPhoneToken, t)
	}
	return t
}

func newPhoneToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// phoneAutoStart : 앱을 켤 때 설정이 켜져 있으면 연다
func (a *App) phoneAutoStart() {
	if a.svc.DB.GetSetting(setPhoneEnabled, "0") == "1" {
		if err := a.startPhone(); err != nil {
			a.log.Warn("휴대폰 연결을 열지 못함", "err", err)
		}
	}
}

func (a *App) startPhone() error {
	h := &a.phone
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv != nil {
		return nil
	}
	port, _ := strconv.Atoi(a.svc.DB.GetSetting(setPhonePort, ""))
	if port <= 0 || port > 65535 {
		port = defaultPhonePort
	}
	// 외부 중계를 쓰면 이 PC 안(127.0.0.1)에서만 연다 — 중계 프로그램만 들어올 수 있고, 방화벽 창도 뜨지 않는다
	host := ""
	relay := a.relayEnabled()
	if relay {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		// 정해 둔 번호를 다른 프로그램이 쓰고 있으면 빈 번호로 (QR 을 다시 찍어야 한다)
		ln, err = net.Listen("tcp", host+":0")
		if err != nil {
			h.err = err.Error()
			return fmt.Errorf("휴대폰 연결을 열지 못했습니다: %w", err)
		}
		port = ln.Addr().(*net.TCPAddr).Port
		_ = a.svc.DB.SetSetting(setPhonePort, strconv.Itoa(port))
	}
	h.port, h.err = port, ""
	h.srv = &http.Server{Handler: a.phoneHandler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 3 * time.Minute, WriteTimeout: 3 * time.Minute}
	srv := h.srv
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.log.Warn("휴대폰 연결 서버 멈춤", "err", err)
			h.mu.Lock()
			h.err, h.srv = err.Error(), nil
			h.mu.Unlock()
		}
	}()
	a.log.Info("휴대폰 연결 열림", "port", port, "relay", relay)
	if relay {
		a.startRelay(port)
	}
	return nil
}

func (a *App) stopPhone() {
	a.stopRelay()
	h := &a.phone
	h.mu.Lock()
	srv := h.srv
	h.srv = nil
	h.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		a.log.Info("휴대폰 연결 닫힘")
	}
}

// ── 화면(PC)에 노출되는 함수 ────────────────────────────────────────

func (a *App) PhoneStatus() PhoneStatus {
	h := &a.phone
	h.mu.Lock()
	st := PhoneStatus{Enabled: a.svc.DB.GetSetting(setPhoneEnabled, "0") == "1", Running: h.srv != nil, Port: h.port, Error: h.err, Pending: len(h.inbox), Relay: a.relayEnabled()}
	h.mu.Unlock()
	if !st.Running {
		return st
	}
	tok := a.phoneToken()
	if st.Relay {
		status, url, rerr := a.relay.snapshot()
		st.RelayStatus, st.RelayError = status, rerr
		if status == "ready" && url != "" {
			st.URLs = []string{url + "/phone.html#t=" + tok}
		}
		return st
	}
	for _, ip := range lanIPs() {
		st.URLs = append(st.URLs, fmt.Sprintf("http://%s:%d/phone.html#t=%s", ip, st.Port, tok))
	}
	if len(st.URLs) == 0 {
		st.Error = "이 PC 의 사내망 주소를 찾지 못했습니다. 인터넷 공유기에 연결되어 있는지 확인하세요."
	}
	return st
}

// SetPhoneRelay : 외부 중계 켜기·끄기 (모바일 데이터·약국 밖에서 쓰기). 연결 중이면 새 방식으로 다시 연다.
func (a *App) SetPhoneRelay(on bool) (PhoneStatus, error) {
	if err := a.svc.DB.SetSetting(setPhoneRelay, map[bool]string{true: "1", false: "0"}[on]); err != nil {
		return a.PhoneStatus(), err
	}
	a.log.Info("휴대폰 외부 중계 설정", "on", on)
	if a.svc.DB.GetSetting(setPhoneEnabled, "0") == "1" {
		a.stopPhone()
		if err := a.startPhone(); err != nil {
			return a.PhoneStatus(), err
		}
	}
	return a.PhoneStatus(), nil
}

// SetPhoneEnabled : 휴대폰 연결 켜기·끄기 (켜 두면 다음에 DPR 을 켤 때도 자동으로 열림)
func (a *App) SetPhoneEnabled(on bool) (PhoneStatus, error) {
	v := map[bool]string{true: "1", false: "0"}[on]
	if err := a.svc.DB.SetSetting(setPhoneEnabled, v); err != nil {
		return a.PhoneStatus(), err
	}
	if on {
		if err := a.startPhone(); err != nil {
			return a.PhoneStatus(), err
		}
	} else {
		a.stopPhone()
	}
	return a.PhoneStatus(), nil
}

// ResetPhoneToken : 연결 비밀번호를 새로 만든다 — 예전 QR·홈 화면 아이콘으로는 더 이상 들어올 수 없다
func (a *App) ResetPhoneToken() PhoneStatus {
	_ = a.svc.DB.SetSetting(setPhoneToken, newPhoneToken())
	a.log.Info("휴대폰 연결 비밀번호를 새로 만듦")
	return a.PhoneStatus()
}

// PhoneInbox : 휴대폰에서 온, 아직 PC 에서 확인하지 않은 사입정보 사진 (date 가 비면 전부)
func (a *App) PhoneInbox(date string) []PhoneItem {
	h := &a.phone
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []PhoneItem{}
	for _, it := range h.inbox {
		if date == "" || it.Date == date {
			out = append(out, *it)
		}
	}
	return out
}

// DismissPhoneItem : 확인·저장했거나 버린 휴대폰 사진을 목록에서 뺀다
func (a *App) DismissPhoneItem(id int64) {
	h := &a.phone
	h.mu.Lock()
	for i, it := range h.inbox {
		if it.ID == id {
			h.inbox = append(h.inbox[:i], h.inbox[i+1:]...)
			break
		}
	}
	h.mu.Unlock()
	a.emit("phone-inbox", nil)
}

func (a *App) emit(name string, data any) {
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, name, data)
	}
}

// ── 휴대폰 쪽 (HTTP) ─────────────────────────────────────────────────

func (a *App) phoneHandler() http.Handler {
	mux := http.NewServeMux()
	dist, _ := fs.Sub(assets, "frontend/dist")
	files := http.FileServer(http.FS(dist))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/":
			// 휴대폰 화면으로 (비밀번호가 담긴 주소 그대로)
			http.Redirect(w, r, "/phone.html?"+r.URL.RawQuery, http.StatusFound)
		case r.URL.Path == "/phone.html", strings.HasPrefix(r.URL.Path, "/assets/"):
			files.ServeHTTP(w, r) // 화면 파일 (자료는 없음)
		default:
			http.NotFound(w, r)
		}
	})
	api := func(path string, fn func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if !privateClient(r.RemoteAddr) {
				http.Error(w, "사내망에서만 연결할 수 있습니다", http.StatusForbidden)
				return
			}
			who := clientKey(r)
			if a.phoneBlocked(who) {
				phoneJSON(w, http.StatusTooManyRequests, map[string]string{"error": "연결 비밀번호를 여러 번 틀려 15분 동안 막았습니다. PC 의 DPR 에서 QR 코드를 다시 찍어 주세요."})
				return
			}
			tok := r.Header.Get("X-DPR-Token")
			if tok == "" {
				tok = r.URL.Query().Get("t")
			}
			if subtle.ConstantTimeCompare([]byte(tok), []byte(a.phoneToken())) != 1 {
				a.phoneFail(who)
				phoneJSON(w, http.StatusUnauthorized, map[string]string{"error": "연결 비밀번호가 맞지 않습니다. PC 의 DPR 에서 QR 코드를 다시 찍어 주세요."})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, phoneMaxBody)
			fn(w, r)
		})
	}

	api("/api/info", func(w http.ResponseWriter, r *http.Request) {
		phoneJSON(w, 200, map[string]any{"version": service.AppVersion, "today": a.svc.Today()})
	})

	// 회차 목록: 보유 인하 품목이 있는 회차 (가까운 순)
	api("/api/cases", func(w http.ResponseWriter, r *http.Request) {
		ds, err := a.svc.PriceCutDates()
		if err != nil {
			phoneErr(w, err)
			return
		}
		type item struct {
			Date string `json:"date"`
			Held int    `json:"held"`
		}
		out := []item{}
		for _, d := range ds {
			if d.HeldDown > 0 || d.HeldDelete > 0 {
				out = append(out, item{d.EffectiveDate, d.HeldDown})
			}
		}
		phoneJSON(w, 200, map[string]any{"cases": out, "today": a.svc.Today()})
	})

	// 회차의 보유 품목 (실계수·사입 근거·바코드 현황)
	api("/api/case", func(w http.ResponseWriter, r *http.Request) {
		date := r.URL.Query().Get("date")
		rk, err := a.svc.Ranking(date, true)
		if err != nil {
			phoneErr(w, err)
			return
		}
		late := rk.EffectiveDate < a.svc.Today()
		codes := make([]string, 0, len(rk.Rows))
		for _, row := range rk.Rows {
			codes = append(codes, row.Code)
		}
		ev, _ := a.svc.ListLotEvidence(codes)
		evN := map[string]int{}
		for _, e := range ev {
			evN[e.Code]++
		}
		// 휴대폰에는 꼭 필요한 것만 보낸다: 약품 이름·코드, 실계수를 넣었는지(숫자는 안 보냄), 사입근거·바코드 건수.
		// 재고·실계수 수량·가격·손실 금액은 보내지 않는다 (외부 중계를 거칠 수 있으므로).
		type item struct {
			Code    string `json:"code"`
			Name    string `json:"name"`
			Counted bool   `json:"counted"`
			Lots    int    `json:"lots"`
			Scanned int    `json:"scanned"`
		}
		out := []item{}
		for _, row := range rk.Rows {
			if row.Stock <= 0 {
				continue
			}
			cq := row.CountQty
			if late {
				cq = row.LaterCountQty
			}
			out = append(out, item{Code: row.Code, Name: row.Name, Counted: cq != nil, Lots: evN[row.Code], Scanned: row.Scan.Units})
		}
		// 실계수를 넣은 약품을 먼저 (그 안에서는 손실 순위 그대로)
		sort.SliceStable(out, func(i, j int) bool { return out[i].Counted && !out[j].Counted })
		phoneJSON(w, 200, map[string]any{"date": rk.EffectiveDate, "rows": out})
	})

	// 사입정보 사진 → 글자 인식 → PC 의 3단계 화면에 '휴대폰에서 온 사진'으로
	api("/api/lot-photo", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Date, Code, Name, ImageBase64, MediaType string
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			phoneJSON(w, 400, map[string]string{"error": "사진이 너무 크거나 전송이 끊겼습니다. 다시 찍어 주세요."})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		res, err := a.svc.ReadLotPhoto(ctx, service.LotPhotoRequest{ImageBase64: in.ImageBase64, Code: in.Code})
		it := &PhoneItem{Date: in.Date, Code: in.Code, Name: in.Name, ReceivedAt: time.Now().Format(time.RFC3339), ImageBase64: in.ImageBase64, MediaType: in.MediaType, Result: res}
		if err != nil {
			it.Error = err.Error()
		}
		h := &a.phone
		h.mu.Lock()
		h.seq++
		it.ID = h.seq
		h.inbox = append(h.inbox, it)
		if len(h.inbox) > phoneInboxMax {
			h.inbox = h.inbox[len(h.inbox)-phoneInboxMax:]
		}
		h.mu.Unlock()
		a.log.Info("휴대폰 사입정보 사진 받음", "code", in.Code, "rows", rowsOf(res), "err", it.Error)
		a.emit("phone-inbox", map[string]any{"id": it.ID, "date": it.Date, "code": it.Code, "name": it.Name, "rows": rowsOf(res), "error": it.Error})
		// 읽은 내용(날짜·도매·제조번호)은 휴대폰으로 돌려보내지 않는다 — 확인은 PC 에서 한다. 몇 줄 읽었는지만.
		mode := ""
		if res != nil {
			mode = res.Mode
		}
		phoneJSON(w, 200, map[string]any{"id": it.ID, "rows": rowsOf(res), "mode": mode, "error": it.Error})
	})

	// 포장 바코드 (휴대폰이 사진에서 읽은 GS1 문자열들) → 5단계 스캔 기록
	api("/api/barcodes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST", http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Date, Code string
			Raws       []string
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			phoneJSON(w, 400, map[string]string{"error": "전송이 끊겼습니다. 다시 시도해 주세요."})
			return
		}
		// 결과는 상태만 돌려준다 (도매 이름·제조번호는 보내지 않음 — PC 의 5단계 화면에서 본다)
		type item struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Matched bool   `json:"matched"` // 도매를 찾았는지
			Unsure  bool   `json:"unsure"`  // 도매가 여럿이라 PC 에서 지정해야 하는지
		}
		out := []item{}
		for _, raw := range in.Raws {
			res, err := a.svc.RecordScan(service.ScanInput{EffectiveDate: in.Date, ProductCode: in.Code, Raw: raw})
			if err != nil {
				out = append(out, item{Status: "error", Message: "기록하지 못했습니다"})
				continue
			}
			it := item{Status: res.Status}
			if res.Status != "added" && res.Status != "duplicate" {
				it.Message = res.Message
			}
			if res.Unit != nil {
				it.Matched, it.Unsure = res.Unit.SupplierID != 0, res.Unit.SupplierID == 0 && res.Unit.MatchStatus == "ambiguous"
			}
			out = append(out, it)
		}
		a.log.Info("휴대폰 바코드 받음", "code", in.Code, "n", len(in.Raws))
		a.emit("data-changed", "scan")
		phoneJSON(w, 200, map[string]any{"results": out})
	})
	return mux
}

func rowsOf(r *service.LotPhotoResult) int {
	if r == nil {
		return 0
	}
	return len(r.Rows)
}

func phoneJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func phoneErr(w http.ResponseWriter, err error) {
	phoneJSON(w, 500, map[string]string{"error": err.Error()})
}

// clientKey : 비밀번호 틀린 횟수를 셀 단위 — 중계로 온 요청은 중계가 알려 준 휴대폰 주소, 아니면 연결한 주소
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if cf := r.Header.Get("Cf-Connecting-Ip"); cf != "" {
			return cf
		}
	}
	return host
}

func (a *App) phoneBlocked(who string) bool {
	h := &a.phone
	h.mu.Lock()
	defer h.mu.Unlock()
	f := h.fails[who]
	return f != nil && time.Now().Before(f.until)
}

func (a *App) phoneFail(who string) {
	h := &a.phone
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fails == nil {
		h.fails = map[string]*failCount{}
	}
	now := time.Now()
	f := h.fails[who]
	if f == nil || now.Sub(f.first) > failWindow {
		f = &failCount{first: now}
		h.fails[who] = f
	}
	f.n++
	if f.n >= failMax {
		f.until = now.Add(failBlock)
		a.log.Warn("휴대폰 연결: 비밀번호를 여러 번 틀려 잠시 막음", "from", who)
	}
}

// privateClient : 사내망(사설 IP)·이 PC 에서 온 연결만 받는다
func privateClient(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// lanIPs : 휴대폰이 찾아올 이 PC 의 사내망 주소. 인터넷으로 나가는 길의 주소를 맨 앞에 둔다.
func lanIPs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(ip net.IP) {
		if ip == nil || ip.To4() == nil || !ip.IsPrivate() || seen[ip.String()] {
			return
		}
		seen[ip.String()] = true
		out = append(out, ip.String())
	}
	// 실제로 보내지는 않는다 (UDP 는 연결 수립이 없음) — 기본 경로의 내 주소만 알아낸다
	if c, err := net.Dial("udp4", "192.0.2.1:9"); err == nil {
		add(c.LocalAddr().(*net.UDPAddr).IP)
		c.Close()
	}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		n := strings.ToLower(ifc.Name)
		if strings.Contains(n, "vethernet") || strings.Contains(n, "virtual") || strings.Contains(n, "vmware") || strings.Contains(n, "wsl") || strings.Contains(n, "docker") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			if ipn, ok := ad.(*net.IPNet); ok {
				add(ipn.IP)
			}
		}
	}
	return out
}
