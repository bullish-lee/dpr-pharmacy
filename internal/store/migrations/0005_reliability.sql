-- 수집 안정성: 계속 실패하는 고시는 간격을 늘려 다시 시도하고(1→3→6→12→24시간), 알림은 처음·세 번째에만 한다.
ALTER TABLE notice ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notice ADD COLUMN next_try TEXT NOT NULL DEFAULT '';
