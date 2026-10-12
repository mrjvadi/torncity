DELETE FROM research_posts WHERE building_id NOT IN (SELECT id FROM settlement_buildings);
ALTER TABLE research_posts ADD CONSTRAINT research_posts_building_id_fkey FOREIGN KEY (building_id) REFERENCES settlement_buildings (id);
