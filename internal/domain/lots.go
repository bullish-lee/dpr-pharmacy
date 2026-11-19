package domain

import (
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// 바코드(제조번호·유효기간) → 사입 도매 귀속.
// 같은 제조번호·유효기간이 여러 도매 사입 기록에 있으면 'ambiguous'(확인 필요)로 분류하고
// 사용자가 도매 앱에서 확인한 뒤 프로그램에서 직접 도매를 지정한다.

func NormalizeLot(lot string) string {
	s := strings.ToUpper(norm.NFKC.String(lot))
	return strings.Join(strings.Fields(s), "")
}

func looseLot(lot string) string {
	return strings.NewReplacer("-", "", "_", "", ".", "").Replace(lot)
}

type LotRecord struct {
	SupplierID int64
	Lot        string
	Expiry     string // YYYY-MM-DD 또는 ""
}

type LotMatch struct {
	Status      string  `json:"status"` // unique | ambiguous | none
	SupplierIDs []int64 `json:"supplierIds"`
}

// MatchLot 은 제조번호(+유효기간)로 사입 도매 후보를 찾는다.
// 유효기간이 한쪽에 없으면 제조번호만으로 비교한다. 하이픈 등 구분자 차이는 느슨하게 허용.
func MatchLot(lot, expiry string, records []LotRecord) LotMatch {
	key := NormalizeLot(lot)
	if key == "" {
		return LotMatch{Status: "none"}
	}
	exact, loose := map[int64]bool{}, map[int64]bool{}
	for _, r := range records {
		rl := NormalizeLot(r.Lot)
		if rl == "" {
			continue
		}
		if expiry != "" && r.Expiry != "" && !sameExpiryMonth(expiry, r.Expiry) {
			continue
		}
		if rl == key {
			exact[r.SupplierID] = true
		} else if looseLot(rl) == looseLot(key) {
			loose[r.SupplierID] = true
		}
	}
	found := exact
	if len(found) == 0 {
		found = loose
	}
	if len(found) == 0 {
		return LotMatch{Status: "none"}
	}
	ids := make([]int64, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	status := "unique"
	if len(ids) > 1 {
		status = "ambiguous"
	}
	return LotMatch{Status: status, SupplierIDs: ids}
}

// sameExpiryMonth : 유효기간을 연·월로 비교한다. 바코드(GS1)의 일 '00' 은 말일로 읽히고(2028-12-31)
// 사입 기록에는 2028-12-01·2028-12 처럼 적히기도 해, 날짜 전체로 비교하면 같은 제조번호인데도 맞지 않는다.
func sameExpiryMonth(a, b string) bool {
	if len(a) < 7 || len(b) < 7 {
		return a == b
	}
	return a[:7] == b[:7]
}
