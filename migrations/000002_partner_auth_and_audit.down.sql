DROP INDEX IF EXISTS uq_restaurants_api_key;
ALTER TABLE restaurants DROP COLUMN IF EXISTS api_key;
ALTER TABLE order_status_history DROP COLUMN IF EXISTS actor;
