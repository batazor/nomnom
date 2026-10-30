#!/usr/bin/env python3
"""Idempotent demo seed of the kind environment (Job of chart ops/kind/demo).

Runs in the cluster after every install/upgrade as ServiceAccount nomnom-bff/demo-seed and plays
the operator and the demo user (owner, kitchen staff and courier at once): the Job's projected
token plus a user assertion signed with the BFF key (mint_jwt.py), through the internal gateway.
Data: /etc/demo/seed.json (ops/kind/demo/files/seed.json).

The topology (ADR 41) is declared by the Cell and Market resources of this chart. Restaurants and
couriers are created in Market seed["market"].

Re-running changes nothing that already exists: restaurants are matched by name among
ListOwnedRestaurants of the owner, menu items by name among GetMenu, cooks by name among
GetCookPool, staff/open/line/availability calls are idempotent, and RegisterCourier of a known
courier is tolerated. SpiceDB relationships of seeded objects are not written here: restaurants
and delivery reconcile them from storage.
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


def owned_restaurants(driver: Driver, owner: str, market: str) -> dict:
    found, token = {}, ""
    while True:
        page = driver.call("nomnom.restaurants", owner, RESTAURANTS + "ListOwnedRestaurants",
                           {"marketId": market, "pageSize": 100, "pageToken": token})
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
                                 {"name": spec["name"], "ownerId": owner, "marketId": seed["market"],
                                  "timeZone": seed["timeZone"]})["restaurant"]
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
    seed_cooks(driver, owner, restaurant_id, spec["cooks"])
    # The demo restaurant works round the clock: two adjacent periods a day, because an
    # interval may not open and close at the same time. Repeats answer changed = false.
    driver.call("nomnom.restaurants", owner, RESTAURANTS + "SetOpeningHours",
                {"restaurantId": restaurant_id, "openingHours": [
                    {"dayOfWeek": interval["day"], "opens": interval["opens"], "closes": interval["closes"]}
                    for interval in spec["openingHours"]]})
    # Zero opening lead and closing grace: windows run on across the adjacent periods.
    driver.call("nomnom.restaurants", owner, RESTAURANTS + "SetScheduledOrderSettings",
                {"restaurantId": restaurant_id, "settings": spec["scheduledOrders"]})
    if not restaurant.get("isOpen"):
        driver.call("nomnom.restaurants", owner, RESTAURANTS + "OpenRestaurant", {"restaurantId": restaurant_id})
        print(f"  opened {spec['name']}")
    return {"id": restaurant_id, "name": spec["name"]}


def seed_cooks(driver: Driver, owner: str, restaurant_id: str, names: list) -> None:
    """Cook Pool ресторана (FR-614): повара seed по имени, все на линии."""
    pool = driver.call("nomnom.restaurants", owner, RESTAURANTS + "GetCookPool", {"restaurantId": restaurant_id})
    cooks = {cook["name"]: cook for cook in pool.get("cooks", [])}
    for name in names:
        cook = cooks.get(name)
        if cook is None:
            cook = driver.call("nomnom.restaurants", owner, RESTAURANTS + "HireCook",
                               {"restaurantId": restaurant_id, "name": name})["cook"]
            print(f"  hired cook {name}")
        if not cook.get("onLine"):
            driver.call("nomnom.restaurants", owner, RESTAURANTS + "PutCookOnLine",
                        {"restaurantId": restaurant_id, "cookId": cook["cookId"]})
            print(f"  cook {name} is on line")


def seed_courier(driver: Driver, seed: dict, courier: dict) -> None:
    """Courier Pool (FR-301): курьер с именем и позицией на линии. Первый — demo-пользователь,
    остальные действуют только через Simulator."""
    try:
        driver.call("nomnom.delivery", seed["operator"], DELIVERY + "RegisterCourier",
                    {"courierId": courier["id"], "name": courier["name"], "marketId": seed["market"]})
        print(f"registered courier {courier['name']} ({courier['id']})")
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
    existing = owned_restaurants(driver, seed["owner"], seed["market"])
    restaurants = [seed_restaurant(driver, seed, spec, existing) for spec in seed["restaurants"]]
    for courier in seed["couriers"]:
        seed_courier(driver, seed, courier)
    print(f"demo seed: {len(restaurants)} restaurants, {len(seed['couriers'])} couriers")


if __name__ == "__main__":
    main()
