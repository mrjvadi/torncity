BEGIN;
UPDATE charter_offices
   SET grants = COALESCE((SELECT jsonb_agg(g) FROM jsonb_array_elements(grants) g WHERE g->>'p' NOT IN ('mine.run', 'rail.draw', 'haul.order')), '[]'::jsonb)
 WHERE grants @> '[{"p": "mine.run"}]'::jsonb OR grants @> '[{"p": "rail.draw"}]'::jsonb OR grants @> '[{"p": "haul.order"}]'::jsonb;
DROP TABLE node_stats;
DROP TRIGGER rail_reservations_loop_only ON rail_reservations;
DROP FUNCTION rail_reservations_loop_only();
DROP TABLE rail_reservations;
DROP TABLE rail_schedules;
DROP TABLE rail_trains;
DROP TABLE rail_stops;
DROP TABLE rail_segments;
DROP TABLE rail_lines;
DROP TABLE haul_loads;
DROP TABLE haul_orders;
DROP TABLE road_links;
DROP TABLE carriers;
DROP TABLE build_works;
DROP TABLE mine_levels;
DROP TABLE mine_state;
DROP INDEX settlement_shifts_role_idx;
ALTER TABLE settlement_shifts DROP COLUMN deposit_reserve, DROP COLUMN deposit_depth, DROP COLUMN deposit_id, DROP COLUMN mine_limit, DROP COLUMN staff_role;
DROP TABLE ore_extractions;
DROP TABLE ore_deposit_surveys;
DROP TABLE ore_deposits;
COMMIT;
