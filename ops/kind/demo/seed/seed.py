#!/usr/bin/env python3
"""Idempotent demo seed of the kind environment (Job of chart ops/kind/demo).

Runs in the cluster after every install/upgrade as ServiceAccount nomnom-bff/demo-seed and plays
the operator and the demo user (owner, kitchen staff and courier at once): the Job's projected
token plus a user assertion signed with the BFF key (mint_jwt.py), through the internal gateway.
Data: /etc/demo/seed.json (ops/kind/demo/files/seed.json).

Re-running changes nothing that already exists: restaurants are matched by name among
ListOwnedRestaurants of the owner, menu items by name among GetMenu, staff/open/availability
calls are idempotent, and RegisterCourier of a known courier is tolerated. SpiceDB relationships
of seeded objects are not written here: restaurants and delivery reconcile them from storage.
"""
import json
import os
import subprocess
import sys
import time

sys.path.insert(0, "/seed")
from mint_jwt import user_assertion  # noqa: E402

SEED = "/etc/demo/seed.json"
IDENTITY = "/var/run/secrets/nomnom/identity"
KEY_DIR = "/var/run/secrets/nomnom/assertion-key"
GATEWAY = "internal-gateway.envoy-gateway-system.svc.cluster.local:8080"
RESTAURANTS = "nomnom.restaurants.v1.RestaurantsService/"
DELIVERY = "nomnom.delivery.v1.DeliveryService/"


# gRPC status codes printed by grpcurl -format-error.
NOT_FOUND, ALREADY_EXISTS, DEADLINE_EXCEEDED, UNAVAILABLE = 5, 6, 4, 14


class CallError(Exception):
    def __init__(self, message: str, code) -> None:
        super().__init__(message)
        self.code = code


def error_code(output: str):
    try:
        return json.loads(output).get("code")
    except ValueError:
        return None  # not a gRPC status: dial error, exec failure


class Driver:
    def __init__(self, key_file: str, kid: str) -> None:
        self.key_file, self.kid = key_file, kid

    def call(self, audience: str, subject: str, method: str, request: dict, attempts: int = 30) -> dict:
        """Calls method as subject; retries UNAVAILABLE-like failures while services start."""
        for attempt in range(attempts):
            token = open(os.path.join(IDENTITY, audience)).read().strip()
            assertion = user_assertion(self.key_file, self.kid, audience, subject)
            result = subprocess.run(
                ["grpcurl", "-plaintext", "-max-time", "10", "-format-error", "-protoset", "/protos/nomnom.binpb",
                 "-H", f"authorization: Bearer {token}", "-H", f"x-nomnom-user-assertion: {assertion}",
                 "-d", json.dumps(request), GATEWAY, method],
                capture_output=True, text=True,
            )
            output = (result.stdout + result.stderr).strip()
            if result.returncode == 0:
                return json.loads(result.stdout.strip() or "{}")
            code = error_code(output)
            if code not in (None, UNAVAILABLE, DEADLINE_EXCEEDED) or attempt == attempts - 1:
                raise CallError(f"{method} as {subject}: {output}", code)
            time.sleep(2)
        raise AssertionError("unreachable")


def owned_restaurants(driver: Driver, owner: str) -> dict:
    found, token = {}, ""
    while True:
        page = driver.call("nomnom.restaurants", owner, RESTAURANTS + "ListOwnedRestaurants",
                           {"pageSize": 100, "pageToken": token})
        for restaurant in page.get("restaurants", []):
            found[restaurant["name"]] = restaurant
        token = page.get("nextPageToken", "")
        if not token:
            return found


def seed_restaurant(driver: Driver, seed: dict, spec: dict, existing: dict) -> dict:
    owner, kitchen = seed["owner"], seed["kitchen"]
    restaurant = existing.get(spec["name"])
    if restaurant is None:
        restaurant = driver.call("nomnom.restaurants", seed["operator"], RESTAURANTS + "RegisterRestaurant",
                                 {"name": spec["name"], "ownerId": owner, "address": spec["address"]})["restaurant"]
        print(f"registered {spec['name']} ({restaurant['restaurantId']})")
    restaurant_id = restaurant["restaurantId"]
    driver.call("nomnom.restaurants", owner, RESTAURANTS + "AssignStaff",
                {"restaurantId": restaurant_id, "userId": kitchen})
    try:
        items = driver.call("nomnom.restaurants", owner, RESTAURANTS + "GetMenu",
                            {"restaurantId": restaurant_id}).get("items", [])
    except CallError as error:
        if error.code != NOT_FOUND:
            raise
        driver.call("nomnom.restaurants", owner, RESTAURANTS + "CreateMenu", {"restaurantId": restaurant_id})
        items = []
    names = {item["name"] for item in items}
    for item in spec["items"]:
        if item["name"] in names:
            continue
        driver.call("nomnom.restaurants", owner, RESTAURANTS + "AddMenuItem", {
            "restaurantId": restaurant_id, "name": item["name"], "description": item["description"],
            "price": {"amount": item["price"], "currency": "EUR"}, "available": True,
        })
        print(f"  added {item['name']}")
    if not restaurant.get("isOpen"):
        driver.call("nomnom.restaurants", owner, RESTAURANTS + "OpenRestaurant", {"restaurantId": restaurant_id})
        print(f"  opened {spec['name']}")
    return {"id": restaurant_id, "name": spec["name"]}


def seed_courier(driver: Driver, seed: dict) -> None:
    courier = seed["courier"]
    try:
        driver.call("nomnom.delivery", seed["operator"], DELIVERY + "RegisterCourier", {"courierId": courier["id"]})
        print(f"registered courier {courier['id']}")
    except CallError as error:
        if error.code != ALREADY_EXISTS:
            raise
    driver.call("nomnom.delivery", courier["id"], DELIVERY + "UpdateCourierLocation", {
        "courierId": courier["id"], "latitude": courier["latitude"], "longitude": courier["longitude"],
    })
    try:
        driver.call("nomnom.delivery", courier["id"], DELIVERY + "SetCourierAvailability", {
            "courierId": courier["id"], "availability": "COURIER_AVAILABILITY_AVAILABLE",
        })
    except CallError as error:
        # Курьер на доставке (busy) остаётся как есть.
        print(f"courier availability unchanged: {error}")


def main() -> None:
    seed = json.load(open(SEED))
    key_file = next(os.path.join(KEY_DIR, name) for name in ("tls.key", "key.pem")
                    if os.path.exists(os.path.join(KEY_DIR, name)))
    driver = Driver(key_file, os.environ["ASSERTION_KEY_ID"])
    existing = owned_restaurants(driver, seed["owner"])
    restaurants = [seed_restaurant(driver, seed, spec, existing) for spec in seed["restaurants"]]
    seed_courier(driver, seed)
    print(f"demo seed: {len(restaurants)} restaurants, courier {seed['courier']['id']}")


if __name__ == "__main__":
    main()
