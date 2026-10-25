"""Integration with a real SpiceDB bootstrapped from ``authorization/bootstrap.yaml``.

Skipped unless ``SPICEDB_ENDPOINT`` is set, e.g. ``SPICEDB_ENDPOINT=localhost:50051``
against ``make spicedb-dev`` (plaintext, preshared key ``devkey``).
"""

import os
import time
import unittest

from infrastructure.authorization import SpiceDBPermissionChecker, create_spicedb_client
from pkg.application import AuthorizationUnavailable, Subject, SubjectKind
from pkg.config import SpiceDBSettings
from slices.payment.applications.ports import PaymentPermission, PaymentResource
from slices.payment.domains.payment import OrderId, PaymentId

ENDPOINT = os.environ.get("SPICEDB_ENDPOINT", "").strip()
PRESHARED_KEY = "devkey"
RESOURCE = PaymentResource(PaymentId("payment-it"), OrderId("order-it"))


def _checker(
    endpoint: str, timeout: str = "2s", key: str = PRESHARED_KEY
) -> SpiceDBPermissionChecker:
    settings = SpiceDBSettings.model_validate(
        {
            "SPICEDB__ENDPOINT": endpoint,
            "SPICEDB__PRESHARED_KEY": key,
            "SPICEDB__INSECURE": True,
            "SPICEDB__TIMEOUT": timeout,
        }
    )
    return SpiceDBPermissionChecker(
        create_spicedb_client(settings), settings.timeout.total_seconds()
    )


@unittest.skipUnless(ENDPOINT, "SPICEDB_ENDPOINT is not set")
class SpiceDBIntegrationTest(unittest.TestCase):
    def test_orders_worker_has_every_payment_permission(self) -> None:
        # AC-465: the fulfillment worker may call payment commands and queries.
        checker = _checker(ENDPOINT)
        worker = Subject(SubjectKind.SERVICE, "orders-worker")
        for permission in PaymentPermission:
            with self.subTest(permission=permission):
                self.assertTrue(checker.is_allowed(worker, permission, RESOURCE))

    def test_other_service_is_denied(self) -> None:
        checker = _checker(ENDPOINT)
        bff = Subject(SubjectKind.SERVICE, "bff")
        for permission in PaymentPermission:
            with self.subTest(permission=permission):
                self.assertFalse(checker.is_allowed(bff, permission, RESOURCE))

    def test_unknown_user_is_denied(self) -> None:
        checker = _checker(ENDPOINT)
        user = Subject(SubjectKind.USER, "u-1")
        self.assertFalse(checker.is_allowed(user, PaymentPermission.VIEW, RESOURCE))

    def test_wrong_preshared_key_is_unavailable(self) -> None:
        checker = _checker(ENDPOINT, key="wrong-key")
        worker = Subject(SubjectKind.SERVICE, "orders-worker")
        with self.assertLogs("infrastructure.authorization.spicedb", "WARNING"):
            with self.assertRaises(AuthorizationUnavailable):
                checker.is_allowed(worker, PaymentPermission.AUTHORIZE, RESOURCE)

    def test_unreachable_spicedb_is_unavailable_within_timeout(self) -> None:
        # AC-466: no answer within SPICEDB__TIMEOUT fails closed.
        checker = _checker("localhost:1", timeout="300ms")
        worker = Subject(SubjectKind.SERVICE, "orders-worker")
        started = time.monotonic()
        with self.assertLogs("infrastructure.authorization.spicedb", "WARNING"):
            with self.assertRaises(AuthorizationUnavailable):
                checker.is_allowed(worker, PaymentPermission.AUTHORIZE, RESOURCE)
        self.assertLess(time.monotonic() - started, 2.0)


if __name__ == "__main__":
    unittest.main()
