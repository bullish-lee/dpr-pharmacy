-- 수정 기록: 사용자가 재고·단위·초기재고·기준일·보상 기간 등을 고친 내역 (무엇을, 언제, 이전 → 이후, 메모, 어느 화면에서)
CREATE TABLE change_log (
  id           INTEGER PRIMARY KEY,
  at           TEXT NOT NULL,
  kind         TEXT NOT NULL,             -- count | count_delete | unit_factor | pack_size | initial_resolve | initial_ignore | baseline | ack | unack | window_override | window_clear
  product_code TEXT NOT NULL DEFAULT '',
  product_name TEXT NOT NULL DEFAULT '',
  old_value    TEXT NOT NULL DEFAULT '',
  new_value    TEXT NOT NULL DEFAULT '',
  note         TEXT NOT NULL DEFAULT '',
  source       TEXT NOT NULL DEFAULT ''   -- 화면 (재고, 손실 순위, 데이터 점검, 품목 판정 …)
);
CREATE INDEX ix_change_log_at ON change_log(at);
CREATE INDEX ix_change_log_p ON change_log(product_code, at);

-- 데이터 점검에서 '확인함(이상 없음)' 으로 표시한 항목. 상태(state)가 바뀌면 다시 보여준다.
CREATE TABLE review_ack (
  product_code TEXT NOT NULL,
  flag         TEXT NOT NULL,             -- negative | unit_check | overstock | unit_auto
  state        TEXT NOT NULL DEFAULT '',  -- 확인 당시 상태 (재고 수량·단위 상태)
  note         TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  PRIMARY KEY (product_code, flag)
);
