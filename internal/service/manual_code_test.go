package service

import (
	"testing"
)

// 후보·검색에 없는 약을 사용자가 보험코드로 직접 지정 → 초기재고에 더해지고, 같은 코드끼리 합쳐진다
func TestResolveUnresolvedByCode(t *testing.T) {
	s := unitTestService(t, [][]any{{"600000010", "가나팩정_(28정)", "28", "정/팩", 6000}})
	mustImport(t, s, ImportRequest{Kind: "initial_stock", FileName: "init.xlsx", BaselineDate: "2026-03-01", DataBase64: xlsxB64(t, map[string][][]any{"Sheet": {
		{"약품명", "재고수량"},
		{"하하하연고", 30},
		{"호호호시럽", 20},
	}})})
	list, err := s.ListUnresolved()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("미확인 %d행 (2행이어야 함): %+v", len(list), list)
	}

	// 형식 검사: 9자리 숫자만 (공백·하이픈 허용, 앞 0 이 빠진 8자리는 0 을 붙임)
	for in, want := range map[string]string{"699900010": "699900010", " 6999-00010 ": "699900010", "12345670": "012345670", "12345": "", "abc": "", "1234567890": ""} {
		info, err := s.CheckProductCode(in)
		if err != nil {
			t.Fatal(err)
		}
		if info.Code != want {
			t.Fatalf("CheckProductCode(%q) = %q, want %q", in, info.Code, want)
		}
	}
	if info, _ := s.CheckProductCode("600000010"); info.Source != "master" || info.Name == "" {
		t.Fatalf("급여목록 코드: %+v", info)
	}
	if info, _ := s.CheckProductCode("699900010"); info.Source != "" {
		t.Fatalf("모르는 코드: %+v", info)
	}
	if err := s.ResolveUnresolvedByCode(list[0].ID, "12345"); err == nil {
		t.Fatal("형식이 틀린 코드를 받아들임")
	}

	// 프로그램에 없는 코드로 지정 → 재고 품목이 생기고 초기재고가 잡힌다
	const code = "699900010"
	if err := s.ResolveUnresolvedByCode(list[0].ID, code); err != nil {
		t.Fatal(err)
	}
	first := anchorQty(t, s, code, "initial")
	if first <= 0 {
		t.Fatalf("초기재고 %s (0보다 커야 함)", first)
	}
	if info, _ := s.CheckProductCode(code); info.Source != "pharmacy" || info.Name != list[0].Name {
		t.Fatalf("지정 후 약국 품목: %+v", info)
	}
	// 다른 행을 같은 코드로 지정 → 같은 품목에 합쳐진다
	if err := s.ResolveUnresolvedByCode(list[1].ID, code); err != nil {
		t.Fatal(err)
	}
	if q := anchorQty(t, s, code, "initial"); q <= first {
		t.Fatalf("합친 뒤 초기재고 %s (처음 %s보다 커야 함)", q, first)
	}
	if left, _ := s.ListUnresolved(); len(left) != 0 {
		t.Fatalf("지정 후에도 미확인 %d행", len(left))
	}
	// 이미 지정한 행을 다시 지정하면 거부 (중복으로 더해지지 않게)
	if err := s.ResolveUnresolvedByCode(list[0].ID, code); err == nil {
		t.Fatal("이미 지정한 행을 다시 지정함")
	}
	// 수정 기록에 '직접 입력'으로 남는다
	logs, err := s.ChangeLog(ChangeFilter{ProductCode: code, Kind: "initial_resolve"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("수정 기록 %d건 (2건이어야 함)", len(logs))
	}
}
