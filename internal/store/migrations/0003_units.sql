-- 단위 체계: 모든 수량을 '고시 단위'(급여 상한금액의 단위 = 이팜 조제 총수량 단위)로 통일한다.
-- 사입·초기재고는 원본(raw)을 보관하고, 배율(factor: 원본 1단위 = 고시 단위 N개)을 곱한 값을 qty 에 둔다.
-- 배율은 급여목록·고시·사용자 지정이 바뀌면 다시 계산한다 (가져오기 순서와 무관).

-- 심평원 약제급여목록표 (월별 전체 목록, 공개용)
CREATE TABLE drug_master (
  code            TEXT PRIMARY KEY,          -- 제품코드 9자리
  name            TEXT NOT NULL DEFAULT '',
  company         TEXT NOT NULL DEFAULT '',
  spec            TEXT NOT NULL DEFAULT '',  -- 규격 (예: '1', '28', '100(1)')
  unit            TEXT NOT NULL DEFAULT '',  -- 단위 (예: '정', '정/팩', 'g/통')
  price           REAL,                      -- 상한금액
  route           TEXT NOT NULL DEFAULT '',  -- 투여 (내복·외용·주사)
  ingredient_code TEXT NOT NULL DEFAULT '',
  otc             TEXT NOT NULL DEFAULT '',  -- 전문 | 일반
  note            TEXT NOT NULL DEFAULT '',  -- 비고 (집행정지·약가유연계약·등재 …)
  list_date       TEXT NOT NULL DEFAULT '',  -- 목록 적용일
  batch_id        INTEGER REFERENCES import_batch(id) ON DELETE SET NULL
);
CREATE INDEX ix_master_name ON drug_master(name);

ALTER TABLE purchase_line ADD COLUMN raw_qty INTEGER;                       -- 원본 수량 (수량×단위수량, 1/1000)
ALTER TABLE purchase_line ADD COLUMN raw_unit_price REAL;                   -- 원본 1단위 단가 (단가÷단위수량)
ALTER TABLE purchase_line ADD COLUMN unit_label TEXT NOT NULL DEFAULT '';   -- 이팜 '단위'
ALTER TABLE purchase_line ADD COLUMN factor REAL NOT NULL DEFAULT 1;        -- 원본 1단위 = 고시 단위 factor 개
UPDATE purchase_line SET raw_qty = qty, raw_unit_price = unit_price;

-- 초기재고 원본 행 (미확인 행을 지정하면 여기에 추가된다). stock_anchor(initial).qty = 이 표의 qty 합
CREATE TABLE initial_line (
  id             INTEGER PRIMARY KEY,
  batch_id       INTEGER NOT NULL REFERENCES import_batch(id) ON DELETE CASCADE,
  sheet          TEXT NOT NULL DEFAULT '',
  row            INTEGER NOT NULL DEFAULT 0,
  product_code   TEXT NOT NULL,
  name           TEXT NOT NULL DEFAULT '',
  raw_qty        INTEGER NOT NULL,           -- 원본 수량 (포장 규격 적용 후, 1/1000)
  raw_unit_price REAL,
  unit_label     TEXT NOT NULL DEFAULT '',
  factor         REAL NOT NULL DEFAULT 1,
  qty            INTEGER NOT NULL,           -- 고시 단위
  unresolved_id  INTEGER,                    -- 미확인 행에서 지정한 경우
  matched_by     TEXT NOT NULL DEFAULT 'code' -- code(시트의 코드) | name(이름 자동 매칭) | user(사용자 지정)
);
CREATE INDEX ix_initial_line_p ON initial_line(product_code);

ALTER TABLE product ADD COLUMN base_unit TEXT NOT NULL DEFAULT '';    -- 고시 단위 짧은 표시 (수량 뒤: '팩', 'g', '정', '관')
ALTER TABLE product ADD COLUMN base_label TEXT NOT NULL DEFAULT '';   -- 고시 단위 설명 (예: '팩(28정)', 'g (1통 = 100g)')
ALTER TABLE product ADD COLUMN unit_status TEXT NOT NULL DEFAULT '';  -- ok | auto_unit | auto_pack | mixed | check | no_reference | manual
ALTER TABLE product ADD COLUMN unit_note TEXT NOT NULL DEFAULT '';    -- 환산 근거
ALTER TABLE product ADD COLUMN canon_pack REAL;                       -- 사입 포장 1개 = 고시 단위 N개 (자동, 바코드 기본 수량)
ALTER TABLE product ADD COLUMN pack_manual REAL;                      -- 사용자 지정: 바코드 1개 = 고시 단위 N개
