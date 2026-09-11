CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_menu_items_name_trgm
    ON menu_items USING gin (name gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_menu_items_description_trgm
    ON menu_items USING gin (description gin_trgm_ops);
