package service

// 사입정보 사진 판독 (약가인하 회차 4단계) — 저장·수정·도매 맞추기.
//
// 실계수를 넣은 약품의 사입 이력(이팜 사입현황 화면 캡처·사진, 거래명세서)을 올리면 Windows 기본 OCR(무료·오프라인)로
// 사입일자·사입도매·수량·유효기간·제조번호를 읽는다 (lotocr.go). 결과는 바로 저장하지 않고 화면에서 사용자가
// 사진과 비교·수정한 뒤 저장한다 (lot_evidence, source = 'photo'). 저장한 뒤에도 고치거나 지울 수 있다.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"dpr/internal/domain"
	"dpr/internal/store"
)

// supplierMatcher : 사진의 도매 이름 → 등록된 거래처. 별칭(정규화 이름)이 같으면 그 거래처,
// 아니면 이름이 서로 포함되는 거래처가 하나뿐일 때만 고른다. 못 찾으면 0 (사용자가 고름).
// 판독 실수로 엉뚱한 거래처가 생기지 않도록 새 거래처는 만들지 않는다.
func (s *Service) supplierMatcher() func(raw string) (int64, string) {
	type sup struct {
		id   int64
		name string
	}
	var list []sup
	if rows, err := s.DB.Query("SELECT id, name FROM supplier WHERE deleted = 0"); err == nil {
		for rows.Next() {
			var v sup
			if rows.Scan(&v.id, &v.name) == nil {
				list = append(list, v)
			}
		}
		rows.Close()
	}
	nameOf := func(id int64) string {
		for _, v := range list {
			if v.id == id {
				return v.name
			}
		}
		return ""
	}
	return func(raw string) (int64, string) {
		alias := domain.NormalizeSupplierName(raw)
		if alias == "" {
			return 0, ""
		}
		var id int64
		if err := s.DB.QueryRow("SELECT a.supplier_id FROM supplier_alias a JOIN supplier s ON s.id = a.supplier_id WHERE a.alias = ? AND s.deleted = 0", alias).Scan(&id); err == nil {
			return id, nameOf(id)
		}
		var hit []sup
		for _, v := range list {
			n := domain.NormalizeSupplierName(v.name)
			if n != "" && (strings.Contains(alias, n) || strings.Contains(n, alias)) {
				hit = append(hit, v)
			}
		}
		if len(hit) == 1 {
			return hit[0].id, hit[0].name
		}
		// 글자 인식이 몇 글자 틀린 경우 ('가람으매' → 가람도매): 가장 비슷한 거래처가 충분히 비슷하고 2등과 차이가 날 때만
		clean := ocrNameKey(alias)
		var best, second float64
		var bestSup sup
		for _, v := range list {
			sc := textSimilarity(clean, ocrNameKey(domain.NormalizeSupplierName(v.name)))
			if sc > best {
				best, second, bestSup = sc, best, v
			} else if sc > second {
				second = sc
			}
		}
		if (best >= 0.6 && best-second >= 0.15) || (best >= 0.5 && second <= 0.25) {
			return bestSup.id, bestSup.name
		}
		return 0, ""
	}
}

// LotEvidenceView : 별도 근거(엑셀·직접 입력·사진) 한 줄 + 약품코드
type LotEvidenceView struct {
	LotView
	Code string `json:"code"`
}

