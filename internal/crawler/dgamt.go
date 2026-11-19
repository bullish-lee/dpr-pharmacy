package crawler

// 약제급여목록(전체) 자동 받기 — 공공데이터포털 「건강보험심사평가원_약가기준정보조회서비스」(#15054445, 무료, 인증키 필요).
//
//	GET https://apis.data.go.kr/B551182/dgamtCrtrInfoService1.2/getDgamtList?ServiceKey=…&numOfRows=…&pageNo=…
//	응답(XML): response/header/resultCode(00=정상) · body/items/item[] · body/totalCount
//	인증 오류: HTTP 401 + OpenAPI_ServiceResponse/cmmMsgHeader/errMsg (SERVICE_KEY_IS_NULL 등)
//
// 심평원 홈페이지의 엑셀은 프로그램 접속을 막으므로 쓰지 않는다. 인증키는 오류 문장·기록에 절대 넣지 않는다.

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DgamtURL : 약가목록조회 주소 (테스트에서 바꾼다)
var DgamtURL = "https://apis.data.go.kr/B551182/dgamtCrtrInfoService1.2/getDgamtList"

// DgamtItem : 약가기준정보 한 품목 (응답 항목 이름 그대로)
type DgamtItem struct {
	MdsCd      string `xml:"mdsCd"`      // 제품코드
	ItmNm      string `xml:"itmNm"`      // 품목명
	MnfEntpNm  string `xml:"mnfEntpNm"`  // 제조업체명
	NomNm      string `xml:"nomNm"`      // 규격명
	Unit       string `xml:"unit"`       // 단위
	MxCprc     string `xml:"mxCprc"`     // 상한가
	AdtStaDd   string `xml:"adtStaDd"`   // 적용시작일자 (YYYYMMDD)
	GnlNmCd    string `xml:"gnlNmCd"`    // 일반명(주성분)코드
	InjcPthNm  string `xml:"injcPthNm"`  // 투여경로명
	PayTpNm    string `xml:"payTpNm"`    // 급여구분명
	SpcGnlTpNm string `xml:"spcGnlTpNm"` // 전문일반구분명
	ChgBfMdsCd string `xml:"chgBfMdsCd"` // 변경이전약품코드
	ChgAfMdsCd string `xml:"chgAfMdsCd"` // 변경이후약품코드
}

type dgamtPage struct {
	XMLName xml.Name
	Header  struct {
		ResultCode string `xml:"resultCode"`
		ResultMsg  string `xml:"resultMsg"`
	} `xml:"header"`
	Body struct {
		Items struct {
			Item []DgamtItem `xml:"item"`
		} `xml:"items"`
		Item       []DgamtItem `xml:"item"` // items 없이 바로 item 이 오는 경우
		NumOfRows  int         `xml:"numOfRows"`
		PageNo     int         `xml:"pageNo"`
		TotalCount int         `xml:"totalCount"`
	} `xml:"body"`
	CmmMsgHeader struct { // 인증·게이트웨이 오류
		ErrMsg           string `xml:"errMsg"`
		ReturnAuthMsg    string `xml:"returnAuthMsg"`
		ReturnReasonCode string `xml:"returnReasonCode"`
	} `xml:"cmmMsgHeader"`
}

// ErrDgamtKey : 인증키 문제 (없음·미등록·만료) — 사용자가 고쳐야 한다
var ErrDgamtKey = errors.New("공공데이터포털 인증키 문제")

// NormalizeServiceKey : 포털의 '인코딩' 키(%2B 등)를 넣어도 '디코딩' 키로 바꾼다 (주소에 넣을 때 한 번만 인코딩되게)
func NormalizeServiceKey(key string) string {
	key = strings.TrimSpace(key)
	if strings.Contains(key, "%") {
		if k, err := url.QueryUnescape(key); err == nil {
			return k
		}
	}
	return key
}

