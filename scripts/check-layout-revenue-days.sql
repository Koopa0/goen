-- Places the revenue fixture's eight orders: the first (uuidv7 ids ascend), which
-- carries the large figure, on yesterday, one on each day before it, and the
-- eighth on today. check-layout.sql places them, and the running-total check
-- places them again just before it reads /admin/reports?days=7, which draws its
-- running total only when each of its seven days, today included, has a paid
-- order, and a run can cross the shop's midnight between the two.
UPDATE orders o SET placed_at = now() - make_interval(days => d.day_ago::integer)
FROM (SELECT ol.order_id, row_number() OVER (ORDER BY ol.order_id) % 8 AS day_ago
      FROM order_lines ol WHERE ol.sku = 'LAYOUT-REVENUE') d
WHERE o.id = d.order_id;
