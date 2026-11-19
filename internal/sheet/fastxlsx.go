package sheet

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// 빠른 xlsx 리더 — 시트 XML 을 바이트 단위로 직접 훑는다.
//
// excelize 는 범용 encoding/xml 로 셀마다 토큰을 만들어 30,000행 사입현황에 0.9초가 걸렸다.
// 여기서는 필요한 태그(sheet·row·c·v·is·t·si)만 보고 값은 원본(RawCellValue 와 같은 의미)으로 돌려준다.
// 예상 밖 구조(Strict OOXML 등)면 errFastUnsupported 를 돌려주고 excelize 로 다시 읽는다.

var errFastUnsupported = errors.New("fast xlsx: unsupported structure")

// ErrSheetLimit : 엑셀 자체 한도(행 1,048,576 · 열 16,384)나 크기 한도를 넘는 파일 — 망가졌거나 악성일 수 있어 다른 리더로도 읽지 않는다.
var ErrSheetLimit = errors.New("엑셀 파일이 행·열·크기 한도를 넘습니다 (행 1,048,576 · 열 16,384 · 시트 512MB) — 파일이 손상되었을 수 있습니다")

const (
	maxRows     = 1 << 20 // 1,048,576
	maxCols     = 1 << 14 // 16,384
	maxPartSize = 512 << 20
)

type zipFiles map[string]*zip.File

