package service

import "testing"

// 사입정보 사진 판독 결과 저장·수정 (판독은 lotocr_test.go)
func TestLotPhotoSaveAndEdit(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)

	garam := supplierID(t, s, "가람도매")
	match := s.supplierMatcher()
	if id, _ := match("(주)가람도매"); id != garam {
		t.Fatalf("도매 이름 맞추기: %d, 기대 %d", id, garam)
	}
	if id, _ := match("가람"); id != garam {
		t.Fatalf("일부 이름: %d", id)
	}
	if id, _ := match("없는약품상사"); id != 0 {
		t.Fatalf("없는 도매는 0: %d", id)
	}

	rows := []LotInput{
		{Code: "900000010", SupplierID: garam, Date: "2026-03-25", Qty: 30, Expiry: "2028-09-30", Lot: "31O07-2"}, // 판독 실수 (O ↔ 0)
		{Code: "900000010", SupplierID: garam, Date: "2026-03-25", Qty: 30, Expiry: "2028-09-30", Lot: "31O07-2"}, // 같은 줄은 한 번만
	}
	n, err := s.AddLots(rows)
	if err != nil || n != 1 {
		t.Fatalf("저장: %d %v", n, err)
	}
	if _, err := s.AddLots([]LotInput{{Code: "900000010", Lot: "X"}}); err == nil {
		t.Fatal("도매 없는 줄은 거부해야 함")
	}

	ev, err := s.ListLotEvidence([]string{"900000010", "900000020"})
	if err != nil || len(ev) != 1 || ev[0].Source != "photo" || ev[0].Code != "900000010" {
		t.Fatalf("근거 목록: %v %+v", err, ev)
	}
	in := LotInput{Code: "900000010", SupplierID: garam, Date: "20260325", Qty: 30, Expiry: "20280930", Lot: "31007-2"}
	if err := s.UpdateLot(ev[0].ID, in); err != nil {
		t.Fatal(err)
	}
	ev, _ = s.ListLotEvidence([]string{"900000010"})
	if ev[0].Lot != "31007-2" || ev[0].Date != "2026-03-25" || ev[0].Expiry != "2028-09-30" {
		t.Fatalf("수정 결과: %+v", ev[0])
	}
}
