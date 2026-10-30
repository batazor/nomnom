-- Fulfillment progress became Order States (docs/fsm.md#order): an open Order
-- moves to the furthest state its facts already reached, like the monotonic
-- transitions would have moved it. order_version does not change; order_details
-- is projected again at process start by its new projection_version.
UPDATE orders o
SET state = CASE
    WHEN EXISTS (SELECT 1 FROM order_facts f WHERE f.order_id = o.id AND f.fact = 'picked_up') THEN 'picked_up'
    WHEN EXISTS (SELECT 1 FROM order_facts f WHERE f.order_id = o.id AND f.fact = 'ready_for_pickup') THEN 'ready'
    WHEN EXISTS (SELECT 1 FROM order_facts f WHERE f.order_id = o.id AND f.fact = 'preparation_started') THEN 'preparing'
    WHEN o.state = 'placed' AND EXISTS (SELECT 1 FROM order_facts f WHERE f.order_id = o.id AND f.fact = 'payment_authorized') THEN 'paid'
    ELSE o.state
END
WHERE o.state IN ('placed', 'confirmed');