func (z zipFiles) read(name string) ([]byte, error) {
	f := z[name]
	if f == nil {
		return nil, fmt.Errorf("%w: %s 없음", errFastUnsupported, name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	// 머리에 적힌 크기는 믿지 않는다 (거짓 크기로 거대한 메모리를 잡게 할 수 있음) — 미리 잡는 것은 64MB 까지, 읽는 것은 한도까지
	hint := f.UncompressedSize64
	if hint > 64<<20 {
		hint = 64 << 20
	}
	buf := bytes.NewBuffer(make([]byte, 0, int(hint)+512))
	n, err := io.Copy(buf, io.LimitReader(rc, maxPartSize+1))
	if err != nil {
		return nil, err
	}
	if n > maxPartSize {
		return nil, ErrSheetLimit
	}
	return buf.Bytes(), nil
}

// guard : 고루틴 안의 패닉을 오류로 바꾼다 (다른 고루틴의 패닉은 화면 바인딩의 복구가 잡지 못해 앱 전체가 죽는다)
func guard(errp *error) {
	if r := recover(); r != nil {
		*errp = fmt.Errorf("%w: %v", errFastUnsupported, r)
	}
}

func readXLSXFast(data []byte) ([]Sheet, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := zipFiles{}
	for _, f := range zr.File {
		files[strings.TrimPrefix(strings.ReplaceAll(f.Name, "\\", "/"), "/")] = f
	}
	wb, err := files.read("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	relsData, err := files.read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, err
	}
	if bytes.Contains(wb, []byte("purl.oclc.org/ooxml")) { // Strict OOXML
		return nil, errFastUnsupported
	}
	type rel struct{ typ, target string }
	rels := map[string]rel{}
	sstPath := ""
	forEachTag(relsData, func(name string, tag []byte, _ bool) {
		if name != "Relationship" {
			return
		}
		id, _ := attr(tag, "Id")
		typ, _ := attr(tag, "Type")
		target, _ := attr(tag, "Target")
		rels[id] = rel{typ, target}
		if strings.HasSuffix(typ, "/sharedStrings") {
			sstPath = resolveTarget(target)
		}
	})
	type sheetRef struct{ name, path string }
	var refs []sheetRef
	forEachTag(wb, func(name string, tag []byte, _ bool) {
		if name != "sheet" {
			return
		}
		n, _ := attr(tag, "name")
		id, ok := attr(tag, "r:id")
		if !ok {
			id, _ = attrSuffix(tag, ":id")
		}
		r, ok := rels[id]
		if !ok || !strings.HasSuffix(r.typ, "/worksheet") {
			return // 차트 시트 등은 건너뛴다
		}
		refs = append(refs, sheetRef{unescape(n), resolveTarget(r.target)})
	})
	if len(refs) == 0 {
		return nil, errFastUnsupported
	}
	var sst []string
	if sstPath != "" {
		if b, err := files.read(sstPath); err == nil {
			sst = parseSharedStrings(b)
		} else {
			return nil, err
		}
	}
	out := make([]Sheet, len(refs))
	errs := make([]error, len(refs))
	var wg sync.WaitGroup
	for i, ref := range refs {
		wg.Add(1)
		go func(i int, ref sheetRef) {
			defer wg.Done()
			defer guard(&errs[i])
			b, err := files.read(ref.path)
			if err != nil {
				errs[i] = err
				return
			}
			rows, err := parseSheet(b, sst)
			out[i], errs[i] = Sheet{Name: ref.name, Rows: rows}, err
		}(i, ref)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}

func resolveTarget(t string) string {
	t = strings.ReplaceAll(t, "\\", "/")
	if strings.HasPrefix(t, "/") {
		return strings.TrimPrefix(t, "/")
	}
	return path.Clean("xl/" + t)
}

// forEachTag 는 여는 태그마다 fn(지역 이름, 태그 바이트, 스스로 닫힘)을 부른다 (작은 XML 용).
func forEachTag(b []byte, fn func(name string, tag []byte, self bool)) {
	for i := 0; ; {
		j := bytes.IndexByte(b[i:], '<')
		if j < 0 {
			return
		}
		j += i
		if j+1 >= len(b) {
			return
		}
		k := bytes.IndexByte(b[j:], '>')
		if k < 0 {
			return
		}
		k += j
		tag := b[j+1 : k]
		i = k + 1
		if len(tag) == 0 || tag[0] == '/' || tag[0] == '?' || tag[0] == '!' {
			continue
		}
		self := tag[len(tag)-1] == '/'
		fn(localName(tag), tag, self)
	}
}

func localName(tag []byte) string { return string(localNameB(tag)) }

// localNameB 는 태그 이름(접두 제외)을 할당 없이 돌려준다. switch string(b) 는 컴파일러가 할당하지 않는다.
func localNameB(tag []byte) []byte {
	end := len(tag)
	for i, c := range tag {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '/' {
			end = i
			break
		}
	}
	n := tag[:end]
	if p := bytes.IndexByte(n, ':'); p >= 0 {
		n = n[p+1:]
	}
	return n
}

// attr 는 태그 안 name="값" 을 찾는다 (작은따옴표도 허용).
func attr(tag []byte, name string) (string, bool) {
	for i := 0; i < len(tag); {
		p := bytes.Index(tag[i:], []byte(name))
		if p < 0 {
			return "", false
		}
		p += i
		q := p + len(name)
		before := byte(' ')
		if p > 0 {
			before = tag[p-1]
		}
		if (before == ' ' || before == '\t' || before == '\n' || before == '\r') && q+1 < len(tag) && tag[q] == '=' && (tag[q+1] == '"' || tag[q+1] == '\'') {
			quote := tag[q+1]
			e := bytes.IndexByte(tag[q+2:], quote)
			if e < 0 {
				return "", false
			}
			return string(tag[q+2 : q+2+e]), true
		}
		i = q
	}
	return "", false
}

func attrSuffix(tag []byte, suffix string) (string, bool) {
	p := bytes.Index(tag, []byte(suffix+"="))
	if p < 0 {
		return "", false
	}
	s := p
	for s > 0 && tag[s-1] != ' ' {
		s--
	}
	return attr(tag, string(tag[s:p+len(suffix)]))
}

func parseSharedStrings(b []byte) []string {
	var out []string
	var cur strings.Builder
	inSI, inT, rph := false, false, 0
	textStart := 0
	for i := 0; i < len(b); {
		j := bytes.IndexByte(b[i:], '<')
		if j < 0 {
			break
		}
		j += i
		if inT && j > textStart {
			cur.WriteString(unescape(string(b[textStart:j])))
		}
		if bytes.HasPrefix(b[j:], []byte("<![CDATA[")) { // CDATA 안은 그대로 (이스케이프 풀지 않음)
			e := bytes.Index(b[j:], []byte("]]>"))
			if e < 0 {
				break
			}
			if inT {
				cur.Write(b[j+9 : j+e])
			}
			i = j + e + 3
			textStart = i
			continue
		}
		k := bytes.IndexByte(b[j:], '>')
		if k < 0 {
			break
		}
		k += j
		tag := b[j+1 : k]
		i = k + 1
		if len(tag) == 0 {
			continue
		}
		end := tag[0] == '/'
		name := localName(bytes.TrimPrefix(tag, []byte("/")))
		self := tag[len(tag)-1] == '/'
		switch name {
		case "si":
			if end {
				out = append(out, cur.String())
				cur.Reset()
				inSI = false
			} else if self {
				out = append(out, "")
			} else {
				inSI = true
				cur.Reset()
			}
		case "rPh": // 일본어 후리가나 — 값에 넣지 않는다
			if end {
				rph--
			} else if !self {
				rph++
			}
		case "t":
			if end {
				inT = false
			} else if !self && inSI && rph == 0 {
				inT, textStart = true, i
			}
		}
	}
	return out
}

// parseSheet 는 시트 XML 의 sheetData 를 행·열 문자열로 바꾼다.
// parseSheet 는 시트 XML 을 행·열 문자열로 바꾼다. 큰 시트는 행 경계로 나눠 여러 CPU 에서 동시에 읽는다.
func parseSheet(b []byte, sst []string) ([][]string, error) {
	start := bytes.Index(b, []byte("<sheetData"))
	end := bytes.LastIndex(b, []byte("</sheetData>"))
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if start < 0 || end <= start || end-start < 4<<20 || workers < 2 {
		rows, _, _, err := parseRows(b, sst)
		return trimRows(rows), err
	}
	// 행 경계('<row')에서 나눈다
	cuts := []int{start}
	for i := 1; i < workers; i++ {
		pos := start + (end-start)*i/workers
		j := bytes.Index(b[pos:end], []byte("<row"))
		if j < 0 {
			break
		}
		if c := pos + j; c > cuts[len(cuts)-1] {
			cuts = append(cuts, c)
		}
	}
	cuts = append(cuts, end)
	type part struct {
		rows  [][]string
		first int
		noRef bool
		err   error
	}
	parts := make([]part, len(cuts)-1)
	var wg sync.WaitGroup
	for i := 0; i+1 < len(cuts); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer guard(&parts[i].err)
			r, first, noRef, err := parseRows(b[cuts[i]:cuts[i+1]], sst)
			parts[i] = part{r, first, noRef, err}
		}(i)
	}
	wg.Wait()
	total := 0
	for _, pt := range parts {
		if pt.err != nil {
			return nil, pt.err
		}
		if pt.noRef { // 행 번호(r)가 없는 시트는 나눠 읽을 수 없다 → 한 번에
			rows, _, _, err := parseRows(b, sst)
			return trimRows(rows), err
		}
		if len(pt.rows) > total {
			total = len(pt.rows)
		}
	}
	rows := make([][]string, total)
	for _, pt := range parts {
		for i := pt.first; i >= 0 && i < len(pt.rows); i++ {
			if pt.rows[i] != nil {
				rows[i] = pt.rows[i]
			}
		}
	}
	return trimRows(rows), nil
}

// parseRows 는 XML 조각의 행을 읽는다. first: 첫 행 번호(-1 이면 행 없음), noRef: r 속성 없는 행이 있었음.
func parseRows(b []byte, sst []string) ([][]string, int, bool, error) {
	var rows [][]string
	var row []string
	rowIdx, col := -1, -1
	nextCol := 0 // r 속성 없는 셀의 열 = 앞 셀 다음 칸 (빈 셀 <c/> 도 한 칸 — excelize 와 같음)
	first, noRef := -1, false
	cellType := ""
	var val []byte
	inV, inIS, inT := false, false, false
	inCell := false
	textStart := 0
	var isBuf []byte
	// 같은 값(날짜·코드·단위·작은 수)이 수만 번 반복되므로 문자열을 한 번만 만든다 (할당·GC 감소)
	intern := make(map[string]string, 4096)
	internB := func(b []byte) string {
		if len(b) > 64 {
			return string(b)
		}
		if v, ok := intern[string(b)]; ok {
			return v
		}
		v := string(b)
		intern[v] = v
		return v
	}
	finishCell := func() error {
		var s string
		switch cellType {
		case "s":
			n, err := strconv.Atoi(strings.TrimSpace(string(val)))
			if err != nil || n < 0 || n >= len(sst) {
				if len(val) == 0 {
					s = ""
					break
				}
				return fmt.Errorf("%w: 공유 문자열 %q", errFastUnsupported, val)
			}
			s = sst[n]
		case "inlineStr":
			s = unescape(string(isBuf))
		default:
			if bytes.IndexByte(val, '&') < 0 && bytes.IndexByte(val, '_') < 0 && bytes.IndexByte(val, '\r') < 0 {
				s = internB(val) // 이스케이프가 없으면 바로
			} else {
				s = unescape(string(val))
			}
		}
		if col < 0 {
			col = len(row)
		}
		for len(row) < col {
			row = append(row, "")
		}
		if col == len(row) {
			row = append(row, s)
		} else {
			row[col] = s
		}
		return nil
	}
	for i := 0; i < len(b); {
		j := bytes.IndexByte(b[i:], '<')
		if j < 0 {
			break
		}
		j += i
		if inV && j > textStart {
			val = append(val, b[textStart:j]...)
		} else if inT && j > textStart {
			isBuf = append(isBuf, b[textStart:j]...)
		}
		if bytes.HasPrefix(b[j:], []byte("<![CDATA[")) {
			e := bytes.Index(b[j:], []byte("]]>"))
			if e < 0 {
				return nil, -1, false, errFastUnsupported
			}
			if inV {
				val = append(val, b[j+9:j+e]...)
			} else if inT {
				isBuf = append(isBuf, b[j+9:j+e]...)
			}
			i = j + e + 3
			textStart = i
			continue
		}
		k := bytes.IndexByte(b[j:], '>')
		if k < 0 {
			return nil, -1, false, errFastUnsupported
		}
		k += j
		tag := b[j+1 : k]
		i = k + 1
		textStart = i
		if len(tag) == 0 || tag[0] == '?' || tag[0] == '!' {
			continue
		}
		end := tag[0] == '/'
		if end {
			tag = tag[1:]
		}
		self := len(tag) > 0 && tag[len(tag)-1] == '/'
		switch string(localNameB(tag)) {
		case "row":
			if end {
				rows = append(rows, row)
				row = nil
				continue
			}
			r := rowIdx + 1
			if v, ok := attr(tag, "r"); ok {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					r = n - 1
				} else if err != nil && len(v) > 7 {
					return nil, -1, false, ErrSheetLimit
				}
			} else {
				noRef = true
			}
			if r >= maxRows {
				return nil, -1, false, ErrSheetLimit
			}
			if first < 0 {
				first = r
			}
			for len(rows) < r { // 빈 행 채우기
				rows = append(rows, nil)
			}
			rowIdx, col, row, nextCol = r, -1, nil, 0
			if self {
				rows = append(rows, nil)
			}
		case "c":
			if end {
				if inCell {
					if err := finishCell(); err != nil {
						return nil, -1, false, err
					}
				}
				inCell = false
				continue
			}
			col = nextCol
			if ref, ok := attr(tag, "r"); ok {
				if c, ok := columnIndex(ref); ok {
					col = c
				}
			}
			if col < 0 || col >= maxCols {
				return nil, -1, false, ErrSheetLimit
			}
			nextCol = col + 1
			cellType, _ = attr(tag, "t")
			val, isBuf = val[:0], isBuf[:0]
			inCell = !self
			if self { // 값 없는 셀
				continue
			}
		case "v":
			if end {
				inV = false
			} else if !self && inCell {
				inV = true
			}
		case "is":
			inIS = !end && !self
		case "t":
			if end {
				inT = false
			} else if !self && inIS {
				inT = true
			}
		case "rPh":
			if !end && !self && inIS { // 후리가나 텍스트는 건너뛴다
				e := bytes.Index(b[i:], []byte("</rPh>"))
				if e < 0 {
					e = bytes.Index(b[i:], []byte(":rPh>"))
				}
				if e >= 0 {
					i += e
					textStart = i
				}
			}
		}
	}
	return rows, first, noRef, nil
}

