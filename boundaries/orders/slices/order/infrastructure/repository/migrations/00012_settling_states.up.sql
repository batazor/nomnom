-- The outcome of restaurants and delivery became Order States (docs/fsm.md#order):
-- an Order still open with such a fact moves to it. A failure wins over a
-- delivery completion, as the transitions would have decided by the first fact
-- of them. order_details is projected again at process start.
UPDATE orders o
SET state = CASE
    WHEN EXISTS (SELECT 1 FROM order_facts f WHERE f.order_id = o.id AND f.fact = 'restaurant_rejected') THEN 'rejected'
    WHEN EXISTS (SELECT 1 FROM order_facts f WHERE f.order_id = o.id AND f.fact = 'delivery_failed') THEN 'delivery_failed'
    WHEN EXISTS (SELECT 1 FROM order_facts f WHERE f.order_id = o.id AND f.fact = 'delivery_completed') THEN 'delivered'
    ELSE o.state
END
WHERE o.state IN ('placed', 'paid', 'confirmed', 'preparing', 'ready', 'picked_up');
