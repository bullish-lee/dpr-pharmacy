package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dpr/internal/domain"
	"dpr/internal/ingest"
	"dpr/internal/sheet"
	"dpr/internal/store"
)

func nilCtx() context.Context { return context.Background() }

const maxImportBytes = 200 << 20

// ImportKinds : 화면에 노출되는 가져오기 종류
var ImportKinds = []struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Help  string `json:"help"`
}{
	{"purchase", "사입현황", "이팜 사입현황 엑셀. 같은 기간을 다시 올리면 그 기간이 교체됩니다. 제조번호·유효기간 열이 있으면 함께 저장됩니다."},
	{"dispense", "조제현황", "이팜 조제현황 엑셀 (환자정보 삭제본). 같은 기간을 다시 올리면 교체됩니다."},
	{"initial_stock", "초기재고", "기준일 시점 재고. 여러 시트·양식이 섞여 있어도 시트별로 인식합니다. 기준일을 지정하세요."},
	{"stock_count", "실사 재고", "직접 센 재고(실계수). 추정재고보다 우선합니다."},
	{"lot", "사입 근거(제조번호)", "제조번호·유효기간이 포함된 사입 내역. 바코드 → 도매 매칭에 사용됩니다."},
	{"notice", "고시 목록표", "평소에는 복지부 게시판에서 자동으로 받습니다. 자동 수집이 안 될 때 「약제 급여 목록 및 급여 상한금액표」 목록표 엑셀을 직접 올리세요."},
	{"gtin", "표준코드 매핑", "평소에는 공공데이터포털에서 새 판을 자동으로 받습니다. 자동으로 받지 못할 때(보안문자 등) 내려받은 '약가마스터_의약품표준코드' CSV/엑셀을 올리세요."},
	{"drug_master", "약제급여목록표", "공공데이터포털 인증키를 넣으면 매달 자동으로 받습니다. 인증키가 없거나 받지 못할 때 심평원 「약제급여목록 및 급여상한금액표」 전체 목록 엑셀을 올리세요."},
}

type ImportRequest struct {
	Kind         string            `json:"kind"`
	FileName     string            `json:"fileName"`
	DataBase64   string            `json:"dataBase64"`
	Path         string            `json:"path"`
	Overrides    []ingest.Override `json:"overrides"`
	BaselineDate string            `json:"baselineDate"` // 초기재고 기준일
	Timing       string            `json:"timing"`       // start | end
	AsOf         string            `json:"asOf"`         // 실사일
	ProductCode  string            `json:"productCode"`  // 사입 근거에 약품코드 열이 없을 때
	SaveProfile  bool              `json:"saveProfile"`
	Force        bool              `json:"force"` // 기간 교체 안전장치를 무시하고 강제로 교체
}

type ImportPreview struct {
	Kind         string           `json:"kind"`
	FileName     string           `json:"fileName"`
	Schema       ingest.Schema    `json:"schema"`
	Sheets       []ingest.Preview `json:"sheets"`
	Usable       []string         `json:"usable"`
	NeedsMapping bool             `json:"needsMapping"`
	ProfileUsed  bool             `json:"profileUsed"`
}

type ImportResult struct {
	BatchID      int64             `json:"batchId"`
	Kind         string            `json:"kind"`
	FileName     string            `json:"fileName"`
	Rows         int               `json:"rows"`
	RangeFrom    string            `json:"rangeFrom"`
	RangeTo      string            `json:"rangeTo"`
	Replaced     int64             `json:"replaced"`
	Products     int               `json:"products"`
	Suppliers    int               `json:"suppliers"`
	Unresolved   int               `json:"unresolved"`
	IssueCount   int               `json:"issueCount"`
	Issues       []ingest.RowIssue `json:"issues"`
	Message      string            `json:"message"`
	NeedsMapping bool              `json:"needsMapping"`
	NeedsConfirm bool              `json:"needsConfirm"` // 기간 교체로 기존 기록이 많이 지워질 수 있어 확인 필요
	Preview      *ImportPreview    `json:"preview"`
	NoticeID     int64             `json:"noticeId"`
	DurationMs   int64             `json:"durationMs"`
}

func loadImportFile(req ImportRequest) (string, []byte, error) {
	if req.Path != "" {
		st, err := os.Stat(req.Path)
		if err != nil {
			return "", nil, err
		}
		if st.Size() > maxImportBytes {
			return "", nil, errors.New("파일이 너무 큽니다 (200MB 초과)")
		}
		data, err := os.ReadFile(req.Path)
		return filepath.Base(req.Path), data, err
	}
	b64 := req.DataBase64
	if i := strings.Index(b64, ","); i >= 0 && strings.HasPrefix(b64, "data:") {
		b64 = b64[i+1:]
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", nil, fmt.Errorf("파일 내용을 읽을 수 없습니다: %w", err)
	}
	if len(data) == 0 {
		return "", nil, errors.New("빈 파일입니다")
	}
	if len(data) > maxImportBytes {
		return "", nil, errors.New("파일이 너무 큽니다 (200MB 초과)")
	}
	return req.FileName, data, nil
}

func shaHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type profileHit struct {
	headerRow int
	mapping   ingest.Mapping
}

func (s *Service) loadProfiles(kind string) map[string]profileHit {
	out := map[string]profileHit{}
	rows, err := s.DB.Query("SELECT signature, header_row, mapping FROM import_profile WHERE kind = ?", kind)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sig, mp string
		var hr int
		if rows.Scan(&sig, &hr, &mp) == nil {
			var m ingest.Mapping
			if json.Unmarshal([]byte(mp), &m) == nil {
				out[sig] = profileHit{hr, m}
			}
		}
	}
	return out
}

func schemaKind(kind string) string {
	switch kind {
	case "initial_stock", "stock_count":
		return "stock"
	}
	return kind
}

// prepareTables 는 시트별로 헤더·매핑을 확정한다 (사용자 지정 > 저장된 양식 > 자동).
func (s *Service) prepareTables(kind string, sheets []sheet.Sheet, overrides []ingest.Override, fileName string) ([]*ingest.Table, *ImportPreview) {
	schema, _ := ingest.SchemaByKind(schemaKind(kind))
	profiles := s.loadProfiles(schemaKind(kind))
	pv := &ImportPreview{Kind: kind, FileName: fileName, Schema: schema}
	var tables []*ingest.Table
	for _, sh := range sheets {
		var ov *ingest.Override
		for i := range overrides {
			if overrides[i].Sheet == sh.Name {
				ov = &overrides[i]
			}
		}
		t := ingest.DetectTable(sh, schema, ov)
		if ov == nil {
			for i := 0; i < len(sh.Rows) && i < 20; i++ {
				if hit, ok := profiles[ingest.HeaderSignature(sh.Rows[i])]; ok {
					t = ingest.DetectTable(sh, schema, &ingest.Override{Sheet: sh.Name, HeaderRow: i, Mapping: hit.mapping})
					pv.ProfileUsed = true
					break
				}
			}
		}
		usable := t.HeaderRow >= 0 && len(t.Missing) == 0
		if schema.Kind == "stock" && !(t.Has("code") || t.Has("name")) {
			usable = false
			if t.HeaderRow >= 0 {
				t.Missing = append(t.Missing, "code")
			}
		}
		if usable && len(t.Rows) == 0 {
			usable = false
		}
		pv.Sheets = append(pv.Sheets, ingest.MakePreview(sh, t))
		if usable {
			tables = append(tables, t)
			pv.Usable = append(pv.Usable, sh.Name)
		}
	}
	pv.NeedsMapping = len(tables) == 0
	return tables, pv
}

// PreviewImport 는 파일을 읽어 시트·자동 매핑 결과만 돌려준다 (DB 변경 없음).
func (s *Service) PreviewImport(req ImportRequest) (*ImportPreview, error) {
	name, data, err := loadImportFile(req)
	if err != nil {
		return nil, err
	}
	if req.Kind == "notice" {
		return &ImportPreview{Kind: "notice", FileName: name}, nil
	}
	sheets, err := sheet.ReadBytes(name, data)
	if err != nil {
		return nil, err
	}
	_, pv := s.prepareTables(req.Kind, sheets, req.Overrides, name)
	return pv, nil
}

