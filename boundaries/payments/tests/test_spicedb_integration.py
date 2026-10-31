"""Integration with a real SpiceDB bootstrapped from ``authorization/bootstrap.yaml``.

SpiceDB runs in testcontainers (``tests/conftest.py``): plaintext, preshared key ``devkey``.
"""

import time
import unittest

import pytest
from conftest import SPICEDB_PRESHARED_KEY

from pkg.authz import AuthorizationUnavailableError, Subject, SubjectKind
from pkg.config import SpiceDBSettings
from slices.payment.applications.ports import PaymentPermission, PaymentResource
from slices.payment.domains.payment import OrderId, PaymentId
from slices.payment.infrastructure.authorization import (
    SpiceDBPermissionChecker,
    create_spicedb_client,
)

RESOURCE = PaymentResource(PaymentId("payment-it"), OrderId("order-it"))


def _checker(
    endpoint: str, timeout: str = "2s", key: str = SPICEDB_PRESHARED_KEY
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


@pytest.mark.usefixtures("spicedb")
class SpiceDBIntegrationTest(unittest.TestCase):
    spicedb_endpoint: str

    def test_orders_worker_has_every_payment_permission(self) -> None:
        # AC-465: the fulfillment worker may call payment commands and queries.
        checker = _checker(self.spicedb_endpoint)
        worker = Subject(SubjectKind.SERVICE, "orders-worker")
        for permission in PaymentPermission:
            with self.subTest(permission=permission):
                self.assertTrue(checker.is_allowed(worker, permission, RESOURCE))

    def test_other_service_is_denied(self) -> None:
        checker = _checker(self.spicedb_endpoint)
        bff = Subject(SubjectKind.SERVICE, "bff")
        for permission in PaymentPermission:
            with self.subTest(permission=permission):
                self.assertFalse(checker.is_allowed(bff, permission, RESOURCE))

    def test_unknown_user_is_denied(self) -> None:
        checker = _checker(self.spicedb_endpoint)
        user = Subject(SubjectKind.USER, "u-1")
        self.assertFalse(checker.is_allowed(user, PaymentPermission.VIEW, RESOURCE))

    def test_wrong_preshared_key_is_unavailable(self) -> None:
        checker = _checker(self.spicedb_endpoint, key="wrong-key")
        worker = Subject(SubjectKind.SERVICE, "orders-worker")
        with (
            self.assertLogs("slices.payment.infrastructure.authorization.spicedb", "WARNING"),
            self.assertRaises(AuthorizationUnavailableError),
        ):
            checker.is_allowed(worker, PaymentPermission.AUTHORIZE, RESOURCE)

    def test_unreachable_spicedb_is_unavailable_within_timeout(self) -> None:
        # AC-466: no answer within SPICEDB__TIMEOUT fails closed.
        checker = _checker("localhost:1", timeout="300ms")
        worker = Subject(SubjectKind.SERVICE, "orders-worker")
        started = time.monotonic()
        with (
            self.assertLogs("slices.payment.infrastructure.authorization.spicedb", "WARNING"),
            self.assertRaises(AuthorizationUnavailableError),
        ):
            checker.is_allowed(worker, PaymentPermission.AUTHORIZE, RESOURCE)
        self.assertLess(time.monotonic() - started, 2.0)


if __name__ == "__main__":
    unittest.main()
