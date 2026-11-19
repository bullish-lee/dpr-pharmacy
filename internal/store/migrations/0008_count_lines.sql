-- 실사 파일 행 — 초기재고 행(initial_line)과 같은 방식으로 원본 수량·단가를 남기고 고시 단위로 환산한다.
-- 실사 기준점(stock_anchor, source='count') 수량 = 그 기준점에 딸린 행의 고시 단위 수량 합.
-- (손으로 넣은 실계수는 입력할 때 단위를 골라 바로 환산하므로 행이 없다)
CREATE TABLE count_line (
  id             INTEGER PRIMARY KEY,
  anchor_id      INTEGER NOT NULL REFERENCES stock_anchor(id) ON DELETE CASCADE,
  batch_id       INTEGER REFERENCES import_batch(id) ON DELETE CASCADE,
  sheet          TEXT NOT NULL DEFAULT '',
  row            INTEGER NOT NULL DEFAULT 0,
  product_code   TEXT NOT NULL,
  name           TEXT NOT NULL DEFAULT '',
  raw_qty        INTEGER NOT NULL,           -- 원본 수량 (포장 규격 적용 후, 1/1000)
  raw_unit_price REAL,
  unit_label     TEXT NOT NULL DEFAULT '',
  factor         REAL NOT NULL DEFAULT 1,
  qty            INTEGER NOT NULL            -- 고시 단위
);
CREATE INDEX ix_count_line_p ON count_line(product_code);
CREATE INDEX ix_count_line_a ON count_line(anchor_id);
