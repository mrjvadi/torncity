-- A scholar's post may be at a finished great work (the great library) as well as at a building: the post names the work's id, which has no row
-- in settlement_buildings. The foreign key goes; the settlement's research day clears a post whose place is neither a building nor a work.
ALTER TABLE research_posts DROP CONSTRAINT IF EXISTS research_posts_building_id_fkey;
