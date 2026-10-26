-- Creation time of a Delivery (epoch seconds), the start of the assignment
-- latency and delivery duration business metrics. Deliveries created before
-- this migration have none.
ALTER TABLE deliveries ADD COLUMN created_at BIGINT;
