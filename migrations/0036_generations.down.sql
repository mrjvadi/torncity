-- 0036_generations, reversed.

BEGIN;

DROP TABLE retrofit_jobs;

ALTER TABLE production_orders DROP CONSTRAINT production_orders_target_check;
ALTER TABLE production_orders DROP COLUMN target_design_id;
ALTER TABLE production_orders DROP CONSTRAINT production_orders_kind_check;
ALTER TABLE production_orders ADD CONSTRAINT production_orders_kind_check CHECK (kind IN ('design', 'component'));

DROP TABLE design_improvement_projects;

ALTER TABLE product_designs DROP CONSTRAINT product_designs_lineage_head_check;
ALTER TABLE product_designs DROP CONSTRAINT product_designs_parent_check;
ALTER TABLE product_designs DROP CONSTRAINT product_designs_version_check;
DROP INDEX product_designs_lineage_idx;
ALTER TABLE product_designs DROP COLUMN improvements;
ALTER TABLE product_designs DROP COLUMN parent_design_id;
ALTER TABLE product_designs DROP COLUMN version;
ALTER TABLE product_designs DROP COLUMN lineage_id;

COMMIT;
