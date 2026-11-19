-- 화면 속도: 재고 계산(품목별 기준점 이후 사입·조제 합계)이 표 행을 읽지 않고 색인만으로 끝나도록
-- (product_code, date) 색인을 수량까지 담은 색인으로 바꾼다. 실데이터 재고 계산 105ms → 28ms (sqlite3 CLI 기준).
DROP INDEX IF EXISTS ix_purchase_pd;
DROP INDEX IF EXISTS ix_dispense_pd;
CREATE INDEX ix_purchase_pdq ON purchase_line(product_code, date, qty);
CREATE INDEX ix_dispense_pdq ON dispense_line(product_code, date, qty);

-- 품목별 '마지막 사입 단가' 조회용 부분 색인 (사입·양수량·단가 있는 행만). 창 함수 30ms → 7ms (CLI 기준)
CREATE INDEX ix_purchase_price ON purchase_line(product_code, date, id, unit_price) WHERE kind = 'purchase' AND qty > 0 AND unit_price > 0;
