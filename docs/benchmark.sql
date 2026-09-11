
\timing on

CREATE OR REPLACE FUNCTION bench_uuid_v7(ms bigint, tail bigint) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$
    SELECT (lpad(to_hex(ms), 12, '0') || '7' || lpad(to_hex(tail % 4096), 3, '0')
            || '8' || lpad(to_hex((tail / 4096) % 4096), 3, '0')
            || lpad(to_hex(tail), 12, '0'))::uuid;
$$;


INSERT INTO restaurants (id, name, description, address, is_active, created_at)
SELECT
    bench_uuid_v7(1735689600000 + i / 10, i),
    'Бенчмарк ' || (ARRAY['Пиццерия','Суши-бар','Бургерная','Шаурмичная','Кофейня',
                          'Пекарня','Хинкальная','Столовая','Чайхана','Кебабная'])[1 + i % 10]
                || ' «' || (ARRAY['Тесто','Огонь','Мангал','Печь','Уголёк','Сковорода',
                                  'Казан','Дым','Соль','Перец','Базилик'])[1 + i % 11] || '» ' || i,
    (ARRAY['Готовим на дровах в печи, которую сложили вручную',
           'Доставка за 30 минут по всему району, курьеры свои',
           'Кухня открыта до полуночи, последний заказ в 23:30',
           'Своя пекарня во дворе, тесто ставим с вечера',
           'Рецепты шефа, который двенадцать лет проработал в Тбилиси',
           'Работаем с фермерскими поставщиками из Подмосковья',
           'Завтраки с восьми утра, кофе обжариваем сами'])[1 + i % 7]
        || '. ' ||
    (ARRAY['Столик можно занять без брони','Есть веранда и место для колясок',
           'Забрать заказ с собой дешевле на десять процентов',
           'По будням бизнес-ланч','Детское меню и стульчики',
           'Оплата картой и наличными'])[1 + i % 6] || '. Заведение № ' || i || '.',
    'Москва, улица ' || (i % 900) || ', дом ' || (i % 90),
    i % 20 <> 0,   -- каждое двадцатое закрыто: частичный индекс должен что-то отсекать
    NOW() - (i || ' minutes')::interval
FROM generate_series(1, 50000) AS i;

INSERT INTO menu_categories (id, restaurant_id, name, sort_order)
SELECT bench_uuid_v7(1735689600000 + n / 10, 1000000 + n), id, 'Основное меню', 0
FROM (
    SELECT r.id, row_number() OVER (ORDER BY r.id)::bigint AS n
    FROM restaurants r
    WHERE r.name LIKE 'Бенчмарк %'
) AS s;

INSERT INTO menu_items (id, restaurant_id, category_id, name, description,
                        price_cents, stock_quantity, is_available, created_at)
SELECT
    bench_uuid_v7(1735689600000 + (n * 4 + d) / 10, 2000000 + n * 4 + d),
    c.restaurant_id,
    c.id,
    (ARRAY['Маргарита','Пепперони','Шаурма','Чизбургер','Хинкали',
           'Том ям','Цезарь','Плов','Круассан','Эспрессо'])[1 + (n * 4 + d) % 10]
        || ' ' || (n * 4 + d),
    CASE WHEN (n * 4 + d) % 16 = 0 THEN 'Состав: моцарелла, томаты, базилик, оливковое масло'
         ELSE (ARRAY['Состав: пепперони, орегано, томатный соус, сыр',
                     'Состав: курица, лаваш, соус чесночный, овощи',
                     'Состав: говядина, чеддер, булочка бриошь, лук',
                     'Состав: рис, нори, лосось, сливочный сыр',
                     'Состав: тесто, сливочное масло, сахар, ваниль',
                     'Состав: баранина, зира, лук, морковь'])[1 + (n * 4 + d) % 6] END
        || ' Готовится ' || (10 + (n * 4 + d) % 20) || ' минут.',
    30000 + ((n * 4 + d) % 70) * 1000,
    100,
    (n * 4 + d) % 25 <> 0,   -- часть позиций в стоп-листе
    NOW() - ((n * 4 + d) || ' seconds')::interval
FROM (
    SELECT c.id, c.restaurant_id, row_number() OVER (ORDER BY c.id)::bigint AS n
    FROM menu_categories c
    JOIN restaurants r ON r.id = c.restaurant_id
    WHERE r.name LIKE 'Бенчмарк %'
) AS c
CROSS JOIN generate_series(0, 3) AS d;

ANALYZE restaurants;
ANALYZE menu_categories;
ANALYZE menu_items;

SELECT (SELECT count(*) FROM restaurants) AS restaurants,
       (SELECT count(*) FROM menu_items)  AS menu_items;


EXPLAIN (ANALYZE, BUFFERS, COSTS OFF)
SELECT id, name, description, address, is_active, created_at, updated_at
FROM restaurants
WHERE is_active = true
ORDER BY created_at DESC
LIMIT 20 OFFSET 0;

