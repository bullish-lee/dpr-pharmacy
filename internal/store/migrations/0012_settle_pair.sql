-- 보상 정산 쌍: 도매가 약가인하 보상(서류·실물)을 장부에서 '옛 가격 반품 → 같은 날 새 가격 재사입'으로 처리한 줄 (협업 약사, 2026-11-25).
-- 실물이 오간 것이 아니므로 서류보상 대상기간 사입량과 재고의 도매 귀속(FIFO) 추정에서 뺀다. 재고 수량은 쌍이 서로 지워져 그대로다.
ALTER TABLE purchase_line ADD COLUMN settle_pair INTEGER NOT NULL DEFAULT 0;
