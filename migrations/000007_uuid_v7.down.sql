ALTER TABLE order_status_history ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE order_items ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE orders ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE menu_items ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE menu_categories ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE restaurants ALTER COLUMN id SET DEFAULT gen_random_uuid();