// ErrDgamtKeyNotReady : 등록되지 않았거나 이 서비스 승인이 아직 퍼지지 않은 키 (ErrDgamtKey 에도 해당).
// 잘못 복사한 키와 승인 직후 키를 서버가 구분해 주지 않는다 — 화면에서 [그래도 저장]을 고르게 한다.
var ErrDgamtKeyNotReady = errors.New("공공데이터포털 인증키가 아직 등록되지 않음")

type keyError struct {
	hint     string
	notReady bool
}

func (e *keyError) Error() string { return ErrDgamtKey.Error() + ": " + e.hint }
func (e *keyError) Is(target error) bool {
	return target == ErrDgamtKey || (e.notReady && target == ErrDgamtKeyNotReady)
}

const notRegisteredHint = "인증키가 등록되지 않았습니다 (활용신청 승인 뒤 1~2시간 걸릴 수 있음)"
const accessDeniedHint = "이 인증키로는 약가기준정보조회서비스를 쓸 수 없습니다 — 활용신청을 했는지 확인하세요 (승인 직후라면 1~2시간 걸릴 수 있음)"

// 포털 오류 코드 (이름·번호) → 인증키 문제
var dgamtKeyCodes = map[string]keyError{
	"SERVICE_KEY_IS_NULL":                 {hint: "인증키가 없습니다"},
	"SERVICE_KEY_IS_NOT_REGISTERED_ERROR": {hint: notRegisteredHint, notReady: true},
	"30":                                  {hint: notRegisteredHint, notReady: true},
	"SERVICE_ACCESS_DENIED_ERROR":         {hint: accessDeniedHint, notReady: true},
	"20":                                  {hint: accessDeniedHint, notReady: true},
	"DEADLINE_HAS_EXPIRED_ERROR":          {hint: "인증키 사용 기간이 끝났습니다 — 공공데이터포털 마이페이지에서 연장하세요"},
	"31":                                  {hint: "인증키 사용 기간이 끝났습니다 — 공공데이터포털 마이페이지에서 연장하세요"},
	"UNREGISTERED_IP_ERROR":               {hint: "등록되지 않은 IP 입니다"},
	"32":                                  {hint: "등록되지 않은 IP 입니다"},
}

// dgamtError : 응답 머리의 오류 → 알기 쉬운 오류. 포털 응답마다 코드가 든 칸이 달라
// (errMsg 에 코드 또는 'SERVICE ERROR', returnAuthMsg 에 코드 또는 한글 설명, returnReasonCode 에 번호) 받은 칸을 차례로 본다.
func dgamtError(fields ...string) error {
	var shown []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if k, ok := dgamtKeyCodes[f]; ok {
			return &keyError{hint: k.hint, notReady: k.notReady}
		}
		if f == "LIMITED_NUMBER_OF_SERVICE_REQUESTS_EXCEEDS_ERROR" || f == "22" {
			return errors.New("공공데이터포털 하루 호출 한도를 넘었습니다 — 내일 다시 받습니다")
		}
		if f != "" {
			shown = append(shown, f)
		}
	}
	return fmt.Errorf("약가기준정보 응답 오류: %.120s", strings.Join(shown, " / "))
}

