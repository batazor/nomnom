import os
import unittest
from datetime import timedelta
from unittest.mock import patch

import grpc
from authzed.api.v1 import CheckPermissionRequest, CheckPermissionResponse
from pydantic import ValidationError

from di.payments import SpiceDBConfigurationError, create_permission_checker
from infrastructure.authorization import (
    PERMISSION_MAPPING,
    SpiceDBPermissionChecker,
)
from pkg.application import (
    AuthorizationUnavailableError,
    PermissionDeniedError,
    Subject,
    SubjectKind,
)
from pkg.config import SpiceDBSettings, load_settings
from slices.payment.applications.access import require_permission
from slices.payment.applications.ports import PaymentPermission, PaymentResource
from slices.payment.domains.payment import OrderId, PaymentId

Permissionship = CheckPermissionResponse.Permissionship
WORKER = Subject(SubjectKind.SERVICE, "orders-worker")
RESOURCE = PaymentResource(PaymentId("payment-1"), OrderId("order-1"))
MANAGE = (
    PaymentPermission.CREATE,
    PaymentPermission.AUTHORIZE,
    PaymentPermission.CAPTURE,
    PaymentPermission.CANCEL_AUTHORIZATION,
    PaymentPermission.REFUND,
)
VIEW = (PaymentPermission.VIEW, PaymentPermission.VIEW_OPERATIONS)

# DATABASE__URL обязателен: без него load_settings падает.
REQUIRED_ENV = {"DATABASE__URL": "postgresql://payments@localhost/payments"}


class _RpcFailure(grpc.RpcError):
    def __init__(self, code: grpc.StatusCode) -> None:
        super().__init__(code.name)
        self._code = code

    def code(self) -> grpc.StatusCode:
        return self._code


class FakePermissionsClient:
    def __init__(
        self,
        permissionship: CheckPermissionResponse.Permissionship.ValueType = (
            Permissionship.PERMISSIONSHIP_HAS_PERMISSION
        ),
        error: Exception | None = None,
    ) -> None:
        self.permissionship = permissionship
        self.error = error
        self.requests: list[CheckPermissionRequest] = []
        self.timeouts: list[float] = []

    def CheckPermission(  # noqa: N802 - name fixed by the gRPC/specification contract
        self, request: CheckPermissionRequest, *, timeout: float
    ) -> CheckPermissionResponse:
        self.requests.append(request)
        self.timeouts.append(timeout)
        if self.error is not None:
            raise self.error
        return CheckPermissionResponse(permissionship=self.permissionship)


class MappingTest(unittest.TestCase):
    def test_every_permission_is_mapped(self) -> None:
        self.assertEqual(set(PERMISSION_MAPPING), set(PaymentPermission))

    def test_manage_permissions_check_platform_manage_payments(self) -> None:
        self._assert_checks(MANAGE, "manage_payments")

    def test_view_permissions_check_platform_view_payments(self) -> None:
        self._assert_checks(VIEW, "view_payments")

    def _assert_checks(self, permissions: tuple[PaymentPermission, ...], expected: str) -> None:
        for permission in permissions:
            with self.subTest(permission=permission):
                client = FakePermissionsClient()
                SpiceDBPermissionChecker(client, 2.0).is_allowed(WORKER, permission, RESOURCE)
                (request,) = client.requests
                self.assertEqual(request.resource.object_type, "platform")
                self.assertEqual(request.resource.object_id, "nomnom")
                self.assertEqual(request.permission, expected)
                self.assertTrue(request.consistency.minimize_latency)
                self.assertEqual(client.timeouts, [2.0])

    def test_subject_kinds(self) -> None:
        cases = {
            Subject(SubjectKind.USER, "u-42"): ("user", "u-42"),
            Subject(SubjectKind.SERVICE, "orders-worker"): ("service", "orders-worker"),
        }
        for subject, (object_type, object_id) in cases.items():
            with self.subTest(subject=subject):
                client = FakePermissionsClient()
                SpiceDBPermissionChecker(client, 1.0).is_allowed(
                    subject, PaymentPermission.VIEW, RESOURCE
                )
                reference = client.requests[0].subject
                self.assertEqual(reference.object.object_type, object_type)
                self.assertEqual(reference.object.object_id, object_id)
                self.assertEqual(reference.optional_relation, "")