// Import 는 파일을 가져와 DB 에 적재한다.
func (s *Service) Import(req ImportRequest) (*ImportResult, error) {
	start := time.Now()
	s.importMu.Lock()
	defer s.importMu.Unlock()
	name, data, err := loadImportFile(req)
	if err != nil {
		return nil, err
	}
	sha := shaHex(data)
	if req.Kind == "notice" {
		id, msg, _, err := s.ingestNoticeFile("upload", "", "", "", "", name, data)
		if err != nil {
			return nil, err
		}
		return &ImportResult{Kind: "notice", FileName: name, NoticeID: id, Message: msg, DurationMs: time.Since(start).Milliseconds()}, nil
	}
	tRead := time.Now()
	sheets, err := sheet.ReadBytes(name, data)
	if err != nil {
		s.Log.Warn("가져오기 파일 읽기 실패", "kind", req.Kind, "file", name, "err", err)
		return nil, err
	}
	s.Log.Debug("엑셀 읽음", "kind", req.Kind, "file", name, "sheets", len(sheets), "ms", time.Since(tRead).Milliseconds())
	tables, pv := s.prepareTables(req.Kind, sheets, req.Overrides, name)
	if len(tables) == 0 {
		s.Log.Info("가져오기: 열 매핑 필요", "kind", req.Kind, "file", name)
		return &ImportResult{Kind: req.Kind, FileName: name, NeedsMapping: true, Preview: pv,
			Message: "필수 열을 자동으로 찾지 못했습니다. 열 매핑을 지정해 주세요."}, nil
	}
	if req.SaveProfile {
		s.saveProfiles(schemaKind(req.Kind), sheets, req.Overrides)
	}
	var res *ImportResult
	switch req.Kind {
	case "purchase":
		res, err = s.importPurchases(name, sha, tables, req.Force)
	case "dispense":
		res, err = s.importDispenses(name, sha, tables, req.Force)
	case "initial_stock":
		res, err = s.importInitialStock(name, sha, tables, req)
	case "stock_count":
		res, err = s.importStockCount(name, sha, tables, req)
	case "lot":
		res, err = s.importLots(name, sha, tables, req)
	case "gtin":
		res, err = s.importGTINs(name, sha, tables)
	case "drug_master":
		res, err = s.importDrugMaster(name, sha, tables)
	default:
		return nil, fmt.Errorf("알 수 없는 가져오기 종류: %s", req.Kind)
	}
	if err != nil {
		s.Log.Warn("가져오기 실패", "kind", req.Kind, "file", name, "err", err)
		return nil, err
	}
	res.Kind, res.FileName = req.Kind, name
	switch req.Kind {
	case "purchase", "dispense", "initial_stock", "stock_count", "lot", "gtin": // 옛 제품코드로 된 자료 → 바뀐 새 코드로
		if !res.NeedsConfirm && !res.NeedsMapping {
			s.applyStoredCodeChanges()
		}
	}
	if !res.NeedsConfirm {
		res.Preview = pv
		switch req.Kind {
		case "purchase", "dispense", "initial_stock", "drug_master": // 재고·단위에 영향을 주는 자료는 점검 결과를 덧붙인다
			res.Message += s.healthNote()
		}
	}
	res.IssueCount = len(res.Issues)
	if len(res.Issues) > 300 {
		res.Issues = res.Issues[:300]
	}
	res.DurationMs = time.Since(start).Milliseconds()
	s.Log.Info("가져오기 완료", "kind", req.Kind, "file", name, "rows", res.Rows, "ms", res.DurationMs)
	return res, nil
}

func (s *Service) saveProfiles(kind string, sheets []sheet.Sheet, overrides []ingest.Override) {
	for _, ov := range overrides {
		for _, sh := range sheets {
			if sh.Name != ov.Sheet || ov.HeaderRow < 0 || ov.HeaderRow >= len(sh.Rows) {
				continue
			}
			sig := ingest.HeaderSignature(sh.Rows[ov.HeaderRow])
			_, err := s.DB.Exec(`INSERT INTO import_profile(kind, signature, name, header_row, mapping, created_at) VALUES(?, ?, ?, ?, ?, ?)
				ON CONFLICT(kind, signature) DO UPDATE SET header_row = excluded.header_row, mapping = excluded.mapping`,
				kind, sig, sh.Name, ov.HeaderRow, toJSON(ov.Mapping), store.Now())
			if err != nil {
				s.Log.Warn("양식 저장 실패", "err", err)
			}
		}
	}
}