// trimRows : 뒤쪽 빈 칸·빈 행 정리 (excelize GetRows 와 같은 모양)
func trimRows(rows [][]string) [][]string {
	for len(rows) > 0 && isEmptyRow(rows[len(rows)-1]) {
		rows = rows[:len(rows)-1]
	}
	for i := range rows {
		r := rows[i]
		for len(r) > 0 && r[len(r)-1] == "" {
			r = r[:len(r)-1]
		}
		rows[i] = r
	}
	return rows
}

func isEmptyRow(r []string) bool {
	for _, c := range r {
		if c != "" {
			return false
		}
	}
	return true
}

// columnIndex : 'B3' → 1, 'AA10' → 26 (열 한도를 넘으면 한도 값 — 부르는 쪽에서 거른다, 넘침 방지)
func columnIndex(ref string) (int, bool) {
	n := 0
	i := 0
	for ; i < len(ref); i++ {
		c := ref[i]
		if n > maxCols {
			n = maxCols + 1
		}
		switch {
		case c >= 'A' && c <= 'Z':
			n = n*26 + int(c-'A'+1)
		case c >= 'a' && c <= 'z':
			n = n*26 + int(c-'a'+1)
		default:
			if i == 0 {
				return 0, false
			}
			return n - 1, true
		}
	}
	if i == 0 {
		return 0, false
	}
	return n - 1, true
}

