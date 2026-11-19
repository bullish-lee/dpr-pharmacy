// Package domain 은 약가인하 대응의 순수 계산 로직이다.
// DB·파일·HTTP·UI 를 import 하지 않으며, 모든 함수는 입력만으로 결과가 결정된다.
package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Qty 는 재고·사입·조제 수량을 1/1000 단위 정수로 표현한다.
//
// 반쪽 정제(0.5T), 1/4 정제(0.25T), 시럽 mL 등 소수 수량이 실제로 존재한다.
// float 로 0.3 을 곱하면 90×0.3=26.999… 이 되어 버림 시 1알이 사라지므로
// 보상 수량 계산은 반드시 정수(Qty) 연산으로 한다.
type Qty int64

const QtyScale = 1000

// QtyFromFloat 은 엑셀에서 읽은 수량(float)을 Qty 로 변환한다 (반올림).
func QtyFromFloat(f float64) Qty {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return Qty(math.Round(f * QtyScale))
}

// Units 는 정수 단위 수량으로 Qty 를 만든다.
func Units(n int64) Qty { return Qty(n * QtyScale) }

// Float 은 표시·금액 계산용 실수 값.
func (q Qty) Float() float64 { return float64(q) / QtyScale }

// String 은 소수점 이하 불필요한 0 을 제거한 표기 ("210", "47.5").
func (q Qty) String() string {
	s := strconv.FormatFloat(q.Float(), 'f', 3, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" {
		return "0"
	}
	return s
}

// Mul 은 수량 × 실수 (포장수량 × 포장개수 등).
func (q Qty) MulFloat(f float64) Qty { return Qty(math.Round(float64(q) * f)) }

// MinQty / MaxQty 헬퍼
func MinQty(a, b Qty) Qty {
	if a < b {
		return a
	}
	return b
}

func MaxQty(a, b Qty) Qty {
	if a > b {
		return a
	}
	return b
}

// RoundWon 은 금액을 원 단위로 반올림한다.
func RoundWon(f float64) int64 { return int64(math.Round(f)) }

// RateBP 는 비율을 베이시스 포인트(1/10000)로 표현한다. 3000 = 30%.
type RateBP int64

func (r RateBP) Percent() float64 { return float64(r) / 100 }

func (r RateBP) String() string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", r.Percent()), "0"), ".") + "%"
}

// MarshalJSON : 화면/API 에는 낱개 단위 숫자(210, 47.5)로 내보낸다 (내부 1/1000 표현을 숨김).
func (q Qty) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(q.Float(), 'f', -1, 64)), nil
}

// UnmarshalJSON : 낱개 단위 숫자를 받아 Qty 로 바꾼다.
func (q *Qty) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*q = QtyFromFloat(f)
	return nil
}
