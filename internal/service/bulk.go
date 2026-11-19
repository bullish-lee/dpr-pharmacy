package service

import (
	"database/sql"
	"strings"
)

// bulkInsert 는 여러 행을 한 INSERT 문에 묶어 넣는다.
// 행마다 문장을 실행하면 SQLite 호출 비용이 행 수만큼 든다 → 500행씩 묶으면 사입 수만 행 저장 시간이 크게 준다.
type bulkInsert struct {
	tx    *sql.Tx
	head  string // 예: "INSERT INTO dispense_line(batch_id, date, product_code, qty) VALUES"
	row   string // 예: "(?, ?, ?, ?)"
	ncol  int
	per   int
	args  []any
	n     int
	stmts map[int]*sql.Stmt
}

func newBulkInsert(tx *sql.Tx, head string, ncol int) *bulkInsert {
	per := 500
	if per*ncol > 30000 { // SQLite 매개변수 한도(32766) 안쪽
		per = 30000 / ncol
	}
	ph := "(" + strings.TrimSuffix(strings.Repeat("?, ", ncol), ", ") + ")"
	return &bulkInsert{tx: tx, head: head, row: ph, ncol: ncol, per: per, args: make([]any, 0, per*ncol), stmts: map[int]*sql.Stmt{}}
}

func (b *bulkInsert) add(vals ...any) error {
	b.args = append(b.args, vals...)
	b.n++
	if b.n == b.per {
		return b.flush()
	}
	return nil
}

func (b *bulkInsert) flush() error {
	if b.n == 0 {
		return nil
	}
	st, ok := b.stmts[b.n]
	if !ok {
		var sb strings.Builder
		sb.WriteString(b.head)
		for i := 0; i < b.n; i++ {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(b.row)
		}
		var err error
		if st, err = b.tx.Prepare(sb.String()); err != nil {
			return err
		}
		b.stmts[b.n] = st
	}
	_, err := st.Exec(b.args...)
	b.args, b.n = b.args[:0], 0
	return err
}

// close 는 남은 행을 넣고 준비한 문장을 닫는다.
func (b *bulkInsert) close() error {
	err := b.flush()
	for _, st := range b.stmts {
		st.Close()
	}
	return err
}

// 큰 일괄 저장 때 보조 색인을 잠시 내렸다가 다시 만든다 (행마다 색인을 고치는 것보다, 다 넣고 한 번에 만드는 편이 빠르다).
// 새로 넣을 행이 남아 있는 행보다 많을 때만 (처음 가져오기·전체 교체). 같은 트랜잭션 안에서 하므로 실패하면 함께 되돌아간다.
var tableIndexes = map[string][]string{
	"purchase_line": {
		"CREATE INDEX ix_purchase_pdq ON purchase_line(product_code, date, qty)",
		"CREATE INDEX ix_purchase_spd ON purchase_line(supplier_id, product_code, date)",
		"CREATE INDEX ix_purchase_date ON purchase_line(date)",
		"CREATE INDEX ix_purchase_price ON purchase_line(product_code, date, id, unit_price) WHERE kind = 'purchase' AND qty > 0 AND unit_price > 0",
	},
	"dispense_line": {
		"CREATE INDEX ix_dispense_pdq ON dispense_line(product_code, date, qty)",
		"CREATE INDEX ix_dispense_date ON dispense_line(date)",
	},
}

func dropIndexesForBulk(tx *sql.Tx, table string, newRows int) (restore func() error, err error) {
	var existing int
	if err := tx.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&existing); err != nil {
		return nil, err
	}
	defs := tableIndexes[table]
	if newRows < 5000 || newRows <= existing || len(defs) == 0 {
		return func() error { return nil }, nil
	}
	for _, d := range defs {
		name := strings.Fields(d)[2]
		if _, err := tx.Exec("DROP INDEX IF EXISTS " + name); err != nil {
			return nil, err
		}
	}
	return func() error {
		for _, d := range defs {
			if _, err := tx.Exec(d); err != nil {
				return err
			}
		}
		return nil
	}, nil
}
