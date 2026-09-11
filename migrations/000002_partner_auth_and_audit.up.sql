ALTER TABLE restaurants ADD COLUMN IF NOT EXISTS api_key TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS uq_restaurants_api_key
    ON restaurants (api_key) WHERE api_key <> '';

ALTER TABLE order_status_history
    ADD COLUMN IF NOT EXISTS actor VARCHAR(32) NOT NULL DEFAULT 'system'
    CHECK (actor IN ('customer', 'restaurant', 'system'));
