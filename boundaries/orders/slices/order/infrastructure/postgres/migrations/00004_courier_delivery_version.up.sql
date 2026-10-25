-- Delivery aggregate_version of the courier event (CourierAssignedToDelivery,
-- CourierUnassignedFromDelivery, DeliveryCancelled) that recorded the stored
-- Courier Assignment or release. It orders courier events of the Order: an
-- event of an older version changes nothing. courier_assigned_at stays the
-- event time; it is the start of the delivery transaction and does not order
-- events. Rows recorded before this migration have version 0.
ALTER TABLE orders
    ADD COLUMN courier_delivery_version BIGINT NOT NULL DEFAULT 0
        CONSTRAINT orders_courier_delivery_version_non_negative CHECK (courier_delivery_version >= 0);
