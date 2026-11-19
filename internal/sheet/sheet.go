// Package sheet 는 엑셀(xlsx/xlsm)·CSV 파일을 '문자열 2차원 배열' 로 읽는 얇은 어댑터다.
// 셀 서식 대신 원본 값(RawCellValue)을 읽어 숫자 코드의 앞자리 0, 날짜 일련번호 등을 직접 해석한다.
package sheet

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/transform"
)

type Sheet struct {
	Name string     `json:"name"`
	Rows [][]string `json:"-"`
}

// ReadFile 은 확장자에 따라 xlsx 또는 csv 를 읽는다.
func ReadFile(path string) ([]Sheet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReadBytes(filepath.Base(path), data)
}

// ReadBytes 는 파일 이름(확장자 판별용)과 내용으로 시트를 읽는다.
func ReadBytes(name string, data []byte) ([]Sheet, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".xlsx", ".xlsm", ".xltx":
		return readXLSX(data)
	case ".csv", ".txt":
		return readCSV(name, data)
	case ".xls":
		return nil, fmt.Errorf("구형 엑셀(.xls)은 아직 지원하지 않습니다. 엑셀에서 '다른 이름으로 저장 → .xlsx' 후 다시 올려주세요")
	}
	// 확장자가 이상해도 내용이 xlsx(zip)면 읽는다 (예: 'xxx.xlsx.xlsx', 메신저 파일명 변형)
	if bytes.HasPrefix(data, []byte("PK")) {
		return readXLSX(data)
	}
	return nil, fmt.Errorf("지원하지 않는 파일 형식입니다: %s", ext)
}

func readXLSX(data []byte) ([]Sheet, error) {
	out, err := readFastSafe(data)
	if err == nil { // 빠른 리더 (fastxlsx.go) — 실패하면 excelize 로
		for _, sh := range out {
			for i := range sh.Rows {
				for j := range sh.Rows[i] {
					sh.Rows[i][j] = strings.TrimSpace(sh.Rows[i][j])
				}
			}
		}
		return out, nil
	}
	if errors.Is(err, ErrSheetLimit) { // 한도를 넘는 파일은 excelize 로도 읽지 않는다 (메모리·시간 폭주 방지)
		return nil, err
	}
	return readXLSXExcelize(data)
}

// readFastSafe : 빠른 리더의 패닉도 오류로 바꿔 excelize 로 넘어가게 한다
func readFastSafe(data []byte) (out []Sheet, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%w: %v", errFastUnsupported, r)
		}
	}()
	return readXLSXFast(data)
}

func readXLSXExcelize(data []byte) ([]Sheet, error) {
	// 시트 XML 이 기본 한도(16MB)를 넘으면 excelize 가 임시 파일로 풀어 조금씩 읽는다(시스템 호출이 시간의 60%).
	// 약국 엑셀은 수십 MB 수준이라 메모리에서 바로 읽는다.
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipXMLSizeLimit: 1 << 30, UnzipSizeLimit: 2 << 30})
	if err != nil {
		return nil, fmt.Errorf("엑셀 파일을 열 수 없습니다: %w", err)
	}
	defer f.Close()
	var out []Sheet
	for _, name := range f.GetSheetList() {
		rows, err := f.GetRows(name, excelize.Options{RawCellValue: true})
		if err != nil {
			return nil, fmt.Errorf("시트 %q 읽기 실패: %w", name, err)
		}
		for i := range rows {
			for j := range rows[i] {
				rows[i][j] = strings.TrimSpace(rows[i][j])
			}
		}
		out = append(out, Sheet{Name: name, Rows: rows})
	}
	return out, nil
}

func readCSV(name string, data []byte) ([]Sheet, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var reader io.Reader = bytes.NewReader(data)
	if !utf8.Valid(data) { // 국내 프로그램 CSV 는 CP949(EUC-KR) 가 흔하다
		reader = transform.NewReader(bytes.NewReader(data), korean.EUCKR.NewDecoder())
	}
	r := csv.NewReader(reader)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV 읽기 실패: %w", err)
	}
	for i := range rows {
		for j := range rows[i] {
			rows[i][j] = strings.TrimSpace(rows[i][j])
		}
	}
	return []Sheet{{Name: strings.TrimSuffix(name, filepath.Ext(name)), Rows: rows}}, nil
}

// Cell 은 범위를 벗어나면 "" 를 돌려준다.
func Cell(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return row[idx]
}

// ColumnLetter : 0 → A, 25 → Z, 26 → AA (매핑 화면 표시용)
func ColumnLetter(idx int) string {
	s := ""
	for idx >= 0 {
		s = string(rune('A'+idx%26)) + s
		idx = idx/26 - 1
	}
	return s
}
