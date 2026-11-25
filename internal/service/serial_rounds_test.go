package service

import (
	"strings"
	"testing"
)

// 같은 일련번호(포장 하나)는 한 번만 보상된다 — 다른 회차에서 이미 도매 앱에 등록한 포장을 찍으면 알린다 (막지는 않음)
func TestScanWarnsSerialRegisteredInOtherRound(t *testing.T) {
	s, err := NewTemp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedSample(t, s)
	const code = "900000010"
	raw := "(01)08809000000117(17)290531(10)LOT-1003(21)SN10000009"
	first, err := s.RecordScan(ScanInput{EffectiveDate: "2026-11-01", ProductCode: code, Raw: raw})
	if err != nil || first.Status != "added" {
		t.Fatalf("첫 회차: %+v %v", first, err)
	}
	// 같은 회차에서 다시 찍으면 중복
	if dup, _ := s.RecordScan(ScanInput{EffectiveDate: "2026-11-01", ProductCode: code, Raw: raw}); dup.Status != "duplicate" {
		t.Fatalf("같은 회차 중복: %+v", dup)
	}
	// 등록하기 전에는 다른 회차에서 찍어도 알리지 않는다
	if r, _ := s.RecordScan(ScanInput{EffectiveDate: "2026-12-01", ProductCode: code, Raw: raw}); r.Status != "added" || strings.Contains(r.Message, "이미 도매 앱에 등록") {
		t.Fatalf("등록 전: %+v", r)
	}
	if err := s.SetScanRegistered(first.Unit.ID, true); err != nil {
		t.Fatal(err)
	}
	r, err := s.RecordScan(ScanInput{EffectiveDate: "2027-01-01", ProductCode: code, Raw: raw})
	if err != nil || r.Status != "added" || !strings.Contains(r.Message, "2026-11-01 회차에 이미 도매 앱에 등록") {
		t.Fatalf("다른 회차 등록 포장: %+v %v", r, err)
	}
}