func insertBatch(tx *sql.Tx, kind, name, sha, from, to string, rows int, summary any) (int64, error) {
	res, err := tx.Exec("INSERT INTO import_batch(kind, filename, sha256, range_from, range_to, row_count, summary, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)",
		kind, name, sha, from, to, rows, toJSON(summary), store.Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type supplierResolver struct {
	tx    *sql.Tx
	cache map[string]int64
	added int
}

func (r *supplierResolver) resolve(raw string) (sql.NullInt64, error) {
	alias := domain.NormalizeSupplierName(raw)
	if alias == "" {
		return sql.NullInt64{}, nil
	}
	if id, ok := r.cache[alias]; ok {
		return sql.NullInt64{Int64: id, Valid: true}, nil
	}
	var id int64
	err := r.tx.QueryRow("SELECT supplier_id FROM supplier_alias WHERE alias = ?", alias).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		now := store.Now()
		if _, err := r.tx.Exec("INSERT INTO supplier(name, kind, created_at, updated_at) VALUES(?, ?, ?, ?) ON CONFLICT(name) DO NOTHING",
			alias, domain.GuessSupplierKind(alias), now, now); err != nil {
			return sql.NullInt64{}, err
		}
		if err := r.tx.QueryRow("SELECT id FROM supplier WHERE name = ?", alias).Scan(&id); err != nil {
			return sql.NullInt64{}, err
		}
		if _, err := r.tx.Exec("INSERT OR IGNORE INTO supplier_alias(alias, supplier_id) VALUES(?, ?)", alias, id); err != nil {
			return sql.NullInt64{}, err
		}
		r.added++
	} else if err != nil {
		return sql.NullInt64{}, err
	}
	r.cache[alias] = id
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

const upsertProductSQL = `INSERT INTO product(code, name, stock_unit, updated_at) VALUES(?, ?, ?, ?)
	ON CONFLICT(code) DO UPDATE SET
	  name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE product.name END,
	  stock_unit = CASE WHEN excluded.stock_unit <> '' THEN excluded.stock_unit ELSE product.stock_unit END,
	  updated_at = excluded.updated_at`

func dateRange(tables []*ingest.Table, dates []string) (string, string) {
	from, to := "", ""
	same := true
	for _, t := range tables {
		if t.RangeFrom == "" {
			same = false
			break
		}
		if from == "" {
			from, to = t.RangeFrom, t.RangeTo
		} else if t.RangeFrom != from || t.RangeTo != to {
			same = false
		}
	}
	if !same {
		from, to = "", ""
	}
	for _, d := range dates {
		if from == "" || d < from {
			from = d
		}
		if to == "" || d > to {
			to = d
		}
	}
	return from, to
}

// errRangeGuard 는 기간 교체 안전장치에 걸렸을 때 (가져오기는 하지 않음)
type errRangeGuard struct{ msg string }

func (e errRangeGuard) Error() string { return e.msg }

// replaceRanges : 새 파일로 교체할 기간들 — 시트마다 (제목의 기간 ∪ 그 시트 자료의 첫날~끝날), 겹치거나 이어지는 기간은 합친다.
// 1월·3월 시트만 든 파일이 사이의 2월 기록까지 지우지 않게 시트별로 나눈다.
// 읽은 행이 하나도 없는 시트는 제목에 기간이 있어도 그 기간을 지우지 않는다 (skipped 로 알린다 — 행을 모두 못 읽은 시트로 한 달 기록이 지워지지 않게).
func replaceRanges(tables []*ingest.Table, dates [][]string) (out [][2]string, skipped []string) {
	var rs [][2]string
	for i, t := range tables {
		if i >= len(dates) || len(dates[i]) == 0 {
			if t.RangeFrom != "" {
				skipped = append(skipped, fmt.Sprintf("시트 '%s'에서 읽은 행이 없어 그 기간(%s ~ %s)의 기존 기록은 그대로 둡니다", t.Sheet, t.RangeFrom, t.RangeTo))
			}
			continue
		}
		from, to := t.RangeFrom, t.RangeTo
		if i < len(dates) {
			for _, d := range dates[i] {
				if from == "" || d < from {
					from = d
				}
				if to == "" || d > to {
					to = d
				}
			}
		}
		if from != "" && to != "" {
			rs = append(rs, [2]string{from, to})
		}
	}
	sort.Slice(rs, func(a, b int) bool { return rs[a][0] < rs[b][0] })
	for _, r := range rs {
		if n := len(out); n > 0 && r[0] <= nextDay(out[n-1][1]) {
			if r[1] > out[n-1][1] {
				out[n-1][1] = r[1]
			}
			continue
		}
		out = append(out, r)
	}
	return out, skipped
}

// applyRanges : 교체 기간을 결과에 반영 (기록·메시지가 실제로 지운 기간과 같게) + 건너뛴 시트 알림
func applyRanges(res *ImportResult, ranges [][2]string, skipped []string) {
	if len(ranges) > 0 {
		res.RangeFrom, res.RangeTo = ranges[0][0], ranges[len(ranges)-1][1]
	}
	for _, m := range skipped {
		res.Issues = append(res.Issues, ingest.RowIssue{Message: m})
	}
}

func nextDay(d string) string {
	t, err := domain.ParseDate(d)
	if err != nil {
		return d
	}
	return domain.FormatDate(t.AddDate(0, 0, 1))
}

// rangeWhere : 기간들의 WHERE 조건
func rangeWhere(ranges [][2]string) (string, []any) {
	if len(ranges) == 0 {
		return "0=1", nil
	}
	parts := make([]string, len(ranges))
	args := make([]any, 0, len(ranges)*2)
	for i, r := range ranges {
		parts[i] = "date BETWEEN ? AND ?"
		args = append(args, r[0], r[1])
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func rangesLabel(ranges [][2]string) string {
	var parts []string
	for _, r := range ranges {
		parts = append(parts, r[0]+" ~ "+r[1])
	}
	return strings.Join(parts, ", ")
}

// rangeGuard : 같은 기간의 기존 기록 대비 새 파일의 약품 수가 20% 미만이면 중단한다.
// (약품 1개짜리 '약품별 사입 내역'을 사입현황으로 잘못 올려 다른 약품 기록이 지워지는 사고 방지)
func rangeGuard(tx *sql.Tx, table string, ranges [][2]string, newProducts int, force bool) error {
	if force {
		return nil
	}
	w, args := rangeWhere(ranges)
	var oldProducts, oldRows int
	if err := tx.QueryRow("SELECT COUNT(DISTINCT product_code), COUNT(*) FROM "+table+" WHERE "+w, args...).Scan(&oldProducts, &oldRows); err != nil {
		return err
	}
	if oldRows > 0 && oldProducts >= 10 && newProducts*5 < oldProducts {
		return errRangeGuard{fmt.Sprintf("이 파일에는 약품 %d개만 있는데, 같은 기간(%s)의 기존 기록에는 약품 %d개(%s행)가 있습니다. 그대로 가져오면 그 기간의 기존 기록이 모두 교체(삭제)됩니다. "+
			"약품별 사입 내역(제조번호 포함)이라면 '사입 근거(제조번호)'로 올려주세요. 전체 자료가 맞다면 '그래도 교체'를 선택하세요.",
			newProducts, rangesLabel(ranges), oldProducts, comma(oldRows))}
	}
	return nil
}

// supplierGuard : 같은 기간 기존 기록에 있던 도매처가 새 파일에 통째로 없으면 중단한다.
// (도매처를 골라 내보낸 사입현황을 올려 다른 도매처 기록이 지워지는 사고 방지 — 약품 수만으로는 못 잡는다)
func supplierGuard(tx *sql.Tx, ranges [][2]string, recs []ingest.PurchaseRec, force bool) error {
	if force {
		return nil
	}
	w, args := rangeWhere(ranges)
	rows, err := tx.Query(`SELECT IFNULL(p.supplier_id, 0), IFNULL(s.name, '(거래처 없음)'), COUNT(*) FROM purchase_line p
		LEFT JOIN supplier s ON s.id = p.supplier_id WHERE `+strings.ReplaceAll(w, "date", "p.date")+` GROUP BY IFNULL(p.supplier_id, 0)`, args...)
	if err != nil {
		return err
	}
	type sup struct {
		id   int64
		name string
		n    int
	}
	var old []sup
	total := 0
	for rows.Next() {
		var v sup
		if err := rows.Scan(&v.id, &v.name, &v.n); err != nil {
			rows.Close()
			return err
		}
		old = append(old, v)
		total += v.n
	}
	rows.Close()
	if len(old) < 2 {
		return nil
	}
	present := map[int64]bool{}
	seen := map[string]bool{}
	for _, r := range recs {
		alias := domain.NormalizeSupplierName(r.Supplier)
		if seen[alias] {
			continue
		}
		seen[alias] = true
		if alias == "" {
			present[0] = true
			continue
		}
		var id int64
		if tx.QueryRow("SELECT supplier_id FROM supplier_alias WHERE alias = ?", alias).Scan(&id) == nil {
			present[id] = true
		}
	}
	var missing []string
	for _, v := range old {
		if !present[v.id] && (v.n >= 20 || v.n*10 >= total) {
			missing = append(missing, fmt.Sprintf("%s(%s행)", v.name, comma(v.n)))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if len(missing) > 3 {
		missing = append(missing[:3], fmt.Sprintf("외 %d곳", len(missing)-3))
	}
	return errRangeGuard{fmt.Sprintf("같은 기간(%s)의 기존 기록에 있는 도매처 %s 이(가) 이 파일에는 없습니다. 그대로 가져오면 그 도매처의 기록이 지워집니다. "+
		"도매처를 골라 내보낸 파일이라면 전체 도매처로 다시 내보내 주세요. 맞다면 '그래도 교체'를 선택하세요.", rangesLabel(ranges), strings.Join(missing, ", "))}
}

// codesInRange : 기간 안에 기록이 있는 품목 (교체로 지워질 행의 품목)
func codesInRange(tx *sql.Tx, table string, ranges [][2]string) ([]string, error) {
	w, args := rangeWhere(ranges)
	rows, err := tx.Query("SELECT DISTINCT product_code FROM "+table+" WHERE "+w, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func distinctCodes[T any](recs []T, code func(T) string) int {
	m := map[string]bool{}
	for _, r := range recs {
		m[code(r)] = true
	}
	return len(m)
}

func (s *Service) importPurchases(name, sha string, tables []*ingest.Table, force bool) (*ImportResult, error) {
	var recs []ingest.PurchaseRec
	res := &ImportResult{}
	tableDates := make([][]string, len(tables))
	for i, t := range tables {
		r, iss := ingest.ParsePurchases(t)
		recs = append(recs, r...)
		res.Issues = append(res.Issues, iss...)
		for _, x := range r {
			tableDates[i] = append(tableDates[i], x.Date)
		}
	}
	if len(recs) == 0 {
		return nil, errors.New("가져올 사입 행이 없습니다")
	}
	dates := make([]string, len(recs))
	for i, r := range recs {
		dates[i] = r.Date
	}
	res.RangeFrom, res.RangeTo = dateRange(tables, dates)
	ranges, skipped := replaceRanges(tables, tableDates)
	applyRanges(res, ranges, skipped)
	products := map[string][2]string{}
	err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if err := rangeGuard(tx, "purchase_line", ranges, distinctCodes(recs, func(r ingest.PurchaseRec) string { return r.Code }), force); err != nil {
			return err
		}
		if err := supplierGuard(tx, ranges, recs, force); err != nil {
			return err
		}
		batchID, err := insertBatch(tx, "purchase", name, sha, res.RangeFrom, res.RangeTo, len(recs), map[string]any{"issues": len(res.Issues)})
		if err != nil {
			return err
		}
		res.BatchID = batchID
		replacedCodes, err := codesInRange(tx, "purchase_line", ranges)
		if err != nil {
			return err
		}
		rw, rargs := rangeWhere(ranges)
		del, err := tx.Exec("DELETE FROM purchase_line WHERE "+rw, rargs...)
		if err != nil {
			return err
		}
		res.Replaced, _ = del.RowsAffected()
		restore, err := dropIndexesForBulk(tx, "purchase_line", len(recs))
		if err != nil {
			return err
		}
		bi := newBulkInsert(tx, `INSERT INTO purchase_line(batch_id, date, product_code, supplier_id, kind, pack_size, pack_qty, qty, unit_price, amount, lot, expiry,
			raw_qty, raw_unit_price, unit_label, factor) VALUES `, 16)
		sr := &supplierResolver{tx: tx, cache: map[string]int64{}}
		for _, r := range recs {
			sid, err := sr.resolve(r.Supplier)
			if err != nil {
				return err
			}
			if err := bi.add(batchID, r.Date, r.Code, sid, r.Kind, r.PackSize, r.PackQty, int64(r.Qty), r.UnitPrice, r.Amount, r.Lot, r.Expiry,
				int64(r.Qty), r.UnitPrice, r.Unit, 1); err != nil {
				return err
			}
			products[r.Code] = [2]string{r.Name, r.Unit}
		}
		if err := bi.close(); err != nil {
			return err
		}
		if err := restore(); err != nil {
			return err
		}
		res.Suppliers = sr.added
		if err := upsertProducts(tx, products); err != nil {
			return err
		}
		if err := updatePackSizes(tx); err != nil {
			return err
		}
		recompute := map[string]bool{}
		for c := range products {
			recompute[c] = true
		}
		for _, c := range replacedCodes { // 이 기간 기록이 새 파일에서 빠진 약도 남은 기록으로 다시 판단
			recompute[c] = true
		}
		return recomputeUnits(tx, sortedKeys(recompute)) // 사입 원본 → 고시 단위
	})
	if g, ok := err.(errRangeGuard); ok {
		return &ImportResult{NeedsConfirm: true, Message: g.msg, RangeFrom: res.RangeFrom, RangeTo: res.RangeTo}, nil
	}
	if err != nil {
		return nil, err
	}
	res.Rows, res.Products = len(recs), len(products)
	res.Message = fmt.Sprintf("사입 %s행을 가져왔습니다 (%s, 기존 %d행 교체)", comma(len(recs)), rangesLabel(ranges), res.Replaced)
	s.rematchUnresolved()
	return res, nil
}

func upsertProducts(tx *sql.Tx, products map[string][2]string) error {
	stmt, err := tx.Prepare(upsertProductSQL)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := store.Now()
	for code, nu := range products {
		if _, err := stmt.Exec(code, nu[0], nu[1], now); err != nil {
			return err
		}
	}
	return nil
}

// updatePackSizes : 품목별 가장 흔한 포장 단위수량 (바코드 1개 = 몇 낱개 기본값)
func updatePackSizes(tx *sql.Tx) error {
	_, err := tx.Exec(`UPDATE product SET pack_size = (
		SELECT pack_size FROM purchase_line p WHERE p.product_code = product.code AND p.kind = 'purchase' AND p.pack_size > 0
		GROUP BY pack_size ORDER BY COUNT(*) DESC, pack_size DESC LIMIT 1)
		WHERE code IN (SELECT DISTINCT product_code FROM purchase_line)`)
	return err
}

func (s *Service) importDispenses(name, sha string, tables []*ingest.Table, force bool) (*ImportResult, error) {
	var recs []ingest.DispenseRec
	res := &ImportResult{}
	tableDates := make([][]string, len(tables))
	for i, t := range tables {
		r, iss := ingest.ParseDispenses(t)
		recs = append(recs, r...)
		res.Issues = append(res.Issues, iss...)
		for _, x := range r {
			tableDates[i] = append(tableDates[i], x.Date)
		}
	}
	if len(recs) == 0 {
		return nil, errors.New("가져올 조제 행이 없습니다")
	}
	dates := make([]string, len(recs))
	for i, r := range recs {
		dates[i] = r.Date
	}
	res.RangeFrom, res.RangeTo = dateRange(tables, dates)
	ranges, skipped := replaceRanges(tables, tableDates)
	applyRanges(res, ranges, skipped)
	products := map[string][2]string{}
	err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if err := rangeGuard(tx, "dispense_line", ranges, distinctCodes(recs, func(r ingest.DispenseRec) string { return r.Code }), force); err != nil {
			return err
		}
		batchID, err := insertBatch(tx, "dispense", name, sha, res.RangeFrom, res.RangeTo, len(recs), map[string]any{"issues": len(res.Issues)})
		if err != nil {
			return err
		}
		res.BatchID = batchID
		rw, rargs := rangeWhere(ranges)
		del, err := tx.Exec("DELETE FROM dispense_line WHERE "+rw, rargs...)
		if err != nil {
			return err
		}
		res.Replaced, _ = del.RowsAffected()
		restore, err := dropIndexesForBulk(tx, "dispense_line", len(recs))
		if err != nil {
			return err
		}
		bi := newBulkInsert(tx, "INSERT INTO dispense_line(batch_id, date, product_code, qty) VALUES ", 4)
		type dunit struct {
			unit string
			size float64
		}
		dispUnits := map[string]map[dunit]int{} // 품목별 조제 단위·단위수량 빈도
		for _, r := range recs {
			if err := bi.add(batchID, r.Date, r.Code, int64(r.Qty)); err != nil {
				return err
			}
			if _, ok := products[r.Code]; !ok {
				products[r.Code] = [2]string{r.Name, ""}
			}
			if r.UnitSize > 0 {
				m := dispUnits[r.Code]
				if m == nil {
					m = map[dunit]int{}
					dispUnits[r.Code] = m
				}
				m[dunit{r.Unit, r.UnitSize}]++
			}
		}
		if err := bi.close(); err != nil {
			return err
		}
		if err := restore(); err != nil {
			return err
		}
		if err := upsertProducts(tx, products); err != nil {
			return err
		}
		ust, err := tx.Prepare("UPDATE product SET disp_unit = ?, disp_size = ? WHERE code = ?")
		if err != nil {
			return err
		}
		defer ust.Close()
		for code, m := range dispUnits { // 가장 흔한 조제 단위를 기록 → 단위 확인(recomputeUnits)에서 고시 규격과 대조
			best, bestN := dunit{}, -1
			for u, n := range m {
				if n > bestN || (n == bestN && (u.size > best.size || (u.size == best.size && u.unit < best.unit))) {
					best, bestN = u, n
				}
			}
			if _, err := ust.Exec(best.unit, best.size, code); err != nil {
				return err
			}
		}
		// 파일의 모든 품목: 새로 생긴 품목의 고시 단위 표시 + 조제 단위수량 대조 (전체가 아니라 이 파일 품목만)
		return recomputeUnits(tx, sortedKeys(products))
	})
	if g, ok := err.(errRangeGuard); ok {
		return &ImportResult{NeedsConfirm: true, Message: g.msg, RangeFrom: res.RangeFrom, RangeTo: res.RangeTo}, nil
	}
	if err != nil {
		return nil, err
	}
	res.Rows, res.Products = len(recs), len(products)
	res.Message = fmt.Sprintf("조제 %s행을 가져왔습니다 (%s, 기존 %d행 교체)", comma(len(recs)), rangesLabel(ranges), res.Replaced)
	s.rematchUnresolved()
	return res, nil
}

// DefaultBaselineDate : 사입·조제 데이터의 가장 이른 날짜 (없으면 오늘)
func (s *Service) DefaultBaselineDate() string {
	var d sql.NullString
	// 엑셀 제목의 조회 시작일(예: '사입현황 20260202 ~') 이 실제 첫 거래일보다 정확한 기준
	_ = s.DB.QueryRow(`SELECT MIN(range_from) FROM import_batch WHERE kind IN ('purchase','dispense') AND IFNULL(range_from,'') <> ''`).Scan(&d)
	if d.Valid && d.String != "" {
		return d.String
	}
	_ = s.DB.QueryRow(`SELECT MIN(d) FROM (SELECT MIN(date) AS d FROM purchase_line UNION ALL SELECT MIN(date) FROM dispense_line)`).Scan(&d)
	if d.Valid && d.String != "" {
		return d.String
	}
	return s.Today()
}

func (s *Service) importInitialStock(name, sha string, tables []*ingest.Table, req ImportRequest) (*ImportResult, error) {
	baseline := strings.TrimSpace(req.BaselineDate)
	if baseline == "" {
		baseline = s.DefaultBaselineDate()
	}
	if _, err := domain.ParseDate(baseline); err != nil {
		return nil, fmt.Errorf("기준일 형식 오류: %s", baseline)
	}
	timing := req.Timing
	if timing != "end" {
		timing = "start"
	}
	res := &ImportResult{RangeFrom: baseline, RangeTo: baseline}
	ix := s.buildNameIndex()
	type matched struct {
		rec  ingest.StockRec
		code string
		by   string // code | name
	}
	type unresolvedRow struct {
		rec   ingest.StockRec
		cands []Candidate
	}
	var lines []matched
	var unresolved []unresolvedRow
	rows, ignored := 0, 0
	codes := map[string]string{}
	masterNames := map[string]string{}
	if mr, err := s.DB.Query("SELECT code, name FROM drug_master"); err == nil {
		for mr.Next() {
			var c, n string
			if mr.Scan(&c, &n) == nil {
				masterNames[c] = n
			}
		}
		mr.Close()
	}
	for _, t := range tables {
		recs, iss := ingest.ParseStock(t)
		res.Issues = append(res.Issues, iss...)
		for _, r := range recs {
			rows++
			code := r.Code
			if code != "" && r.Name != "" { // 코드와 이름이 급여목록과 다르면 알린다 (코드 오기 의심)
				if mn, ok := masterNames[code]; ok && !namesAgree(r.Name, mn) {
					res.Issues = append(res.Issues, ingest.RowIssue{Sheet: r.Sheet, Row: r.Row,
						Message: fmt.Sprintf("약품코드 %s 는 급여목록에서 '%s'인데 시트 이름은 '%s' — 코드를 확인하세요", code, mn, r.Name)})
				}
			}
			if code == "" {
				if r.Qty == 0 { // 수량 0 행(구분 제목·빈 칸)은 재고에 영향이 없다
					ignored++
					continue
				}
				var cands []Candidate
				code, cands = ix.match(r.Name, r.Spec != "", r.UnitPrice)
				if code == "" {
					unresolved = append(unresolved, unresolvedRow{r, cands})
					continue
				}
			}
			by := "code"
			if r.Code == "" {
				by = "name"
			}
			lines = append(lines, matched{r, code, by})
			if _, ok := codes[code]; !ok {
				codes[code] = r.Name
			}
		}
	}
	err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		batchID, err := insertBatch(tx, "initial_stock", name, sha, baseline, baseline, rows, map[string]any{"products": len(codes), "unresolved": len(unresolved), "ignored": ignored})
		if err != nil {
			return err
		}
		res.BatchID = batchID
		for _, q := range []string{"DELETE FROM stock_anchor WHERE source = 'initial'", "DELETE FROM initial_line", "DELETE FROM initial_unresolved"} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		for _, kv := range [][2]string{{SetBaselineDate, baseline}, {SetBaselineTiming, timing}} {
			if _, err := tx.Exec("INSERT INTO app_setting(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", kv[0], kv[1]); err != nil {
				return err
			}
		}
		stmt, err := tx.Prepare(`INSERT INTO initial_line(batch_id, sheet, row, product_code, name, raw_qty, raw_unit_price, unit_label, factor, qty, matched_by)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, l := range lines {
			var price any
			if l.rec.UnitPrice > 0 {
				price = l.rec.UnitPrice
			}
			if _, err := stmt.Exec(batchID, l.rec.Sheet, l.rec.Row, l.code, l.rec.Name, int64(l.rec.Qty), price, l.rec.Unit, int64(l.rec.Qty), l.by); err != nil {
				return err
			}
		}
		products := map[string][2]string{}
		for code, nm := range codes {
			if !ix.has(code) {
				if n := ix.nameOf(code); n != "" { // 급여목록 이름이 있으면 그 이름으로
					nm = n
				}
				products[code] = [2]string{nm, ""}
			}
		}
		if err := upsertProducts(tx, products); err != nil {
			return err
		}
		ustmt, err := tx.Prepare("INSERT INTO initial_unresolved(batch_id, sheet, row, name, qty, unit_price, spec, candidates, unit_label) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer ustmt.Close()
		for _, u := range unresolved {
			if _, err := ustmt.Exec(batchID, u.rec.Sheet, u.rec.Row, u.rec.Name, int64(u.rec.Qty), u.rec.UnitPrice, u.rec.Spec, toJSON(u.cands), u.rec.Unit); err != nil {
				return err
			}
		}
		return recomputeUnits(tx, sortedKeys(codes)) // 원본 → 고시 단위, 기준점 생성
	})
	if err != nil {
		return nil, err
	}
	// 같은 파일에서 확정한 약(약국 품목·단가)이 사전에 들어가므로 한 번 더 맞춰 본다 —
	// 초기재고를 마지막에 올려도 나중에 다른 자료를 올린 것과 같은 결과가 되게 (가져오기 잠금 안이라 바로 실행)
	if len(unresolved) > 0 {
		s.rematchNow()
		left := 0
		if err := s.DB.QueryRow("SELECT COUNT(*) FROM initial_unresolved WHERE resolved_code = '' AND ignored = 0").Scan(&left); err == nil {
			if left < len(unresolved) {
				s.Log.Info("초기재고 두 번째 매칭", "before", len(unresolved), "after", left)
			}
			unresolved = unresolved[:left]
		}
	}
	res.Rows, res.Products, res.Unresolved = rows, len(codes), len(unresolved)
	res.Message = fmt.Sprintf("초기재고 %s행 → %s개 품목 (기준일 %s).", comma(rows), comma(len(codes)), baseline)
	if ignored > 0 {
		res.Message += fmt.Sprintf(" 수량 0인 이름 행 %d개는 건너뛰었습니다.", ignored)
	}
	if len(unresolved) > 0 {
		res.Message += fmt.Sprintf(" 약품을 찾지 못한 %d행은 '재고 → 초기재고 확인 필요'에서 지정하세요.", len(unresolved))
	}
	return res, nil
}

func (s *Service) importStockCount(name, sha string, tables []*ingest.Table, req ImportRequest) (*ImportResult, error) {
	asOf := strings.TrimSpace(req.AsOf)
	if asOf == "" {
		asOf = s.Today()
	}
	if _, err := domain.ParseDate(asOf); err != nil {
		return nil, fmt.Errorf("실사일 형식 오류: %s", asOf)
	}
	timing := req.Timing
	if timing != "start" {
		timing = "end"
	}
	res := &ImportResult{RangeFrom: asOf, RangeTo: asOf}
	ix := s.buildNameIndex()
	type cline struct {
		rec  ingest.StockRec
		code string
	}
	var lines []cline
	names := map[string]string{}
	rows := 0
	for _, t := range tables {
		recs, iss := ingest.ParseStock(t)
		res.Issues = append(res.Issues, iss...)
		for _, r := range recs {
			rows++
			code := r.Code
			if code == "" {
				code, _ = ix.match(r.Name, r.Spec != "", r.UnitPrice)
			}
			if code == "" {
				res.Unresolved++
				res.Issues = append(res.Issues, ingest.RowIssue{Sheet: r.Sheet, Row: r.Row, Message: "약품을 찾지 못함: " + r.Name})
				continue
			}
			lines = append(lines, cline{r, code})
			if _, ok := names[code]; !ok {
				names[code] = r.Name
			}
		}
	}
	codes := sortedKeys(names)
	err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		batchID, err := insertBatch(tx, "stock_count", name, sha, asOf, asOf, rows, map[string]any{"products": len(codes)})
		if err != nil {
			return err
		}
		res.BatchID = batchID
		now := store.Now()
		// 품목마다 실사 기준점 하나 + 원본 행 (원본 단위 → 고시 단위 환산은 초기재고와 같은 규칙, recomputeUnits)
		anchors := map[string]int64{}
		for _, code := range codes {
			if _, err := tx.Exec("DELETE FROM stock_anchor WHERE source = 'count' AND product_code = ? AND as_of = ? AND timing = ?", code, asOf, timing); err != nil {
				return err
			}
			r, err := tx.Exec("INSERT INTO stock_anchor(product_code, as_of, timing, qty, source, batch_id, note, created_at) VALUES(?, ?, ?, 0, 'count', ?, '실사 파일', ?)",
				code, asOf, timing, batchID, now)
			if err != nil {
				return err
			}
			anchors[code], _ = r.LastInsertId()
		}
		stmt, err := tx.Prepare(`INSERT INTO count_line(anchor_id, batch_id, sheet, row, product_code, name, raw_qty, raw_unit_price, unit_label, factor, qty)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, l := range lines {
			var price any
			if l.rec.UnitPrice > 0 {
				price = l.rec.UnitPrice
			}
			if _, err := stmt.Exec(anchors[l.code], batchID, l.rec.Sheet, l.rec.Row, l.code, l.rec.Name, int64(l.rec.Qty), price, l.rec.Unit, int64(l.rec.Qty)); err != nil {
				return err
			}
		}
		products := map[string][2]string{} // 사입·조제 기록이 없는 약도 재고 화면에 보이게
		for code, nm := range names {
			if !ix.has(code) {
				if n := ix.nameOf(code); n != "" {
					nm = n
				}
				products[code] = [2]string{nm, ""}
			}
		}
		if err := upsertProducts(tx, products); err != nil {
			return err
		}
		return recomputeUnits(tx, codes)
	})
	if err != nil {
		return nil, err
	}
	res.Rows, res.Products = rows, len(codes)
	res.Message = fmt.Sprintf("실사 재고 %d개 품목을 반영했습니다 (실사일 %s)", len(codes), asOf)
	s.Log.Info("실사 파일 반영", "file", name, "products", len(codes), "unresolved", res.Unresolved, "as_of", asOf)
	return res, nil
}

func (s *Service) importLots(name, sha string, tables []*ingest.Table, req ImportRequest) (*ImportResult, error) {
	ix := s.buildNameIndex()
	res := &ImportResult{}
	var recs []ingest.LotRec
	for _, t := range tables {
		r, iss := ingest.ParseLots(t)
		recs = append(recs, r...)
		res.Issues = append(res.Issues, iss...)
	}
	forced := domain.NormalizeProductCode(req.ProductCode)
	err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		batchID, err := insertBatch(tx, "lot", name, sha, "", "", len(recs), nil)
		if err != nil {
			return err
		}
		res.BatchID = batchID
		sr := &supplierResolver{tx: tx, cache: map[string]int64{}}
		now := store.Now()
		for _, r := range recs {
			code := r.Code
			if code == "" {
				code = forced
			}
			if code == "" && r.Name != "" {
				code, _ = ix.match(r.Name, false, 0)
			}
			if code == "" {
				res.Issues = append(res.Issues, ingest.RowIssue{Row: r.Row, Message: "약품을 알 수 없어 건너뜀 (약품코드 열이 없으면 대상 약품을 지정하세요)"})
				continue
			}
			sid, err := sr.resolve(r.Supplier)
			if err != nil {
				return err
			}
			if !sid.Valid {
				continue
			}
			if _, err := tx.Exec("INSERT INTO lot_evidence(product_code, supplier_id, date, qty, lot, expiry, source, batch_id, created_at) VALUES(?, ?, ?, ?, ?, ?, 'import', ?, ?)",
				code, sid.Int64, r.Date, int64(r.Qty), r.Lot, r.Expiry, batchID, now); err != nil {
				return err
			}
			res.Rows++
		}
		res.Suppliers = sr.added
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Message = fmt.Sprintf("제조번호 근거 %d건을 저장했습니다", res.Rows)
	return res, nil
}

// ── 가져오기 이력 ────────────────────────────────────────────────

type BatchView struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	FileName  string `json:"fileName"`
	RangeFrom string `json:"rangeFrom"`
	RangeTo   string `json:"rangeTo"`
	Rows      int    `json:"rows"`
	Live      int    `json:"live"` // 현재 남아 있는 행 (기간 교체로 줄어들 수 있음)
	CreatedAt string `json:"createdAt"`
}

func (s *Service) ListImports(limit int) ([]BatchView, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.Query(`SELECT b.id, b.kind, b.filename, IFNULL(b.range_from,''), IFNULL(b.range_to,''), b.row_count, b.created_at,
		CASE b.kind WHEN 'purchase' THEN (SELECT COUNT(*) FROM purchase_line WHERE batch_id = b.id)
		            WHEN 'dispense' THEN (SELECT COUNT(*) FROM dispense_line WHERE batch_id = b.id)
		            WHEN 'lot' THEN (SELECT COUNT(*) FROM lot_evidence WHERE batch_id = b.id)
		            ELSE (SELECT COUNT(*) FROM stock_anchor WHERE batch_id = b.id) END
		FROM import_batch b ORDER BY b.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BatchView
	for rows.Next() {
		var v BatchView
		if err := rows.Scan(&v.ID, &v.Kind, &v.FileName, &v.RangeFrom, &v.RangeTo, &v.Rows, &v.CreatedAt, &v.Live); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteImport 는 가져오기 1건과 그 행들을 지운다.
// DeleteImport 는 가져오기 한 건을 지운다 (그 파일로 들어온 행이 함께 지워진다).
// 남은 기록으로 단위 배율·포장 수량을 다시 계산하고, 수정 기록에 남긴다.
func (s *Service) DeleteImport(id int64) error {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		var kind, file, from, to string
		var n int
		if err := tx.QueryRow("SELECT kind, filename, range_from, range_to, row_count FROM import_batch WHERE id = ?", id).Scan(&kind, &file, &from, &to, &n); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if kind == "drug_master" || kind == "gtin" { // 목록 전체를 바꾸는 자료 — 이 기록만 지우면 행은 남고 '지웠다'고만 보인다
			return errors.New("약제급여목록·표준코드는 지울 수 없습니다 — 새 목록을 받거나 올리면 바뀝니다")
		}
		codes := map[string]bool{}
		for _, q := range []string{
			"SELECT DISTINCT product_code FROM purchase_line WHERE batch_id = ?",
			"SELECT DISTINCT product_code FROM dispense_line WHERE batch_id = ?",
			"SELECT DISTINCT product_code FROM initial_line WHERE batch_id = ?",
			"SELECT DISTINCT product_code FROM count_line WHERE batch_id = ?",
			"SELECT DISTINCT product_code FROM stock_anchor WHERE batch_id = ?",
		} {
			rows, err := tx.Query(q, id)
			if err != nil {
				return err
			}
			for rows.Next() {
				var c string
				if rows.Scan(&c) == nil {
					codes[c] = true
				}
			}
			rows.Close()
		}
		if _, err := tx.Exec("DELETE FROM import_batch WHERE id = ?", id); err != nil {
			return err
		}
		if kind == "purchase" {
			if err := updatePackSizes(tx); err != nil {
				return err
			}
		}
		if len(codes) > 0 {
			if err := recomputeUnits(tx, sortedKeys(codes)); err != nil {
				return err
			}
		}
		label := map[string]string{"purchase": "사입", "dispense": "조제", "initial_stock": "초기재고", "stock_count": "실사", "lot": "로트", "drug_master": "급여목록", "gtin": "표준코드"}[kind]
		if label == "" {
			label = kind
		}
		what := fmt.Sprintf("%s %s (%s행", label, file, comma(n))
		if from != "" {
			what += ", " + from
			if to != "" && to != from {
				what += " ~ " + to
			}
		}
		what += ")"
		return logChange(tx, ChangeEntry{Kind: "import_delete", OldValue: what, NewValue: "삭제", Note: fmt.Sprintf("품목 %d개 다시 계산", len(codes)), Source: "가져오기"})
	})
}

// ── 초기재고 미해결 행 ────────────────────────────────────────────

type UnresolvedRow struct {
	ID         int64       `json:"id"`
	Sheet      string      `json:"sheet"`
	Row        int         `json:"row"`
	Name       string      `json:"name"`
	Qty        domain.Qty  `json:"qty"`
	UnitPrice  float64     `json:"unitPrice"`
	Spec       string      `json:"spec"`
	Candidates []Candidate `json:"candidates"`
}

func (s *Service) ListUnresolved() ([]UnresolvedRow, error) {
	rows, err := s.DB.Query("SELECT id, sheet, row, name, qty, IFNULL(unit_price,0), spec, candidates FROM initial_unresolved WHERE resolved_code = '' AND ignored = 0 ORDER BY sheet, row")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnresolvedRow
	for rows.Next() {
		var u UnresolvedRow
		var c string
		var q int64
		if err := rows.Scan(&u.ID, &u.Sheet, &u.Row, &u.Name, &q, &u.UnitPrice, &u.Spec, &c); err != nil {
			return nil, err
		}
		u.Qty = domain.Qty(q)
		_ = json.Unmarshal([]byte(c), &u.Candidates)
		out = append(out, u)
	}
	return out, rows.Err()
}

// ResolveUnresolved 는 미해결 행을 약품에 연결하고 초기재고에 더한다. code 가 "" 이면 무시 처리.
func (s *Service) ResolveUnresolved(id int64, code string) error {
	var name, sheetName string
	var qty int64
	if err := s.DB.QueryRow("SELECT name, sheet, qty FROM initial_unresolved WHERE id = ?", id).Scan(&name, &sheetName, &qty); err != nil {
		return ErrNotFound
	}
	what := fmt.Sprintf("%s (%s, 원본 %s)", name, sheetName, domain.Qty(qty))
	if code == "" {
		return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
			if _, err := tx.Exec("UPDATE initial_unresolved SET ignored = 1 WHERE id = ?", id); err != nil {
				return err
			}
			return logChange(tx, ChangeEntry{Kind: "initial_ignore", OldValue: what, NewValue: "제외 (약가인하 대상 아님)", Note: "초기재고 확인 필요 행", Source: "재고"})
		})
	}
	code = domain.NormalizeProductCode(code)
	if code == "" {
		return errors.New("약품코드 형식이 올바르지 않습니다")
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if err := resolveUnresolvedTx(tx, s, id, "", code, "user"); err != nil {
			return err
		}
		return logChange(tx, ChangeEntry{Kind: "initial_resolve", ProductCode: code, OldValue: what, NewValue: code, Note: "초기재고 확인 필요 행 → 약품 지정", Source: "재고"})
	})
}

// ProductCodeInfo : 사용자가 직접 입력한 보험코드를 확인한 결과 (지정 전에 화면에 보여준다)
type ProductCodeInfo struct {
	Code   string `json:"code"`   // 9자리로 정규화한 보험코드 ("" = 형식이 맞지 않음)
	Name   string `json:"name"`   // 약국 품목·급여목록의 이름 (모르는 코드면 "")
	Source string `json:"source"` // pharmacy(약국 품목) | master(급여목록) | ""(프로그램에 없는 코드)
}

// normalizeEDIInput : 화면에서 입력한 보험코드를 9자리로 맞춘다 (공백·하이픈 허용, 앞자리 0 이 빠진 8자리 허용). 아니면 "".
func normalizeEDIInput(raw string) string {
	s := strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(raw))
	code := domain.NormalizeProductCode(s)
	if !domain.IsEDICode(code) {
		return ""
	}
	return code
}

// CheckProductCode 는 직접 입력한 보험코드가 형식에 맞는지, 프로그램이 아는 약인지 알려준다.
func (s *Service) CheckProductCode(raw string) (*ProductCodeInfo, error) {
	info := &ProductCodeInfo{Code: normalizeEDIInput(raw)}
	if info.Code == "" {
		return info, nil
	}
	var name string
	switch err := s.DB.QueryRow("SELECT name FROM product WHERE code = ?", info.Code).Scan(&name); {
	case err == nil:
		info.Name, info.Source = name, "pharmacy"
		return info, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}
	switch err := s.DB.QueryRow("SELECT name FROM drug_master WHERE code = ?", info.Code).Scan(&name); {
	case err == nil:
		info.Name, info.Source = name, "master"
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}
	return info, nil
}

// ResolveUnresolvedByCode 는 후보·검색에 없는 약을 사용자가 입력한 보험코드(9자리)로 지정하고 초기재고에 더한다.
// 프로그램 목록(약국 품목·급여목록)에 없는 코드도 받는다. 이름은 파일의 약품명을 쓰고, 나중에 급여목록을 올리면 바로잡힌다.
// 같은 코드가 이미 재고에 있으면 그 품목에 합쳐진다.
func (s *Service) ResolveUnresolvedByCode(id int64, raw string) error {
	code := normalizeEDIInput(raw)
	if code == "" {
		return errors.New("보험코드는 숫자 9자리로 입력하세요 (예: 987654320)")
	}
	info, err := s.CheckProductCode(code)
	if err != nil {
		return err
	}
	var name, sheetName string
	var qty int64
	if err := s.DB.QueryRow("SELECT name, sheet, qty FROM initial_unresolved WHERE id = ?", id).Scan(&name, &sheetName, &qty); err != nil {
		return ErrNotFound
	}
	what := fmt.Sprintf("%s (%s, 원본 %s)", name, sheetName, domain.Qty(qty))
	note := "초기재고 확인 필요 행 → 보험코드 직접 입력"
	if info.Source == "" {
		note += " (급여목록·약국 품목에 없는 코드)"
	}
	return s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		if err := resolveUnresolvedTx(tx, s, id, "", code, "user"); err != nil {
			return err
		}
		return logChange(tx, ChangeEntry{Kind: "initial_resolve", ProductCode: code, OldValue: what, NewValue: code, Note: note, Source: "재고"})
	})
}

// resolveUnresolvedTx : 미확인 행 id 를 code 로 지정한다. expectName 을 주면 그 이름의 행일 때만 (뒤에서 다시 매칭하는 동안
// 초기재고를 새로 올려 같은 id 가 다른 행이 된 경우를 막는다 — id 는 다시 쓰일 수 있다).
func resolveUnresolvedTx(tx *sql.Tx, s *Service, id int64, expectName, code, by string) error {
	var qty int64
	var price float64
	var batchID int64
	var sheetName, name, unit string
	var row int
	err := tx.QueryRow("SELECT qty, IFNULL(unit_price,0), batch_id, sheet, row, name, unit_label FROM initial_unresolved WHERE id = ? AND resolved_code = '' AND ignored = 0", id).
		Scan(&qty, &price, &batchID, &sheetName, &row, &name, &unit)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expectName != "" && name != expectName {
		return ErrNotFound
	}
	var p any
	if price > 0 {
		p = price
	}
	if _, err := tx.Exec(`INSERT INTO initial_line(batch_id, sheet, row, product_code, name, raw_qty, raw_unit_price, unit_label, factor, qty, unresolved_id, matched_by)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`, batchID, sheetName, row, code, name, qty, p, unit, qty, id, by); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE initial_unresolved SET resolved_code = ? WHERE id = ?", code, id); err != nil {
		return err
	}
	var mname string
	if tx.QueryRow("SELECT name FROM drug_master WHERE code = ?", code).Scan(&mname) == nil && mname != "" {
		name = mname
	}
	_, _ = tx.Exec("INSERT INTO product(code, name, updated_at) VALUES(?, ?, ?) ON CONFLICT(code) DO NOTHING", code, name, store.Now())
	return recomputeUnits(tx, []string{code})
}

// rematchUnresolved : 사전(사입·조제·급여목록·고시)이 바뀌면 초기재고 이름 매칭을 다시 한다.
// 앱에서는 백그라운드로 돌리고(여러 번 요청이 와도 한 번으로 합침), 끝나면 화면에 알린다.
func (s *Service) rematchUnresolved() {
	if !s.AsyncRematch {
		s.rematchNow()
		return
	}
	s.rematchMu.Lock()
	if s.rematchBusy {
		s.rematchDirty = true
		s.rematchMu.Unlock()
		return
	}
	s.rematchBusy = true
	s.rematchMu.Unlock()
	go func() {
		for {
			// 가져오기(특히 초기재고 다시 올리기)와 겹치지 않게 — 읽은 목록이 그사이 바뀌면 엉뚱한 행을 지정할 수 있다
			s.importMu.Lock()
			s.rematchNow()
			s.importMu.Unlock()
			s.rematchMu.Lock()
			if !s.rematchDirty {
				s.rematchBusy = false
				s.rematchMu.Unlock()
				break
			}
			s.rematchDirty = false
			s.rematchMu.Unlock()
		}
		if s.OnDataChanged != nil {
			s.OnDataChanged("initial_stock")
		}
	}()
}

func (s *Service) rematchNow() {
	list, err := s.ListUnresolved()
	if err != nil {
		return
	}
	type autoLine struct {
		id         int64
		code, name string
		price      float64
	}
	var autos []autoLine
	if rows, err := s.DB.Query("SELECT id, product_code, name, IFNULL(raw_unit_price, 0) FROM initial_line WHERE matched_by = 'name'"); err == nil {
		for rows.Next() {
			var a autoLine
			if rows.Scan(&a.id, &a.code, &a.name, &a.price) == nil {
				autos = append(autos, a)
			}
		}
		rows.Close()
	}
	if len(list) == 0 && len(autos) == 0 {
		return
	}
	ix := s.buildNameIndex()
	matched, moved := 0, 0
	err = s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		for _, u := range list {
			code, cands := ix.match(u.Name, u.Spec != "", u.UnitPrice)
			if code != "" {
				// 행마다 저장점 — 한 행이 중간에 실패하면 그 행만 되돌린다 (반쯤 들어간 행이 다음에 중복되지 않게)
				if _, err := tx.Exec("SAVEPOINT rematch_row"); err != nil {
					return err
				}
				err := resolveUnresolvedTx(tx, s, u.ID, u.Name, code, "name")
				if err == nil {
					if _, err := tx.Exec("RELEASE rematch_row"); err != nil {
						return err
					}
					matched++
					continue
				}
				if _, rerr := tx.Exec("ROLLBACK TO rematch_row"); rerr != nil {
					return rerr
				}
				if _, rerr := tx.Exec("RELEASE rematch_row"); rerr != nil {
					return rerr
				}
				if !errors.Is(err, ErrNotFound) {
					s.Log.Warn("초기재고 재매칭 실패", "row", u.ID, "code", code, "err", err)
				}
			}
			if _, err := tx.Exec("UPDATE initial_unresolved SET candidates = ? WHERE id = ? AND name = ? AND resolved_code = ''", toJSON(cands), u.ID, u.Name); err != nil {
				return err
			}
		}
		// 이름으로 자동 매칭했던 행은 사전(급여목록·사입)이 늘면 다시 판단한다 → 가져오는 순서와 무관하게 같은 결과
		var touched []string
		for _, a := range autos {
			code, _ := ix.match(a.name, false, a.price)
			if code == "" || code == a.code {
				continue // 더 확실한 답이 없으면 그대로 둔다
			}
			res, err := tx.Exec("UPDATE initial_line SET product_code = ? WHERE id = ? AND name = ? AND product_code = ? AND matched_by = 'name'", code, a.id, a.name, a.code)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue // 그사이 바뀐 행 (초기재고를 새로 올림 등)
			}
			if mn := ix.nameOf(code); mn != "" {
				_, _ = tx.Exec("INSERT INTO product(code, name, updated_at) VALUES(?, ?, ?) ON CONFLICT(code) DO NOTHING", code, mn, store.Now())
			}
			touched = append(touched, a.code, code)
			moved++
		}
		if len(touched) > 0 {
			if _, err := tx.Exec("DELETE FROM stock_anchor WHERE source = 'initial' AND product_code NOT IN (SELECT product_code FROM initial_line) AND batch_id IN (SELECT batch_id FROM initial_line)"); err != nil {
				return err
			}
			return recomputeUnits(tx, touched)
		}
		return nil
	})
	if err != nil {
		s.Log.Warn("초기재고 재매칭 오류", "err", err)
	}
	if matched > 0 || moved > 0 {
		s.Log.Info("초기재고 재매칭", "matched", matched, "moved", moved)
	}
}

func comma(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 0 {
		return "-" + comma(-n)
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// sortedKeys 헬퍼
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// importGTINs : 공식 표준코드 매핑. 사용자가 직접 확정한 매핑은 덮어쓰지 않는다.
func (s *Service) importGTINs(name, sha string, tables []*ingest.Table) (*ImportResult, error) {
	res := &ImportResult{}
	var recs []ingest.GTINRec
	for _, t := range tables {
		r, iss := ingest.ParseGTINs(t)
		recs = append(recs, r...)
		res.Issues = append(res.Issues, iss...)
	}
	if len(recs) == 0 {
		return nil, errors.New("표준코드·제품코드 행을 찾지 못했습니다")
	}
	err := s.DB.Tx(nilCtx(), func(tx *sql.Tx) error {
		batchID, err := insertBatch(tx, "gtin", name, sha, "", "", len(recs), nil)
		if err != nil {
			return err
		}
		res.BatchID = batchID
		stmt, err := tx.Prepare(`INSERT INTO gtin_map(gtin, product_code, created_at, source, pack_qty, name) VALUES(?, ?, ?, 'official', ?, ?)
			ON CONFLICT(gtin) DO UPDATE SET product_code = excluded.product_code, source = 'official', pack_qty = excluded.pack_qty, name = excluded.name
			WHERE gtin_map.source <> 'user'`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := store.Now()
		for _, r := range recs {
			var pack any
			if r.Pack > 0 {
				pack = r.Pack
			}
			if _, err := stmt.Exec(r.GTIN, r.Code, now, pack, r.Name); err != nil {
				return err
			}
		}
		return recomputeUnits(tx, nil) // 포장 수량(제품총수량)이 단위 판단 근거가 된다
	})
	if err != nil {
		return nil, err
	}
	s.rematchUnresolved()
	s.noteManualStdFile(name)
	res.Rows = len(recs)
	res.Message = fmt.Sprintf("표준코드 매핑 %s건을 반영했습니다 (출처: 건강보험심사평가원, 공공누리 제1유형)", comma(len(recs)))
	return res, nil
}
