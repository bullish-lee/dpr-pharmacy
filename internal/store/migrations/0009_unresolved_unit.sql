-- 초기재고 미확인 행에도 원본 단위(재고단위)를 남긴다 — 나중에 약품을 지정할 때 그 단위로 고시 단위 환산을 하기 위해
-- (없으면 단가 없는 행이 '같은 단위 사입 판단'을 따르지 못해, 먼저 맞춰진 행과 나중에 지정한 행의 결과가 달라진다)
ALTER TABLE initial_unresolved ADD COLUMN unit_label TEXT NOT NULL DEFAULT '';