// unescape : XML 엔티티 (&amp; &lt; &gt; &quot; &apos; &#NN; &#xHH;) 와 _xHHHH_ 이스케이프
func unescape(s string) string {
	if strings.IndexByte(s, '\r') >= 0 { // XML 줄바꿈 정규화: 원문 \r\n · \r → \n (엔티티 &#13; 는 아래에서 \r 로)
		s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	}
	if strings.IndexByte(s, '&') < 0 && !strings.Contains(s, "_x") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == '&' {
			if e := strings.IndexByte(s[i:], ';'); e > 1 && e < 12 {
				ent := s[i+1 : i+e]
				switch {
				case ent == "amp":
					b.WriteByte('&')
				case ent == "lt":
					b.WriteByte('<')
				case ent == "gt":
					b.WriteByte('>')
				case ent == "quot":
					b.WriteByte('"')
				case ent == "apos":
					b.WriteByte('\'')
				case strings.HasPrefix(ent, "#x") || strings.HasPrefix(ent, "#X"):
					if n, err := strconv.ParseUint(ent[2:], 16, 32); err == nil {
						b.WriteRune(rune(n))
					}
				case strings.HasPrefix(ent, "#"):
					if n, err := strconv.ParseUint(ent[1:], 10, 32); err == nil {
						b.WriteRune(rune(n))
					}
				default:
					b.WriteString(s[i : i+e+1])
				}
				i += e + 1
				continue
			}
		}
		// Excel 의 '_x000D_' 형식 이스케이프 (줄바꿈 등)
		if c == '_' && i+6 < len(s) && s[i+1] == 'x' && s[i+6] == '_' {
			if n, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
				b.WriteRune(rune(n))
				i += 7
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		b.WriteRune(r)
		i += size
	}
	return b.String()
}
