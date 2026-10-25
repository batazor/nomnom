#!/usr/bin/env python3
"""Idempotent demo seed of the kind environment (after-hook of `make kind-deploy`).

Plays the operator and the demo user (owner, kitchen staff and courier at once) through the e2e-driver fixture the
same way e2e/templates/driver-call.yaml does: the driver's projected token plus a user
assertion signed on the host with the BFF key (scripts/mint-jwt.py). Data: ops/kind/demo/seed.json.

Re-running changes nothing that already exists: restaurants are matched by name among
ListOwnedRestaurants of the owner, menu items by name among GetMenu, staff/open/availability
calls are idempotent, and RegisterCourier of a known courier is tolerated.

Usage: seed-demo.py <kube-context>
"""
import base64
import json
import os
import subprocess
import sys
import tempfile
import time

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
MINT = os.path.join(ROOT, "ops", "kind", "scripts", "mint-jwt.py")
SEED = os.path.join(ROOT, "ops", "kind", "demo", "seed.json")
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


def kubectl(context: str, *args: str, stdin=None) -> str:
    return subprocess.run(
        ["kubectl", "--context", context, *args], input=stdin, capture_output=True, text=True, check=True
    ).stdout


class Driver:
    def __init__(self, context: str, key_file: str) -> None:
        self.context, self.key_file = context, key_file

    def call(self, audience: str, subject: str, method: str, request: dict, attempts: int = 30) -> dict:
        """Calls method as subject; retries UNAVAILABLE-like failures while services start."""
        for attempt in range(attempts):
            assertion = subprocess.run(
                ["python3", "-I", MINT, "user-assertion", self.key_file, "nomnom-bff-kind", audience, subject],
                capture_output=True, text=True, check=True,
            ).stdout.strip()
            result = subprocess.run(
                ["kubectl", "--context", self.context, "-n", "nomnom-bff", "exec", "deploy/e2e-driver", "--",
                 "sh", "-c",
                 'grpcurl -plaintext -max-time 10 -format-error -protoset /protos/nomnom.binpb '
                 '-H "authorization: Bearer $(cat /var/run/secrets/nomnom/identity/$0)" '
                 '-H "x-nomnom-user-assertion: $1" -d "$2" "$3" "$4" 2>&1',
                 audience, assertion, json.dumps(request), GATEWAY, method],
                capture_output=True, text=True,
            )
            output = result.stdout.strip()
            if result.returncode == 0:
                return json.loads(output or "{}")
            code = error_code(output)
            if code not in (None, UNAVAILABLE, DEADLINE_EXCEEDED) or attempt == attempts - 1:
                raise CallError(f"{method} as {subject}: {output}", code)
            time.sleep(2)
        raise AssertionError("unreachable")


def restore_relationships(context: str, relationships: list) -> None:
    """TOUCHes SpiceDB relationships of already seeded objects.

    SpiceDB in kind keeps data in memory and restarts whenever schema or bootstrap changes, while
    MongoDB and PostgreSQL keep the restaurants and the courier: their owner, staff and self
    relationships, written once by the services, are then lost. A one-off pod with the e2e-probe
    image (grpcurl, SpiceDB server reflection) writes them back; TOUCH is idempotent.
    """
    if not relationships:
        return
    key = base64.b64decode(json.loads(kubectl(
        context, "-n", "nomnom-authz", "get", "secret", "spicedb-preshared-key", "-o", "json"))["data"]["preshared-key"]).decode()
    image = kubectl(context, "-n", "nomnom-bff", "get", "deploy", "e2e-driver",
                    "-o", "jsonpath={.spec.template.spec.containers[0].image}")
    body = {"updates": [{
        "operation": "OPERATION_TOUCH",
        "relationship": {
            "resource": {"objectType": kind, "objectId": object_id},
            "relation": relation,
            "subject": {"object": {"objectType": "user", "objectId": user}},
        },
    } for kind, object_id, relation, user in relationships]}
    # Key first line of stdin, request the rest: neither lands in the pod spec.
    kubectl(context, "-n", "nomnom-authz", "run", "demo-seed-authz", "--rm", "-i", "--quiet", "--restart=Never",
            f"--image={image}", "--image-pull-policy=IfNotPresent", "--command", "--",
            "sh", "-c", 'read -r key; grpcurl -plaintext -max-time 10 -H "authorization: Bearer $key" -d @ '
            "spicedb.nomnom-authz.svc.cluster.local:50051 authzed.api.v1.PermissionsService/WriteRelationships",
            stdin=key + "\n" + json.dumps(body))


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


def main(context: str) -> None:
    seed = json.load(open(SEED))
    kubectl(context, "-n", "nomnom-bff", "rollout", "status", "deploy/e2e-driver", "--timeout=300s")
    secret = json.loads(kubectl(context, "-n", "nomnom-bff", "get", "secret", "bff-assertion-key", "-o", "json"))
    pem = base64.b64decode(secret["data"].get("tls.key") or secret["data"]["key.pem"])
    with tempfile.NamedTemporaryFile("wb", suffix=".pem") as key:
        key.write(pem)
        key.flush()
        driver = Driver(context, key.name)
        existing = owned_restaurants(driver, seed["owner"])
        restore_relationships(context, [
            relationship
            for restaurant in existing.values()
            for relationship in (("restaurant", restaurant["restaurantId"], "owner", seed["owner"]),
                                 ("restaurant", restaurant["restaurantId"], "staff", seed["kitchen"]))
        ] + [("courier", seed["courier"]["id"], "self", seed["courier"]["id"])])
        restaurants = [seed_restaurant(driver, seed, spec, existing) for spec in seed["restaurants"]]
        seed_courier(driver, seed)
    print(f"demo seed: {len(restaurants)} restaurants, courier {seed['courier']['id']}")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    main(sys.argv[1])
