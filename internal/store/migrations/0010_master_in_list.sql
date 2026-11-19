-- 약제급여목록: 새 목록에서 빠졌지만 약국 기록(사입·조제·재고)이 있어 남겨 둔 품목을 표시한다 (in_list = 0).
-- 빠진 품목을 지우면 단위 환산 기준이 사라져 재고 수량이 바뀌므로 규격·단위는 남기되, '지금 급여목록에 있음'으로는 보지 않는다.
-- (batch_id 로 가리면 가져오기 기록을 지웠을 때 NULL 이 되어 구분할 수 없다)
ALTER TABLE drug_master ADD COLUMN in_list INTEGER NOT NULL DEFAULT 1;
