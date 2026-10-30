-- The journal of facts becomes the transition history of the Order FSM
-- (docs/fsm.md#order). courier_assigned moves no State: the time of the current
-- assignment stays in orders.courier_assigned_at. Reasons stay with their owners,
-- restaurants and delivery. A cancellation joins the history with its known time;
-- the time of a past completion was not stored. order_details is projected again
-- at process start.
--
-- order_facts stays: the previous version of the code and PINNED workers of open
-- workflows still read and write it (ADR 42). A later migration removes it after
-- they drain; what they record until then does not reach the history.
CREATE TABLE order_transitions (
    order_id    TEXT        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    trigger     TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (order_id, trigger)
);

INSERT INTO order_transitions (order_id, trigger, occurred_at)
SELECT order_id,
    CASE fact
        WHEN 'payment_authorized' THEN 'authorize_payment'
        WHEN 'restaurant_accepted' THEN 'confirm'
        WHEN 'restaurant_rejected' THEN 'reject'
        WHEN 'preparation_started' THEN 'start_preparation'
        WHEN 'ready_for_pickup' THEN 'mark_ready'
        WHEN 'picked_up' THEN 'pick_up'
        WHEN 'delivery_completed' THEN 'deliver'
        WHEN 'delivery_failed' THEN 'fail_delivery'
    END,
    occurred_at
FROM order_facts
WHERE fact <> 'courier_assigned';

INSERT INTO order_transitions (order_id, trigger, occurred_at)
SELECT id, 'cancel', cancelled_at FROM orders WHERE cancelled_at IS NOT NULL;
