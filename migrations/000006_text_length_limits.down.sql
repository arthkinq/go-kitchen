ALTER TABLE order_status_history DROP CONSTRAINT IF EXISTS order_status_history_comment_length;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_comment_length;
ALTER TABLE menu_items DROP CONSTRAINT IF EXISTS menu_items_description_length;
ALTER TABLE restaurants DROP CONSTRAINT IF EXISTS restaurants_description_length;
