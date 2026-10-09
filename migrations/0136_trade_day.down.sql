BEGIN;

UPDATE charter_offices
   SET grants = COALESCE((SELECT jsonb_agg(g) FROM jsonb_array_elements(grants) g WHERE g->>'p' <> 'trade.export'), '[]'::jsonb)
 WHERE grants @> '[{"p": "trade.export"}]'::jsonb;

DROP TABLE trade_day_lines;
DROP TABLE trade_days;
DROP TABLE trade_orders;

COMMIT;
