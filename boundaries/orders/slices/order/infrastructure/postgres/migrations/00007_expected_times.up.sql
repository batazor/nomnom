-- Latest Fulfillment deadlines named by restaurants and delivery (FR-611):
-- ready time of RestaurantOrderAccepted / PreparationDelayed, pickup time of
-- the latest CourierAssignedToDelivery, handover time of DeliveryPickedUp.
-- NULL — the deadline is not named.
ALTER TABLE orders
    ADD COLUMN expected_ready_at     TIMESTAMPTZ,
    ADD COLUMN expected_pickup_at    TIMESTAMPTZ,
    ADD COLUMN expected_delivered_at TIMESTAMPTZ;
