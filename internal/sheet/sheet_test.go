package sheet

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func normRows(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		c := make([]string, len(r))
		for j := range r {
			c[j] = strings.TrimSpace(r[j])
		}
		for len(c) > 0 && c[len(c)-1] == "" {
			c = c[:len(c)-1]
		}
		out[i] = c
	}
	for len(out) > 0 && len(out[len(out)-1]) == 0 {
		out = out[:len(out)-1]
	}
	return out
}

func compare(t *testing.T, label string, data []byte) {
	t.Helper()
	fast, err := readXLSXFast(data)
	if err != nil {
		t.Fatalf("%s: fast %v", label, err)
	}
	slow, err := readXLSXExcelize(data)
	if err != nil {
		t.Fatalf("%s: excelize %v", label, err)
	}
	if len(fast) != len(slow) {
		t.Fatalf("%s: 시트 수 %d vs %d", label, len(fast), len(slow))
	}
	for i := range fast {
		if fast[i].Name != slow[i].Name {
			t.Fatalf("%s: 시트 이름 %q vs %q", label, fast[i].Name, slow[i].Name)
		}
		a, b := normRows(fast[i].Rows), normRows(slow[i].Rows)
		if len(a) != len(b) {
			t.Fatalf("%s/%s: 행 수 %d vs %d", label, fast[i].Name, len(a), len(b))
		}
		bad := 0
		for r := range a {
			if strings.Join(a[r], "\x00") != strings.Join(b[r], "\x00") {
				bad++
				if bad <= 3 {
					t.Errorf("%s/%s 행 %d:\n fast %q\n xlsx %q", label, fast[i].Name, r+1, a[r], b[r])
				}
			}
		}
		if bad > 0 {
			t.Fatalf("%s/%s: 다른 행 %d개", label, fast[i].Name, bad)
		}
	}
}

func TestFastReaderMatchesExcelize(t *testing.T) {
	f := excelize.NewFile()
	_ = f.SetSheetName("Sheet1", "사입현황 & <특수>")
	rows := [][]any{
		{"사입현황 20260202 ~ 20260331"},
		{"일자", "약품명", "약품코드", "수량", "단가", "", "비고"},
		{"20260115", "새봄점안액1%(가상성분)(1회용)_(4mg/0.4mL)", "912345670", 48, 12340.5, nil, "줄\n바꿈 \"따옴표\" 'a' & b"},
		{20260303, "  앞뒤 공백  ", "012345670", -0.25, 1e-7, nil, nil},
	}
	for i, r := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		_ = f.SetSheetRow("사입현황 & <특수>", cell, &r)
	}
	_ = f.SetCellFormula("사입현황 & <특수>", "H3", "D3*E3")
	_ = f.SetCellValue("사입현황 & <특수>", "Z10", "먼 칸")
	_ = f.SetCellValue("사입현황 & <특수>", "B12", true)
	f.NewSheet("두번째")
	_ = f.SetCellValue("두번째", "A1", "x")
	_ = f.SetCellValue("두번째", "C5", 3.14159)
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatal(err)
	}
	compare(t, "합성", buf.Bytes())
}

// 실데이터 (커밋하지 않는 폴더) — 모든 xlsx 를 excelize 결과와 셀 단위로 비교한다.
func TestFastReaderRealData(t *testing.T) {
	dir := os.Getenv("DPR_REALDATA")
	if dir == "" {
		t.Skip("DPR_REALDATA 미설정")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.xlsx"))
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		compare(t, filepath.Base(p), data)
	}
}

// rawXLSX : 시트·공유 문자열 XML 을 그대로 넣은 최소 xlsx (다른 프로그램이 만든 파일 흉내)
func rawXLSX(t *testing.T, sheetXML, sstXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	put("[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/></Types>`)
	put("_rels/.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`)
	put("xl/workbook.xml", `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="S" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	put("xl/_rels/workbook.xml.rels", `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/></Relationships>`)
	put("xl/worksheets/sheet1.xml", `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`+sheetXML+`</sheetData></worksheet>`)
	put("xl/sharedStrings.xml", `<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`+sstXML+`</sst>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// r 속성 없는 셀(빈 셀 <c/> 포함)은 다음 칸으로 센다 — 열이 밀리면 값이 다른 열로 들어간다
func TestFastReaderCellsWithoutRef(t *testing.T) {
	data := rawXLSX(t,
		`<row><c t="s"><v>0</v></c><c s="1"/><c t="s"><v>1</v></c></row>`+
			`<row><c><v>1</v></c><c></c><c><v>3</v></c><c t="s"><v>2</v></c></row>`,
		`<si><t>A</t></si><si><t>C</t></si><si><t><![CDATA[a<b & c]]></t></si>`)
	compare(t, "r 없는 셀", data)
	got, err := readXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	if r := got[0].Rows; len(r) != 2 || strings.Join(r[0], "|") != "A||C" || strings.Join(r[1], "|") != "1||3|a<b & c" {
		t.Fatalf("행: %q", got[0].Rows)
	}
}

// 망가진 파일·터무니없는 행/열 번호는 앱을 죽이지 않고 오류로 끝난다
func TestFastReaderBadFilesDoNotCrash(t *testing.T) {
	for label, xml := range map[string]string{
		"큰 행 번호": `<row r="2000000000"><c r="A2000000000"><v>1</v></c></row>`,
		"큰 열 번호": `<row r="1"><c r="ZZZZZZZ1"><v>1</v></c></row>`,
		"넘치는 열":  `<row r="1"><c r="ZZZZZZZZZZZZZZZZZZZZZZZZZ1"><v>1</v></c></row>`,
	} {
		data := rawXLSX(t, xml, "")
		if _, err := readXLSXFast(data); err == nil {
			t.Fatalf("%s: 빠른 리더는 오류여야 함", label)
		}
		_, _ = readXLSX(data) // excelize 로 넘어가도 죽지 않아야 함
	}
	// zip 머리에 크기를 거짓으로 적은 파일
	data := rawXLSX(t, `<row r="1"><c r="A1"><v>1</v></c></row>`, "")
	i := bytes.Index(data, []byte("xl/worksheets/sheet1.xml"))
	if i < 0 {
		t.Fatal("시험 파일")
	}
	if _, err := readXLSX(corruptSizes(data)); err == nil {
		t.Log("크기를 속인 파일도 읽힘 (괜찮음)")
	}
}

// corruptSizes : 중앙 디렉터리의 '압축 전 크기'를 아주 크게 바꾼다
func corruptSizes(data []byte) []byte {
	out := append([]byte(nil), data...)
	sig := []byte{0x50, 0x4b, 0x01, 0x02}
	for i := 0; ; {
		j := bytes.Index(out[i:], sig)
		if j < 0 {
			break
		}
		p := i + j + 24 // uncompressed size (4 bytes)
		if p+4 <= len(out) {
			out[p], out[p+1], out[p+2], out[p+3] = 0xff, 0xff, 0xff, 0x7f
		}
		i = i + j + 4
	}
	return out
}
