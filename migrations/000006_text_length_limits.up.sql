ALTER TABLE restaurants
    ADD CONSTRAINT restaurants_description_length CHECK (octet_length(description) <= 1000);

ALTER TABLE menu_items
    ADD CONSTRAINT menu_items_description_length CHECK (octet_length(description) <= 1000);

ALTER TABLE orders
    ADD CONSTRAINT orders_comment_length CHECK (octet_length(comment) <= 1000);

ALTER TABLE order_status_history
    ADD CONSTRAINT order_status_history_comment_length CHECK (octet_length(comment) <= 1000);