EXPLAIN (ANALYZE, BUFFERS, COSTS OFF)
SELECT id, name, description, address, is_active, created_at, updated_at
FROM restaurants
WHERE is_active = true
  AND (name ILIKE '%zzqx%' ESCAPE '\' OR description ILIKE '%zzqx%' ESCAPE '\')
ORDER BY created_at DESC
LIMIT 20 OFFSET 0;

SET enable_bitmapscan = off;
SET enable_indexscan  = off;
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF)
SELECT id, name, description, address, is_active, created_at, updated_at
FROM restaurants
WHERE is_active = true
  AND (name ILIKE '%zzqx%' ESCAPE '\' OR description ILIKE '%zzqx%' ESCAPE '\')
ORDER BY created_at DESC
LIMIT 20 OFFSET 0;
RESET enable_bitmapscan;
RESET enable_indexscan;

EXPLAIN (ANALYZE, BUFFERS, COSTS OFF)
SELECT id, name, description, address, is_active, created_at, updated_at
FROM restaurants
WHERE is_active = true
  AND (name ILIKE '%Пиццерия%' ESCAPE '\' OR description ILIKE '%Пиццерия%' ESCAPE '\')
ORDER BY created_at DESC
LIMIT 20 OFFSET 0;

EXPLAIN (ANALYZE, BUFFERS, COSTS OFF)
SELECT mi.id, mi.name, r.name
FROM menu_items mi
JOIN restaurants r ON r.id = mi.restaurant_id
WHERE mi.is_available = true AND r.is_active = true
  AND (mi.name ILIKE '%zzqx%' ESCAPE '\' OR mi.description ILIKE '%zzqx%' ESCAPE '\')
ORDER BY mi.name ASC, mi.id ASC
LIMIT 20 OFFSET 0;

SELECT count(*) AS matches_for_mozzarella
FROM menu_items mi JOIN restaurants r ON r.id = mi.restaurant_id
WHERE mi.is_available = true AND r.is_active = true
  AND (mi.name ILIKE '%моцарелла%' ESCAPE '\' OR mi.description ILIKE '%моцарелла%' ESCAPE '\');

EXPLAIN (ANALYZE, BUFFERS, COSTS OFF)
SELECT mi.id, mi.name, r.name
FROM menu_items mi
JOIN restaurants r ON r.id = mi.restaurant_id
WHERE mi.is_available = true AND r.is_active = true
  AND (mi.name ILIKE '%моцарелла%' ESCAPE '\' OR mi.description ILIKE '%моцарелла%' ESCAPE '\')
ORDER BY mi.name ASC, mi.id ASC
LIMIT 20 OFFSET 0;

EXPLAIN (ANALYZE, COSTS OFF)
SELECT id FROM restaurants WHERE is_active = true ORDER BY created_at DESC LIMIT 20 OFFSET 40000;


CREATE EXTENSION IF NOT EXISTS pgstattuple;

DROP TABLE IF EXISTS bench_v4;
DROP TABLE IF EXISTS bench_v7;
CREATE TABLE bench_v4 (id uuid PRIMARY KEY, payload text NOT NULL);
CREATE TABLE bench_v7 (id uuid PRIMARY KEY, payload text NOT NULL);

INSERT INTO bench_v4 SELECT gen_random_uuid(), 'x' FROM generate_series(1, 200000);
INSERT INTO bench_v7 SELECT bench_uuid_v7(1735689600000 + i / 10, i), 'x' FROM generate_series(1, 200000) AS i;

SELECT 'v4' AS version,
       pg_size_pretty(pg_relation_size('bench_v4_pkey')) AS pk_index_size,
       round(avg_leaf_density::numeric, 1) AS leaf_density
FROM pgstatindex('bench_v4_pkey')
UNION ALL
SELECT 'v7',
       pg_size_pretty(pg_relation_size('bench_v7_pkey')),
       round(avg_leaf_density::numeric, 1)
FROM pgstatindex('bench_v7_pkey');


DELETE FROM menu_items mi USING restaurants r
    WHERE r.id = mi.restaurant_id AND r.name LIKE 'Бенчмарк %';
DELETE FROM menu_categories c USING restaurants r
    WHERE r.id = c.restaurant_id AND r.name LIKE 'Бенчмарк %';
DELETE FROM restaurants WHERE name LIKE 'Бенчмарк %';

DROP TABLE bench_v4;
DROP TABLE bench_v7;
DROP FUNCTION bench_uuid_v7(bigint, bigint);

VACUUM ANALYZE restaurants;
VACUUM ANALYZE menu_items;

SELECT (SELECT count(*) FROM restaurants) AS restaurants_left,
       (SELECT count(*) FROM menu_items)  AS menu_items_left;