// fetchDgamtPage : 한 쪽을 받는다. 오류 문장에 주소(인증키 포함)를 넣지 않는다.
func (c *Client) fetchDgamtPage(ctx context.Context, key string, page, rows int) (*dgamtPage, error) {
	q := url.Values{"ServiceKey": {key}, "numOfRows": {strconv.Itoa(rows)}, "pageNo": {strconv.Itoa(page)}}
	var lastErr error
	for attempt := 0; attempt < max(1, c.Retries); attempt++ {
		if attempt > 0 {
			if err := Pause(ctx, time.Duration(attempt*attempt)*c.Backoff); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, DgamtURL+"?"+q.Encode(), nil)
		if err != nil {
			return nil, errors.New("약가기준정보 요청을 만들지 못했습니다")
		}
		req.Header.Set("User-Agent", c.UserAgent)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = errors.New("공공데이터포털에 접속하지 못했습니다") // url.Error 에는 인증키가 든 주소가 있다 — 버린다
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if rerr != nil {
			lastErr = errors.New("약가기준정보 응답을 읽지 못했습니다")
			continue
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("공공데이터포털 응답 오류 (HTTP %d)", resp.StatusCode)
			continue
		}
		denied := resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
		var p dgamtPage
		if err := xml.Unmarshal(body, &p); err != nil {
			if denied { // 'Unauthorized' 같은 글자만 온 경우
				return nil, &keyError{hint: fmt.Sprintf("인증키가 거부되었습니다 (HTTP %d)", resp.StatusCode)}
			}
			return nil, fmt.Errorf("약가기준정보 응답 형식이 다릅니다 (HTTP %d)", resp.StatusCode)
		}
		if h := p.CmmMsgHeader; h.ErrMsg != "" || h.ReturnAuthMsg != "" || h.ReturnReasonCode != "" {
			return nil, dgamtError(h.ErrMsg, h.ReturnAuthMsg, h.ReturnReasonCode)
		}
		if denied {
			return nil, &keyError{hint: fmt.Sprintf("인증키가 거부되었습니다 (HTTP %d)", resp.StatusCode)}
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("공공데이터포털 응답 오류 (HTTP %d)", resp.StatusCode)
		}
		if rc := strings.TrimSpace(p.Header.ResultCode); rc != "" && rc != "00" {
			return nil, dgamtError(rc, p.Header.ResultMsg)
		}
		if len(p.Body.Items.Item) == 0 && len(p.Body.Item) > 0 {
			p.Body.Items.Item = p.Body.Item
		}
		return &p, nil
	}
	return nil, lastErr
}

// FetchDgamtAll 은 약가기준정보 전체 목록을 쪽마다 받는다 (약 2만 2천 품목, 쪽당 1000개 → 20여 번 요청).
// progress 는 (받은 수, 전체 수) — nil 이면 쓰지 않는다.
func (c *Client) FetchDgamtAll(ctx context.Context, key string, progress func(got, total int)) ([]DgamtItem, error) {
	key = NormalizeServiceKey(key)
	if key == "" {
		return nil, fmt.Errorf("%w: 인증키가 없습니다", ErrDgamtKey)
	}
	const rows = 1000
	var out []DgamtItem
	total := -1
	for page := 1; page <= 1000; page++ { // 포털이 쪽당 행 수를 줄여 보내도 전체를 다 받게 넉넉히
		if page > 1 {
			if err := Pause(ctx, 300*time.Millisecond); err != nil {
				return nil, err
			}
		}
		p, err := c.fetchDgamtPage(ctx, key, page, rows)
		if err != nil {
			return nil, err
		}
		if total < 0 {
			total = p.Body.TotalCount
			if total <= 0 { // 전체 수를 모르면 끊긴 목록인지 알 수 없다
				return nil, errors.New("약가기준정보의 전체 품목 수를 알 수 없습니다")
			}
		}
		out = append(out, p.Body.Items.Item...)
		if progress != nil {
			progress(len(out), total)
		}
		if len(p.Body.Items.Item) == 0 || (total > 0 && len(out) >= total) {
			break
		}
	}
	// 도중에 끊긴(빈 쪽이 끼어든) 목록으로 전체를 바꾸면 품목이 사라진다 — 전체 수만큼 다 받았을 때만
	if len(out) < total {
		return nil, fmt.Errorf("약가기준정보를 다 받지 못했습니다 (%d / %d)", len(out), total)
	}
	return out, nil
}

// CheckDgamtKey 는 인증키로 한 품목만 조회해 키가 쓰이는지 확인하고 전체 품목 수를 돌려준다.
func (c *Client) CheckDgamtKey(ctx context.Context, key string) (int, error) {
	key = NormalizeServiceKey(key)
	if key == "" {
		return 0, fmt.Errorf("%w: 인증키가 없습니다", ErrDgamtKey)
	}
	p, err := c.fetchDgamtPage(ctx, key, 1, 1)
	if err != nil {
		return 0, err
	}
	return p.Body.TotalCount, nil
}
