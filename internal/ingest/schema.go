// Package ingest 는 시트(2차원 문자열)를 업무 레코드로 바꾸는 파서 모음이다.
//
// 설계 원칙 — "어떤 양식이 와도 유연하게"
//  1. 각 가져오기 종류는 Schema(필드 + 열 이름 동의어)로 정의한다. 코드에 열 순서를 고정하지 않는다.
//  2. 헤더 행을 자동으로 찾고(상단 20행 중 필드가 가장 많이 맞는 행), 동의어로 열을 자동 매핑한다.
//  3. 필수 필드를 못 찾으면 '매핑 필요' 미리보기를 돌려주고, 사용자가 화면에서 열을 지정한다.
//     지정한 매핑은 헤더 서명(signature)으로 저장되어 같은 양식은 다음부터 자동 적용된다.
//  4. 이팜 외 다른 약국 프로그램(유팜, PM+20 등)도 동의어 추가 또는 매핑 저장만으로 지원한다.
package ingest

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Synonyms []string `json:"synonyms"`
	Required bool     `json:"required"`
	Help     string   `json:"help"`
}

type Schema struct {
	Kind   string  `json:"kind"`
	Label  string  `json:"label"`
	Fields []Field `json:"fields"`
}

func (s Schema) Field(key string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// NormalizeHeader : '비  고' → '비고', '낱알 재고' → '낱알재고', '사  용\n장려금' → '사용장려금'
func NormalizeHeader(s string) string {
	s = strings.ToLower(norm.NFKC.String(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// 이팜 사입현황(일괄) / 약품별 사입현황(제조번호·유효기간 포함) / 타 프로그램 공통
var PurchaseSchema = Schema{Kind: "purchase", Label: "사입현황", Fields: []Field{
	{Key: "date", Label: "일자", Required: true, Synonyms: []string{"일자", "날짜", "거래일", "거래일자", "사입일", "사입일자", "입고일", "입고일자", "매입일", "매입일자"}},
	{Key: "code", Label: "약품코드", Required: true, Synonyms: []string{"약품코드", "보험코드", "제품코드", "청구코드", "edi코드", "edi", "표준코드", "코드"}, Help: "9자리 보험코드"},
	{Key: "name", Label: "약품명", Synonyms: []string{"약품명", "제품명", "품명", "품목명", "약명"}},
	{Key: "supplier", Label: "거래처", Required: true, Synonyms: []string{"거래처", "거래처명", "도매", "도매처", "도매상", "도매상명", "공급처", "공급자", "매입처"}},
	{Key: "kind", Label: "구분", Synonyms: []string{"구분", "거래구분", "입출고구분", "사입구분"}, Help: "사입/반품/폐기/재고조정/차용입고/차용출고 (없으면 수량 부호로 판단)"},
	{Key: "pack_size", Label: "단위수량", Synonyms: []string{"단위수량", "포장수량", "입수", "포장단위수량"}, Help: "포장 1개에 든 낱개 수 (예: 30)"},
	{Key: "pack_qty", Label: "수량", Synonyms: []string{"수량", "포장수", "입고수량", "사입수량", "매입수량"}, Help: "포장 개수"},
	{Key: "total_qty", Label: "총수량", Synonyms: []string{"총수량", "낱알수량", "총낱알", "총입고수량"}, Help: "낱개 기준 총량 (있으면 우선 사용)"},
	{Key: "price", Label: "단가", Synonyms: []string{"단가", "사입단가", "매입단가", "공급단가"}, Help: "포장 1개 단가"},
	{Key: "amount", Label: "합계", Synonyms: []string{"합계", "합계액", "금액", "합계금액", "공급가액", "매입금액"}},
	{Key: "unit", Label: "단위", Synonyms: []string{"단위", "포장단위"}},
	{Key: "lot", Label: "제조번호", Synonyms: []string{"제조번호", "로트", "로트번호", "lot", "lotno", "배치번호", "제조번호lot"}},
	{Key: "expiry", Label: "유효기간", Synonyms: []string{"유효기간", "유효일자", "사용기한", "유통기한", "유효기한", "exp", "expiry", "expdate"}},
}}

var DispenseSchema = Schema{Kind: "dispense", Label: "조제현황", Fields: []Field{
	{Key: "date", Label: "일자", Required: true, Synonyms: []string{"일자", "날짜", "조제일", "조제일자", "투약일", "투약일자", "처방일", "처방일자"}},
	{Key: "code", Label: "약품코드", Required: true, Synonyms: []string{"약품코드", "보험코드", "제품코드", "청구코드", "edi코드", "코드"}},
	{Key: "name", Label: "약품명", Synonyms: []string{"약품명", "제품명", "품명", "약명"}},
	{Key: "total_qty", Label: "총수량", Required: true, Synonyms: []string{"총수량", "총투약량", "조제수량", "총사용량", "사용량"}, Help: "청구 단위(고시 단위) 총 조제량"},
	{Key: "unit", Label: "단위", Synonyms: []string{"단위"}},
	{Key: "pack_size", Label: "단위수량", Synonyms: []string{"단위수량"}, Help: "청구 단위 1개의 크기 (고시 규격과 같아야 함, 단위 확인용)"},
}}

// 초기재고·재고 스냅샷 (표준 양식 + 이팜/수기 정리 양식)
var StockSchema = Schema{Kind: "stock", Label: "재고", Fields: []Field{
	{Key: "code", Label: "약품코드", Synonyms: []string{"약품코드", "보험코드", "제품코드", "청구코드", "코드"}, Help: "없으면 약품명으로 매칭"},
	{Key: "name", Label: "약품명", Synonyms: []string{"약품명", "제품명", "품명", "약명"}},
	{Key: "qty", Label: "재고수량", Required: true, Synonyms: []string{"낱알재고", "실재고", "재고수량", "현재고", "현재고량", "재고", "수량", "실사수량", "계수"}, Help: "낱개 기준 (제품규격이 있으면 포장 개수로 간주)"},
	{Key: "unit_price", Label: "단가", Synonyms: []string{"사입단가", "단가", "매입단가", "매출단가"}},
	{Key: "spec", Label: "제품규격", Synonyms: []string{"제품규격", "규격", "포장규격"}, Help: "'30T' 처럼 포장 단위로 센 경우"},
	{Key: "unit", Label: "재고단위", Synonyms: []string{"재고단위", "단위"}},
	{Key: "location", Label: "보관위치", Synonyms: []string{"보관위치", "위치"}},
}}

// 제조번호·유효기간 사입 근거 (이팜 약품별 사입현황 등). 약품코드가 없으면 화면에서 약품을 지정한다.
var LotSchema = Schema{Kind: "lot", Label: "사입 근거(제조번호)", Fields: []Field{
	{Key: "date", Label: "일자", Synonyms: []string{"일자", "날짜", "사입일", "사입일자", "입고일", "입고일자"}},
	{Key: "code", Label: "약품코드", Synonyms: []string{"약품코드", "보험코드", "제품코드", "코드"}},
	{Key: "name", Label: "약품명", Synonyms: []string{"약품명", "제품명", "품명"}},
	{Key: "supplier", Label: "거래처", Required: true, Synonyms: []string{"거래처", "거래처명", "도매", "도매처", "공급처", "매입처"}},
	{Key: "pack_qty", Label: "수량", Synonyms: []string{"수량", "포장수"}},
	{Key: "total_qty", Label: "총수량", Synonyms: []string{"총수량", "낱알수량"}},
	{Key: "lot", Label: "제조번호", Required: true, Synonyms: []string{"제조번호", "로트", "로트번호", "lot", "lotno", "배치번호"}},
	{Key: "expiry", Label: "유효기간", Synonyms: []string{"유효기간", "유효일자", "사용기한", "유통기한", "exp", "expiry"}},
}}

// 심평원 '약가마스터_의약품표준코드'(공공데이터포털, 분기 갱신) 등 표준코드↔보험코드 매핑
var GTINSchema = Schema{Kind: "gtin", Label: "표준코드(바코드) 매핑", Fields: []Field{
	{Key: "gtin", Label: "표준코드", Required: true, Synonyms: []string{"표준코드", "바코드", "gtin", "상품바코드", "의약품표준코드"}, Help: "880으로 시작하는 13자리"},
	{Key: "code", Label: "제품코드", Required: true, Synonyms: []string{"제품코드개정후", "제품코드", "보험코드", "약품코드", "청구코드"}, Help: "9자리 보험코드"},
	{Key: "name", Label: "제품명", Synonyms: []string{"한글상품명", "제품명", "품목명", "약품명", "상품명"}},
	{Key: "pack", Label: "제품총수량", Synonyms: []string{"제품총수량", "총수량", "포장수량", "포장단위수량"}, Help: "포장 1개에 든 낱개 수"},
}}

// 심평원 「약제급여목록 및 급여상한금액표」 (월별 전체 목록, 공개용). 고시 단위(규격·단위)와 상한금액의 기준.
var DrugMasterSchema = Schema{Kind: "drug_master", Label: "약제급여목록표", Fields: []Field{
	{Key: "code", Label: "제품코드", Required: true, Synonyms: []string{"제품코드", "보험코드", "약품코드", "청구코드"}},
	{Key: "name", Label: "제품명", Required: true, Synonyms: []string{"제품명", "품명", "약품명"}},
	{Key: "company", Label: "업체명", Synonyms: []string{"업체명", "제약사", "제조사", "회사명"}},
	{Key: "spec", Label: "규격", Required: true, Synonyms: []string{"규격"}},
	{Key: "unit", Label: "단위", Required: true, Synonyms: []string{"단위"}},
	{Key: "price", Label: "상한금액", Required: true, Synonyms: []string{"상한금액표금액", "상한금액", "상한가", "상한금액원"}},
	{Key: "route", Label: "투여", Synonyms: []string{"투여", "투여경로"}},
	{Key: "ingredient_code", Label: "주성분코드", Synonyms: []string{"주성분코드"}},
	{Key: "otc", Label: "전문/일반", Synonyms: []string{"전일", "전문일반", "전문일반구분"}},
	{Key: "note", Label: "비고", Synonyms: []string{"비고"}},
}}

func SchemaByKind(kind string) (Schema, bool) {
	switch kind {
	case "purchase":
		return PurchaseSchema, true
	case "dispense":
		return DispenseSchema, true
	case "stock", "initial_stock":
		return StockSchema, true
	case "lot":
		return LotSchema, true
	case "gtin":
		return GTINSchema, true
	case "drug_master":
		return DrugMasterSchema, true
	}
	return Schema{}, false
}
