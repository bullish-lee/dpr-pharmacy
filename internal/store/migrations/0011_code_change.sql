-- 제품코드 변경 기록 (약가기준정보의 '삭제' 행 중 변경이후약품코드가 있는 것).
-- merged : 옛 코드의 약국 기록(사입·조제·재고·바코드)을 새 코드로 옮김. 뒤에 옛 코드로 가져온 자료도 새 코드로 옮긴다.
-- review : 옛 코드와 새 코드의 고시 단위(규격·단위)가 달라 옮기지 않음 — 옮기면 수량 환산이 틀어진다. 사용자가 확인.
CREATE TABLE code_change (
  old_code   TEXT PRIMARY KEY,
  new_code   TEXT NOT NULL,
  changed_on TEXT NOT NULL DEFAULT '',   -- 옛 코드가 삭제된 적용일
  status     TEXT NOT NULL,              -- merged | review
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
CREATE INDEX ix_code_change_new ON code_change(new_code);
