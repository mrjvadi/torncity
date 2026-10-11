-- 0149_tier_normalise: the size labels are retired (ADR 0072, plan B8a).
--
-- Promotion village -> town -> city was retired on 2026-10-02 and nothing promotes a settlement, so every founded settlement is
-- 'village'. A settlement that was promoted before that keeps what it had: its grid side (town 9, city 15 against the village's 5)
-- becomes growth steps, its head office takes the one head office code every settlement has, and its label becomes 'village'.
-- A no-op where no such settlement exists. The column stays until the last reader is gone (ADR 0072 step d).
BEGIN;

UPDATE cities
   SET grid_growth = grid_growth + CASE tier WHEN 'town' THEN 9 - 5 ELSE 15 - 5 END
 WHERE origin = 'founded' AND tier IN ('town', 'city');

UPDATE offices o
   SET office_code = 'village_head'
  FROM cities c
 WHERE c.jurisdiction_id = o.jurisdiction_id AND c.origin = 'founded' AND c.tier IN ('town', 'city')
   AND o.office_code IN ('town_head', 'mayor')
   AND NOT EXISTS (SELECT 1 FROM offices v WHERE v.jurisdiction_id = o.jurisdiction_id AND v.office_code = 'village_head' AND v.seat = o.seat);

UPDATE cities SET tier = 'village' WHERE origin = 'founded' AND tier IN ('town', 'city');

COMMIT;
