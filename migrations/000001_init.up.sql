CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS restaurants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL CHECK (length(trim(name)) > 0),
    description TEXT NOT NULL DEFAULT '',
    address TEXT NOT NULL CHECK (length(trim(address)) > 0),
    is_active BOOLEAN NOT NULL DEFAULT true,
    webhook_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_restaurants_active ON restaurants (created_at DESC) WHERE is_active = true;

CREATE TABLE IF NOT EXISTS menu_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL CHECK (length(trim(name)) > 0),
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_menu_categories_restaurant_id_id UNIQUE (restaurant_id, id),
    CONSTRAINT uq_menu_categories_restaurant_name UNIQUE (restaurant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_menu_categories_restaurant_id ON menu_categories (restaurant_id, sort_order);

CREATE TABLE IF NOT EXISTS menu_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
    category_id UUID NOT NULL,
    name VARCHAR(255) NOT NULL CHECK (length(trim(name)) > 0),
    description TEXT NOT NULL DEFAULT '',
    price_cents BIGINT NOT NULL CHECK (price_cents >= 0),
    stock_quantity INT NOT NULL DEFAULT 0 CHECK (stock_quantity >= 0),
    is_available BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT fk_menu_items_restaurant_category 
        FOREIGN KEY (restaurant_id, category_id) 
        REFERENCES menu_categories(restaurant_id, id) 
        ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_menu_items_restaurant_category ON menu_items (restaurant_id, category_id);
CREATE INDEX IF NOT EXISTS idx_menu_items_available ON menu_items (restaurant_id, is_available);

CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE RESTRICT,
    status VARCHAR(50) NOT NULL CHECK (status IN ('created', 'accepted', 'cooking', 'ready', 'delivering', 'completed', 'cancelled')),
    total_price_cents BIGINT NOT NULL CHECK (total_price_cents >= 0),
    delivery_address TEXT NOT NULL CHECK (length(trim(delivery_address)) > 0),
    comment TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_user_id ON orders (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_restaurant_status ON orders (restaurant_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS order_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    menu_item_id UUID NOT NULL REFERENCES menu_items(id) ON DELETE RESTRICT,
    item_name VARCHAR(255) NOT NULL,
    price_cents BIGINT NOT NULL CHECK (price_cents >= 0),
    quantity INT NOT NULL CHECK (quantity > 0),
    CONSTRAINT uq_order_items_order_item UNIQUE (order_id, menu_item_id)
);

CREATE INDEX IF NOT EXISTS idx_order_items_order_id ON order_items (order_id);
CREATE INDEX IF NOT EXISTS idx_order_items_menu_item_id ON order_items (menu_item_id);

CREATE TABLE IF NOT EXISTS order_status_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    from_status VARCHAR(50) NOT NULL,
    to_status VARCHAR(50) NOT NULL,
    comment TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_status_history_order_id ON order_status_history (order_id, created_at ASC);