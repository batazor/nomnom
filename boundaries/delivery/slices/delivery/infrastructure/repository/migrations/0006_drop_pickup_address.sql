-- The restaurant pickup address is no longer carried by delivery: couriers
-- find the run's offer by order id, the address stays with restaurants.
ALTER TABLE deliveries DROP COLUMN pickup_address;