// ListLotEvidence 는 여러 약품의 별도 제조번호 근거를 돌려준다 (사진 판독 화면).
func (s *Service) ListLotEvidence(codes []string) ([]LotEvidenceView, error) {
	out := []LotEvidenceView{}
	if len(codes) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(codes)), ",")
	args := make([]any, len(codes))
	for i, c := range codes {
		args[i] = domain.NormalizeProductCode(c)
	}
	rows, err := s.DB.Query(`
		SELECT l.id, l.product_code, l.source, l.supplier_id, IFNULL(s.name, ''), l.date, l.qty, l.lot, l.expiry
		  FROM lot_evidence l LEFT JOIN supplier s ON s.id = l.supplier_id
		 WHERE l.product_code IN (`+ph+`)
		 ORDER BY l.product_code, l.date DESC, l.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v LotEvidenceView
		var q int64
		if err := rows.Scan(&v.ID, &v.Code, &v.Source, &v.SupplierID, &v.SupplierName, &v.Date, &q, &v.Lot, &v.Expiry); err != nil {
			return nil, err
		}
		v.Qty = domain.Qty(q)
		out = append(out, v)
	}
	return out, rows.Err()
}

// UpdateLot 는 저장한 제조번호 근거 한 줄을 고친다 (사진 판독이 틀렸을 때).
func (s *Service) UpdateLot(id int64, in LotInput) error {
	code := domain.NormalizeProductCode(in.Code)
	if code == "" || in.SupplierID == 0 || strings.TrimSpace(in.Lot) == "" {
		return errors.New("약품·거래처·제조번호는 필수입니다")
	}
	if in.Expiry != "" {
		e, ok := domain.ParseFlexibleDate(in.Expiry)
		if !ok {
			return fmt.Errorf("유효기간 형식 오류: %s", in.Expiry)
		}
		in.Expiry = e
	}
	if in.Date != "" {
		d, ok := domain.ParseFlexibleDate(in.Date)
		if !ok {
			return fmt.Errorf("사입일자 형식 오류: %s", in.Date)
		}
		in.Date = d
	}
	var old string
	if err := s.DB.QueryRow("SELECT product_code FROM lot_evidence WHERE id = ?", id).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("이미 지워진 근거입니다")
		}
		return err
	}
	if _, err := s.DB.Exec("UPDATE lot_evidence SET product_code = ?, supplier_id = ?, date = ?, qty = ?, lot = ?, expiry = ? WHERE id = ?",
		code, in.SupplierID, in.Date, int64(domain.QtyFromFloat(in.Qty)), strings.TrimSpace(in.Lot), in.Expiry, id); err != nil {
		return err
	}
	s.rematchScans(code)
	if old != code {
		s.rematchScans(old)
	}
	return nil
}

// AddLots 는 사진 판독 결과(사용자가 고친 것)를 한꺼번에 저장한다. 같은 약품·도매·제조번호·유효기간이 이미 있으면 건너뛴다.
func (s *Service) AddLots(in []LotInput) (int, error) {
	for i, l := range in {
		if domain.NormalizeProductCode(l.Code) == "" || l.SupplierID == 0 || strings.TrimSpace(l.Lot) == "" {
			return 0, fmt.Errorf("%d번째 줄: 약품·도매·제조번호를 모두 채워 주세요", i+1)
		}
		if l.Expiry != "" {
			if _, ok := domain.ParseFlexibleDate(l.Expiry); !ok {
				return 0, fmt.Errorf("%d번째 줄: 유효기간 형식 오류 (%s)", i+1, l.Expiry)
			}
		}
		if l.Date != "" {
			if _, ok := domain.ParseFlexibleDate(l.Date); !ok {
				return 0, fmt.Errorf("%d번째 줄: 사입일자 형식 오류 (%s)", i+1, l.Date)
			}
		}
	}
	touched := map[string]bool{}
	added := 0
	err := s.DB.Tx(context.Background(), func(tx *sql.Tx) error {
		for _, l := range in {
			code := domain.NormalizeProductCode(l.Code)
			exp, date := "", ""
			if l.Expiry != "" {
				exp, _ = domain.ParseFlexibleDate(l.Expiry)
			}
			if l.Date != "" {
				date, _ = domain.ParseFlexibleDate(l.Date)
			}
			lot := strings.TrimSpace(l.Lot)
			var n int
			if err := tx.QueryRow("SELECT COUNT(*) FROM lot_evidence WHERE product_code = ? AND supplier_id = ? AND lot = ? AND expiry = ?", code, l.SupplierID, lot, exp).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				continue
			}
			src := l.Source
			if src == "" {
				src = "photo"
			}
			if _, err := tx.Exec("INSERT INTO lot_evidence(product_code, supplier_id, date, qty, lot, expiry, source, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
				code, l.SupplierID, date, int64(domain.QtyFromFloat(l.Qty)), lot, exp, src, store.Now()); err != nil {
				return err
			}
			touched[code] = true
			added++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for c := range touched {
		s.rematchScans(c)
	}
	return added, nil
}
