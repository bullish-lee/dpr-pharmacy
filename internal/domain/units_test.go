package domain

import (
	"math"
	"testing"
)

func TestParsePriceUnit(t *testing.T) {
	cases := []struct {
		spec, unit, label string
		size, perCont     float64
	}{
		{"1", "정", "정", 1, 1},
		{"28", "정/팩", "팩(28정)", 28, 1},
		{"0.4", "mL/관", "관(0.4mL)", 0.4, 1},
		{"100(1)", "g/통", "g", 1, 100},
		{"95(1)", "mL/병", "mL", 1, 95},
		{"5", "mL/병(PP)", "병(5mL)", 5, 1},
		{"60", "회/통", "통(60회)", 60, 1},
	}
	for _, c := range cases {
		pu := ParsePriceUnit(c.spec, c.unit)
		if !pu.Known || pu.Label != c.label || pu.Size != c.size || pu.PerCont != c.perCont {
			t.Errorf("%s %s → %+v", c.spec, c.unit, pu)
		}
	}
	if n := ParsePriceUnit("100(1)", "g/통").ContainerNote(); n != "1통 = 100g" {
		t.Errorf("note %q", n)
	}
}

func TestPerBarcode(t *testing.T) {
	cases := []struct {
		spec, unit string
		pack, want float64
	}{
		{"28", "정/팩", 28, 1},       // 28정 PTP 1박스 = 1팩
		{"60", "캡슐/팩", 60, 1},      // 흡입용 캡슐 60캡슐 = 1팩
		{"100(1)", "g/통", 1, 100},  // 100g 1병 = 100g
		{"10(1)", "mL/포", 50, 500}, // 10mL 50포 = 500mL
		{"0.4", "mL/관", 60, 60},    // 1회용 점안 60관
		{"3", "mL/펜", 5, 5},        // 펜 5개
		{"1", "정", 100, 100},
	}
	for _, c := range cases {
		if got := ParsePriceUnit(c.spec, c.unit).PerBarcode(c.pack); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s %s pack %v → %v, want %v", c.spec, c.unit, c.pack, got, c.want)
		}
	}
}

func TestDecideFactor(t *testing.T) {
	cases := []struct {
		name       string
		spec, unit string
		ratio      float64
		packs      []float64
		factor     float64
		status     string
	}{
		{"같은 단위(정)", "1", "정", 0.98, nil, 1, "ok"},
		{"인하 직전 매입가(1.4배)", "1", "정", 1.41, nil, 1, "ok"},
		{"팩 단위 고시, 사입은 정", "28", "정/팩", 241.93 / 6774, nil, 1.0 / 28, "auto_unit"},
		{"흡입용 캡슐 60캡슐/팩", "60", "캡슐/팩", 414.75 / 24885, nil, 1.0 / 60, "auto_unit"},
		{"g 단위 고시, 사입은 통", "100(1)", "g/통", 100, nil, 100, "auto_unit"},
		{"mL 단위 고시, 사입은 포", "10(1)", "mL/포", 10, nil, 10, "auto_unit"},
		{"10g 연고, 사입은 g", "10", "g/개", 0.1, nil, 0.1, "auto_unit"},
		{"펜(3mL), 사입은 mL", "3", "mL/펜", 37349.0 / 103406, nil, 1.0 / 3, "auto_unit"},
		{"이팜 단위수량 오기(100정 1통을 1로)", "1", "정", 100, nil, 100, "auto_pack"},
		{"작은 정수배는 근거가 있어야", "1", "정", 4.008, nil, 1, "check"},
		{"작은 정수배 + 알려진 포장", "1", "매", 5.0, []float64{5}, 5, "auto_pack"},
		{"설명 안 되는 비율(유연계약 등)", "1", "정", 0.36, nil, 1, "check"},
		{"참조가 없음", "1", "정", 0, nil, 1, "no_reference"},
	}
	for _, c := range cases {
		d := DecideFactor(c.ratio, ParsePriceUnit(c.spec, c.unit), c.packs, "")
		if math.Abs(d.Factor-c.factor) > 1e-9 || d.Status != c.status {
			t.Errorf("%s: %+v (want ×%v %s)", c.name, d, c.factor, c.status)
		}
	}
}

func TestMedian(t *testing.T) {
	if Median([]float64{3, 1, 2}) != 2 || Median([]float64{4, 1, 3, 2}) != 2.5 || Median(nil) != 0 {
		t.Fatal("median")
	}
}
