-- DPR 초기 스키마
-- 규칙: 날짜는 'YYYY-MM-DD' TEXT, 시각은 RFC3339 TEXT, 수량은 1/1000 단위 INTEGER(qty), 금액·단가는 REAL(원)

CREATE TABLE app_setting (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- 약품 마스터 (사입·조제·재고·고시에서 자동 수집)
CREATE TABLE product (
  code        TEXT PRIMARY KEY,           -- 9자리 EDI 또는 이팜 내부코드
  name        TEXT NOT NULL DEFAULT '',
  company     TEXT NOT NULL DEFAULT '',
  stock_unit  TEXT NOT NULL DEFAULT '',
  pack_size   REAL,                       -- 대표 포장 단위수량 (바코드 1개 = 몇 낱개)
  unit_factor REAL,                       -- 사용자 지정: 재고 1단위 = 고시 단위 N개 (NULL = 자동)
  memo        TEXT NOT NULL DEFAULT '',
  updated_at  TEXT NOT NULL
);
CREATE INDEX ix_product_name ON product(name);

-- 거래처(도매처). 이름은 정규화 이름, 원본 표기는 supplier_alias 로 연결
CREATE TABLE supplier (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  kind       TEXT NOT NULL DEFAULT 'wholesaler',  -- wholesaler | other
  active     INTEGER NOT NULL DEFAULT 1,
  deleted    INTEGER NOT NULL DEFAULT 0,          -- 사용 이력이 있어 숨김 처리된 경우
  contact    TEXT NOT NULL DEFAULT '',
  memo       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE supplier_alias (
  alias       TEXT PRIMARY KEY,                    -- NormalizeSupplierName(원본)
  supplier_id INTEGER NOT NULL REFERENCES supplier(id) ON DELETE CASCADE
);

-- 도매별 보상 기준 (버전: valid_from). supplier_id NULL = 기본 규칙
CREATE TABLE comp_rule (
  id               INTEGER PRIMARY KEY,
  supplier_id      INTEGER REFERENCES supplier(id) ON DELETE CASCADE,
  valid_from       TEXT NOT NULL DEFAULT '2000-01-01',
  rate_bp          INTEGER NOT NULL,
  window_mode      TEXT NOT NULL DEFAULT 'cycle',
  lookback         INTEGER NOT NULL,
  cutoff_day       INTEGER NOT NULL DEFAULT 0,
  extend_to_anchor INTEGER NOT NULL DEFAULT 0,
  anchor           TEXT NOT NULL DEFAULT 'effective_date',
  rounding         TEXT NOT NULL DEFAULT 'floor',
  net_returns      INTEGER NOT NULL DEFAULT 1,
  settlement_day   INTEGER NOT NULL DEFAULT 0,
  needs_review     INTEGER NOT NULL DEFAULT 1,     -- 실제 기준 확인 전(의사결정 필요)
  memo             TEXT NOT NULL DEFAULT '',
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
);
CREATE UNIQUE INDEX ux_rule_supplier_from ON comp_rule(IFNULL(supplier_id, 0), valid_from);

-- 가져오기 이력
CREATE TABLE import_batch (
  id         INTEGER PRIMARY KEY,
  kind       TEXT NOT NULL,       -- purchase | dispense | initial_stock | stock_count | lot | notice
  filename   TEXT NOT NULL,
  sha256     TEXT NOT NULL,
  range_from TEXT,
  range_to   TEXT,
  row_count  INTEGER NOT NULL DEFAULT 0,
  summary    TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

-- 사용자가 지정한 열 매핑 (같은 양식이면 자동 적용)
CREATE TABLE import_profile (
  id         INTEGER PRIMARY KEY,
  kind       TEXT NOT NULL,
  signature  TEXT NOT NULL,
  name       TEXT NOT NULL DEFAULT '',
  header_row INTEGER NOT NULL,
  mapping    TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(kind, signature)
);

CREATE TABLE purchase_line (
  id           INTEGER PRIMARY KEY,
  batch_id     INTEGER NOT NULL REFERENCES import_batch(id) ON DELETE CASCADE,
  date         TEXT NOT NULL,
  product_code TEXT NOT NULL,
  supplier_id  INTEGER REFERENCES supplier(id),
  kind         TEXT NOT NULL,     -- purchase | return | disposal | adjust | borrow_in | borrow_out | other
  pack_size    REAL,
  pack_qty     REAL,
  qty          INTEGER NOT NULL,  -- 낱개(1/1000), 부호 포함
  unit_price   REAL,
  amount       REAL,
  lot          TEXT NOT NULL DEFAULT '',
  expiry       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ix_purchase_pd  ON purchase_line(product_code, date);
CREATE INDEX ix_purchase_spd ON purchase_line(supplier_id, product_code, date);
CREATE INDEX ix_purchase_date ON purchase_line(date);

CREATE TABLE dispense_line (
  id           INTEGER PRIMARY KEY,
  batch_id     INTEGER NOT NULL REFERENCES import_batch(id) ON DELETE CASCADE,
  date         TEXT NOT NULL,
  product_code TEXT NOT NULL,
  qty          INTEGER NOT NULL
);
CREATE INDEX ix_dispense_pd ON dispense_line(product_code, date);
CREATE INDEX ix_dispense_date ON dispense_line(date);

-- 재고 기준점: 초기재고(initial) / 실계수(count). 최신 기준점 + 이후 사입 − 조제 = 추정재고
CREATE TABLE stock_anchor (
  id           INTEGER PRIMARY KEY,
  product_code TEXT NOT NULL,
  as_of        TEXT NOT NULL,
  timing       TEXT NOT NULL DEFAULT 'start', -- start: 그날 거래 반영(영업 전) | end: 그날 거래 미반영(영업 후)
  qty          INTEGER NOT NULL,
  source       TEXT NOT NULL,                 -- initial | count
  unit_price   REAL,
  batch_id     INTEGER REFERENCES import_batch(id) ON DELETE CASCADE,
  note         TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL
);
CREATE INDEX ix_anchor_p ON stock_anchor(product_code, as_of);

-- 초기재고 중 약품코드를 확정하지 못한 행 (사용자가 지정)
CREATE TABLE initial_unresolved (
  id            INTEGER PRIMARY KEY,
  batch_id      INTEGER NOT NULL REFERENCES import_batch(id) ON DELETE CASCADE,
  sheet         TEXT NOT NULL,
  row           INTEGER NOT NULL,
  name          TEXT NOT NULL,
  qty           INTEGER NOT NULL,
  unit_price    REAL,
  spec          TEXT NOT NULL DEFAULT '',
  candidates    TEXT NOT NULL DEFAULT '[]',
  resolved_code TEXT NOT NULL DEFAULT '',
  ignored       INTEGER NOT NULL DEFAULT 0
);

-- 고시 (복지부 수집 또는 수동 업로드)
CREATE TABLE notice (
  id           INTEGER PRIMARY KEY,
  source       TEXT NOT NULL,      -- mohw | upload
  list_no      TEXT UNIQUE,
  notice_no    TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL DEFAULT '',
  url          TEXT NOT NULL DEFAULT '',
  published_at TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL,      -- detected | parsed | failed | no_attachment
  error        TEXT NOT NULL DEFAULT '',
  file_name    TEXT NOT NULL DEFAULT '',
  file_sha256  TEXT NOT NULL DEFAULT '',
  file_path    TEXT NOT NULL DEFAULT '',
  summary      TEXT NOT NULL DEFAULT '{}',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX ux_notice_sha ON notice(file_sha256) WHERE file_sha256 <> '';

CREATE TABLE price_change (
  id              INTEGER PRIMARY KEY,
  notice_id       INTEGER NOT NULL REFERENCES notice(id) ON DELETE CASCADE,
  product_code    TEXT NOT NULL,
  product_name    TEXT NOT NULL DEFAULT '',
  company         TEXT NOT NULL DEFAULT '',
  spec            TEXT NOT NULL DEFAULT '',
  unit            TEXT NOT NULL DEFAULT '',
  ingredient_code TEXT NOT NULL DEFAULT '',
  ingredient      TEXT NOT NULL DEFAULT '',
  category        TEXT NOT NULL DEFAULT '',
  route           TEXT NOT NULL DEFAULT '',
  kind            TEXT NOT NULL,   -- new | change | delete
  detail          TEXT NOT NULL,   -- price_down | price_up | same | agreed_price | name_change | unknown | other | new | delete
  price_before    REAL,
  price_after     REAL,
  before_source   TEXT NOT NULL DEFAULT '', -- compare | history | purchase | ''
  effective_date  TEXT NOT NULL DEFAULT '',
  grace_until     TEXT NOT NULL DEFAULT '',
  flexible        INTEGER NOT NULL DEFAULT 0,
  sheet           TEXT NOT NULL DEFAULT '',
  note            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ix_pc_eff  ON price_change(effective_date, detail);
CREATE INDEX ix_pc_code ON price_change(product_code);

-- 제조번호·유효기간 사입 근거 (이팜 제조번호 포함 내보내기, 사진 판독, 수기)
CREATE TABLE lot_evidence (
  id           INTEGER PRIMARY KEY,
  product_code TEXT NOT NULL,
  supplier_id  INTEGER NOT NULL REFERENCES supplier(id),
  date         TEXT NOT NULL DEFAULT '',
  qty          INTEGER NOT NULL DEFAULT 0,
  lot          TEXT NOT NULL,
  expiry       TEXT NOT NULL DEFAULT '',
  source       TEXT NOT NULL DEFAULT 'manual',
  batch_id     INTEGER REFERENCES import_batch(id) ON DELETE CASCADE,
  created_at   TEXT NOT NULL
);
CREATE INDEX ix_lot_p ON lot_evidence(product_code);

-- 바코드 GTIN ↔ 약품코드 (사용자 확인 시 학습)
CREATE TABLE gtin_map (
  gtin         TEXT PRIMARY KEY,
  product_code TEXT NOT NULL,
  created_at   TEXT NOT NULL
);

-- 바코드로 확인한 보유 실물 (시행일 × 약품 단위 작업)
CREATE TABLE scan_unit (
  id             INTEGER PRIMARY KEY,
  effective_date TEXT NOT NULL,
  product_code   TEXT NOT NULL,
  gtin           TEXT NOT NULL DEFAULT '',
  lot            TEXT NOT NULL DEFAULT '',
  expiry         TEXT NOT NULL DEFAULT '',
  serial         TEXT NOT NULL DEFAULT '',
  raw            TEXT NOT NULL DEFAULT '',
  qty            INTEGER NOT NULL,                 -- 포장 1개의 낱개 수량(1/1000)
  match_status   TEXT NOT NULL,                    -- unique | ambiguous | none
  candidates     TEXT NOT NULL DEFAULT '[]',
  supplier_id    INTEGER REFERENCES supplier(id),  -- 확정 도매
  resolved_by    TEXT NOT NULL DEFAULT '',         -- auto | user
  registered     INTEGER NOT NULL DEFAULT 0,       -- 도매 앱 등록(스캔) 완료
  created_at     TEXT NOT NULL
);
CREATE UNIQUE INDEX ux_scan_serial ON scan_unit(effective_date, gtin, serial) WHERE serial <> '';
CREATE INDEX ix_scan_ep ON scan_unit(effective_date, product_code);

-- 회차별 대상기간 직접 지정 (도매별 예외 상황 대응)
CREATE TABLE window_override (
  effective_date TEXT NOT NULL,
  supplier_id    INTEGER NOT NULL REFERENCES supplier(id) ON DELETE CASCADE,
  window_from    TEXT NOT NULL,
  window_to      TEXT NOT NULL,
  memo           TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (effective_date, supplier_id)
);

-- 처리 결과 (약품 × 도매 × 시행일)
CREATE TABLE settlement (
  id             INTEGER PRIMARY KEY,
  effective_date TEXT NOT NULL,
  product_code   TEXT NOT NULL,
  supplier_id    INTEGER NOT NULL REFERENCES supplier(id),
  method         TEXT NOT NULL,                  -- document | physical | none
  qty            INTEGER NOT NULL,
  unit_delta     REAL NOT NULL,
  amount         REAL NOT NULL,
  status         TEXT NOT NULL DEFAULT 'planned', -- planned | done
  memo           TEXT NOT NULL DEFAULT '',
  rule_snapshot  TEXT NOT NULL DEFAULT '{}',
  done_at        TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL,
  updated_at     TEXT NOT NULL,
  UNIQUE(effective_date, product_code, supplier_id)
);

CREATE TABLE job_run (
  id          INTEGER PRIMARY KEY,
  job         TEXT NOT NULL,
  started_at  TEXT NOT NULL,
  finished_at TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL,                     -- running | ok | error
  message     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE notification (
  id         INTEGER PRIMARY KEY,
  created_at TEXT NOT NULL,
  level      TEXT NOT NULL DEFAULT 'info',       -- info | warn | error
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  link       TEXT NOT NULL DEFAULT '',
  read_at    TEXT NOT NULL DEFAULT ''
);
