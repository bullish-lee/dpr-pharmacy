-- 심평원 '약가마스터_의약품표준코드' 등 공식 GTIN↔보험코드 매핑 지원
ALTER TABLE gtin_map ADD COLUMN source TEXT NOT NULL DEFAULT 'user';   -- official | user | heuristic
ALTER TABLE gtin_map ADD COLUMN pack_qty REAL;                         -- 제품총수량 (포장 1개 = 낱개 수)
ALTER TABLE gtin_map ADD COLUMN name TEXT NOT NULL DEFAULT '';
