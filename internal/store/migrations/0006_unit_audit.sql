-- 조제 단위 확인: 이팜 조제현황의 '단위·단위수량'(청구 단위)을 품목별로 기록해 고시 규격과 대조한다.
ALTER TABLE product ADD COLUMN disp_unit TEXT NOT NULL DEFAULT '';  -- 조제 단위 (예: '정/팩')
ALTER TABLE product ADD COLUMN disp_size REAL;                      -- 조제 단위수량 (예: 28) — 고시 규격의 가격 단위 크기와 같아야 한다