class DecisionTest(unittest.TestCase):
    def test_only_has_permission_allows(self) -> None:
        cases = {
            Permissionship.PERMISSIONSHIP_HAS_PERMISSION: True,
            Permissionship.PERMISSIONSHIP_NO_PERMISSION: False,
            Permissionship.PERMISSIONSHIP_CONDITIONAL_PERMISSION: False,
            Permissionship.PERMISSIONSHIP_UNSPECIFIED: False,
        }
        for permissionship, expected in cases.items():
            with self.subTest(permissionship=permissionship):
                checker = SpiceDBPermissionChecker(FakePermissionsClient(permissionship), 1.0)
                self.assertIs(
                    checker.is_allowed(WORKER, PaymentPermission.AUTHORIZE, RESOURCE), expected
                )

    def test_errors_fail_closed_as_unavailable(self) -> None:
        errors = (
            _RpcFailure(grpc.StatusCode.UNAVAILABLE),
            _RpcFailure(grpc.StatusCode.DEADLINE_EXCEEDED),
            _RpcFailure(grpc.StatusCode.UNAUTHENTICATED),
            RuntimeError("unexpected"),
        )
        for error in errors:
            with self.subTest(error=error):
                checker = SpiceDBPermissionChecker(FakePermissionsClient(error=error), 1.0)
                with (
                    self.assertLogs("infrastructure.authorization.spicedb", "WARNING"),
                    self.assertRaises(AuthorizationUnavailableError),
                ):
                    checker.is_allowed(WORKER, PaymentPermission.CAPTURE, RESOURCE)

    def test_use_case_check_propagates_unavailable_and_denial(self) -> None:
        unavailable = SpiceDBPermissionChecker(
            FakePermissionsClient(error=_RpcFailure(grpc.StatusCode.DEADLINE_EXCEEDED)), 1.0
        )
        with (
            self.assertLogs("infrastructure.authorization.spicedb", "WARNING"),
            self.assertRaises(AuthorizationUnavailableError),
        ):
            require_permission(unavailable, WORKER, PaymentPermission.REFUND, RESOURCE)
        denied = SpiceDBPermissionChecker(
            FakePermissionsClient(Permissionship.PERMISSIONSHIP_NO_PERMISSION), 1.0
        )
        with self.assertRaises(PermissionDeniedError):
            require_permission(denied, WORKER, PaymentPermission.REFUND, RESOURCE)

    def test_rejects_non_positive_timeout(self) -> None:
        with self.assertRaises(ValueError):
            SpiceDBPermissionChecker(FakePermissionsClient(), 0)


class CompositionTest(unittest.TestCase):
    def test_empty_endpoint_fails_startup(self) -> None:
        with self.assertRaisesRegex(SpiceDBConfigurationError, "SPICEDB__ENDPOINT"):
            create_permission_checker(SpiceDBSettings.model_validate({}))

    def test_configured_endpoint_creates_spicedb_checker(self) -> None:
        for insecure in (True, False):
            with self.subTest(insecure=insecure):
                settings = SpiceDBSettings.model_validate(
                    {
                        "SPICEDB__ENDPOINT": "localhost:50051",
                        "SPICEDB__PRESHARED_KEY": "devkey",
                        "SPICEDB__INSECURE": insecure,
                    }
                )
                checker = create_permission_checker(settings)
                self.assertIsInstance(checker, SpiceDBPermissionChecker)


class SettingsTest(unittest.TestCase):
    def test_defaults(self) -> None:
        with patch.dict(os.environ, REQUIRED_ENV, clear=True):
            settings = load_settings().spicedb
        self.assertEqual(settings.endpoint, "")
        self.assertFalse(settings.enabled)
        self.assertFalse(settings.insecure)
        self.assertEqual(settings.timeout, timedelta(seconds=2))

    def test_reads_environment(self) -> None:
        environment = {
            "SPICEDB__ENDPOINT": "spicedb.nomnom-authz:50051",
            "SPICEDB__PRESHARED_KEY": "devkey",
            "SPICEDB__INSECURE": "true",
            "SPICEDB__TIMEOUT": "500ms",
        }
        with patch.dict(os.environ, {**REQUIRED_ENV, **environment}, clear=True):
            settings = load_settings().spicedb
        self.assertTrue(settings.enabled)
        self.assertEqual(settings.endpoint, "spicedb.nomnom-authz:50051")
        self.assertTrue(settings.insecure)
        self.assertEqual(settings.timeout, timedelta(milliseconds=500))
        self.assertNotIn("devkey", repr(settings))

    def test_endpoint_without_key_is_startup_error(self) -> None:
        environment = {"SPICEDB__ENDPOINT": "localhost:50051", "SPICEDB__PRESHARED_KEY": " "}
        with (
            patch.dict(os.environ, {**REQUIRED_ENV, **environment}, clear=True),
            self.assertRaises(ValidationError),
        ):
            load_settings()

    def test_rejects_invalid_timeout(self) -> None:
        for value in ("0s", "2", "PT2S", "-1s"):
            with (
                self.subTest(value=value),
                patch.dict(os.environ, {**REQUIRED_ENV, "SPICEDB__TIMEOUT": value}, clear=True),
                self.assertRaises(ValidationError),
            ):
                load_settings()


if __name__ == "__main__":
    unittest.main()
