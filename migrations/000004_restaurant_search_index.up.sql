CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_restaurants_name_trgm
    ON restaurants USING gin (name gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_restaurants_description_trgm
    ON restaurants USING gin (description gin_trgm_ops);
