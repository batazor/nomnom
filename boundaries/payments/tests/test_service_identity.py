import json
import os
import tempfile
import time
import unittest
from collections.abc import Sequence
from pathlib import Path
from typing import Any, cast, override
from unittest.mock import patch

import grpc
import jwt
from cryptography.hazmat.primitives.asymmetric import ec
from jwt.algorithms import ECAlgorithm  # ty: ignore[possibly-missing-import] - defined when cryptography is installed
from payment_fakes import PaymentsTestKit, close_container, payments_container
from pydantic import ValidationError

from infrastructure.grpc import identity
from infrastructure.grpc.identity import (
    IdentityConfigError,
    RejectingSubjectResolver,
    ServiceTokenSubjectResolver,
    create_subject_resolver,
    load_jwks,
)
from infrastructure.grpc.service import (
    GrpcWorker,
    PaymentsHandlers,
    payments_pb2,
    service_pb2,
    service_pb2_grpc,
)
from pkg.application import Subject, SubjectKind
from pkg.config import IdentitySettings, load_settings
from slices.payment.applications.authorize_payment import AuthorizePaymentHandler
from slices.payment.applications.cancel_authorization import CancelAuthorizationHandler
from slices.payment.applications.capture_payment import CapturePaymentHandler
from slices.payment.applications.create_payment import CreatePaymentHandler
from slices.payment.applications.get_payment_status import GetPaymentStatusHandler
from slices.payment.applications.refund_payment import RefundPaymentHandler
from slices.payment.domains.payment import PaymentState

# Shared vectors of ADR 27: boundaries/payments/../../tests/identity/fixtures.
FIXTURES = Path(__file__).resolve().parents[1].parent.parent / "tests" / "identity" / "fixtures"
JWKS_FILE = FIXTURES / "jwks.json"
VECTORS: dict[str, Any] = json.loads((FIXTURES / "vectors.json").read_text(encoding="utf-8"))

ORDERS_WORKER = Subject(SubjectKind.SERVICE, VECTORS["expected_service"])
VALID_NBF = 1_700_000_000
VALID_EXP = 4_102_444_800

# DATABASE__URL обязателен: без него load_settings падает.
REQUIRED_ENV = {"DATABASE__URL": "postgresql://payments@localhost/payments"}


class _AbortedError(Exception):
    pass


class FakeContext:
    """Minimal ``grpc.ServicerContext``: metadata in, ``abort`` raises like grpcio."""

    def __init__(self, metadata: Sequence[tuple[str, str | bytes]] = ()) -> None:
        self._metadata = tuple(metadata)
        self.code: grpc.StatusCode | None = None
        self.details: str | None = None

    def invocation_metadata(self) -> tuple[tuple[str, str | bytes], ...]:
        return self._metadata

    def abort(self, code: grpc.StatusCode, details: str) -> None:
        self.code = code
        self.details = details
        raise _AbortedError(details)


def _bearer(token: str) -> FakeContext:
    return FakeContext([("authorization", f"Bearer {token}")])


def _vector_settings(**overrides: str) -> IdentitySettings:
    values = {
        "IDENTITY__ISSUER": VECTORS["issuer"],
        "IDENTITY__AUDIENCE": VECTORS["audience"],
        "IDENTITY__JWKS_FILE": str(JWKS_FILE),
        "IDENTITY__SERVICE_ACCOUNTS": VECTORS["service_accounts"],
    }
    values.update(overrides)
    with patch.dict(os.environ, {**REQUIRED_ENV, **values}, clear=True):
        return IdentitySettings(_env_file=None)  # type: ignore[call-arg]


def _vector_resolver(clock: float | None = None) -> ServiceTokenSubjectResolver:
    if clock is None:
        resolver = create_subject_resolver(_vector_settings())
    else:
        resolver = create_subject_resolver(_vector_settings(), clock=lambda: clock)
    assert isinstance(resolver, ServiceTokenSubjectResolver)
    return resolver


class ServiceTokenVectorsTest(unittest.TestCase):
    """AC-459, AC-460, AC-461: shared vectors of ``tests/identity``."""

    @override
    def setUp(self) -> None:
        self.resolver = _vector_resolver()

    def _resolve(self, context: FakeContext) -> Subject:
        return self.resolver.resolve(cast(grpc.ServicerContext, context))

    def _assert_rejected(self, context: FakeContext) -> None:
        with (
            self.assertLogs("infrastructure.grpc.identity", level="WARNING"),
            self.assertRaises(_AbortedError),
        ):
            self._resolve(context)
        self.assertEqual(context.code, grpc.StatusCode.UNAUTHENTICATED)
        self.assertEqual(context.details, "invalid caller identity")

    def test_vectors_cover_accepted_and_rejected_tokens(self) -> None:
        self.assertEqual(VECTORS["accepted"], ["valid", "valid_string_aud"])
        self.assertTrue(VECTORS["rejected"])

    def test_accepted_vectors_resolve_to_orders_worker(self) -> None:
        for name in VECTORS["accepted"]:
            with self.subTest(vector=name):
                subject = self._resolve(_bearer(VECTORS["tokens"][name]))
                self.assertEqual(subject, ORDERS_WORKER)

    def test_rejected_vectors_are_unauthenticated(self) -> None:
        for name in VECTORS["rejected"]:
            with self.subTest(vector=name):
                self._assert_rejected(_bearer(VECTORS["tokens"][name]))

    def test_missing_authorization_is_rejected(self) -> None:
        self._assert_rejected(FakeContext())

    def test_non_bearer_scheme_is_rejected(self) -> None:
        token = VECTORS["tokens"]["valid"]
        for value in (f"Basic {token}", token, "Bearer ", f"Token {token}"):
            with self.subTest(value=value[:12]):
                self._assert_rejected(FakeContext([("authorization", value)]))

    def test_multiple_authorization_values_are_rejected(self) -> None:
        token = VECTORS["tokens"]["valid"]
        context = FakeContext([("authorization", f"Bearer {token}")] * 2)
        self._assert_rejected(context)

    def test_malformed_token_is_rejected(self) -> None:
        self._assert_rejected(_bearer("not-a-jwt"))

    def test_hmac_token_is_rejected(self) -> None:
        # HS256 with the kid of the cluster key: an algorithm-confusion attempt.
        token = jwt.encode(
            _claims(), "s" * 64, algorithm="HS256", headers={"kid": "nomnom-test-key"}
        )
        self._assert_rejected(_bearer(token))

    def test_rejection_reason_is_logged_not_returned(self) -> None:
        context = _bearer(VECTORS["tokens"]["unknown_subject"])
        with (
            self.assertLogs("infrastructure.grpc.identity", level="WARNING") as logs,
            self.assertRaises(_AbortedError),
        ):
            self._resolve(context)
        self.assertIn("not allowed", "\n".join(logs.output))
        self.assertEqual(context.details, "invalid caller identity")


class ServiceTokenClockTest(unittest.TestCase):
    """``exp`` > now − 30 s and ``nbf`` ≤ now + 30 s with an injected clock."""

    def _accepts(self, now: float) -> bool:
        resolver = _vector_resolver(clock=now)
        context = _bearer(VECTORS["tokens"]["valid"])
        try:
            with patch.object(identity.logger, "warning"):
                resolver.resolve(cast(grpc.ServicerContext, context))
        except _AbortedError:
            return False
        return True

    def test_expiry_leeway_is_thirty_seconds(self) -> None:
        self.assertTrue(self._accepts(VALID_EXP + 29))
        self.assertFalse(self._accepts(VALID_EXP + 30))

    def test_not_before_leeway_is_thirty_seconds(self) -> None:
        self.assertTrue(self._accepts(VALID_NBF - 30))
        self.assertFalse(self._accepts(VALID_NBF - 31))


def _claims(**overrides: object) -> dict[str, object]:
    claims: dict[str, object] = {
        "iss": VECTORS["issuer"],
        "aud": [VECTORS["audience"]],
        "sub": "system:serviceaccount:nomnom:orders-worker",
        "nbf": VALID_NBF,
        "exp": VALID_EXP,
    }
    claims.update(overrides)
    return claims


class EcdsaTokenTest(unittest.TestCase):
    @override
    def setUp(self) -> None:
        self.private_key = ec.generate_private_key(ec.SECP256R1())
        jwk = json.loads(ECAlgorithm.to_jwk(self.private_key.public_key()))
        jwk.update({"kid": "ec-key", "alg": "ES256", "use": "sig"})
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        jwks_file = Path(self.directory.name) / "jwks.json"
        jwks_file.write_text(json.dumps({"keys": [jwk]}), encoding="utf-8")
        self.resolver = ServiceTokenSubjectResolver(
            issuer=VECTORS["issuer"],
            audience=VECTORS["audience"],
            keys=load_jwks(jwks_file),
            service_accounts={"system:serviceaccount:nomnom:orders-worker": "orders-worker"},
        )

    def test_es256_token_is_accepted(self) -> None:
        token = jwt.encode(
            _claims(), self.private_key, algorithm="ES256", headers={"kid": "ec-key"}
        )
        context = cast(grpc.ServicerContext, _bearer(token))
        self.assertEqual(self.resolver.resolve(context), ORDERS_WORKER)

    def test_token_without_kid_is_rejected(self) -> None:
        token = jwt.encode(_claims(), self.private_key, algorithm="ES256")
        context = _bearer(token)
        with (
            self.assertLogs("infrastructure.grpc.identity", level="WARNING"),
            self.assertRaises(_AbortedError),
        ):
            self.resolver.resolve(cast(grpc.ServicerContext, context))
        self.assertEqual(context.code, grpc.StatusCode.UNAUTHENTICATED)

    def test_es384_token_is_rejected(self) -> None:
        key = ec.generate_private_key(ec.SECP384R1())
        token = jwt.encode(_claims(), key, algorithm="ES384", headers={"kid": "ec-key"})
        context = _bearer(token)
        with (
            self.assertLogs("infrastructure.grpc.identity", level="WARNING"),
            self.assertRaises(_AbortedError),
        ):
            self.resolver.resolve(cast(grpc.ServicerContext, context))


class IdentityConfigTest(unittest.TestCase):
    """FR-432, AC-462: fail closed without configuration, fail startup on invalid config."""

    def test_identity_is_disabled_by_default(self) -> None:
        with patch.dict(os.environ, REQUIRED_ENV, clear=True):
            self.assertFalse(load_settings().identity.enabled)

    def test_reads_identity_from_environment(self) -> None:
        settings = _vector_settings()
        self.assertTrue(settings.enabled)
        self.assertEqual(settings.issuer, VECTORS["issuer"])
        self.assertEqual(settings.audience, "nomnom.payments")
        self.assertEqual(settings.jwks_file, str(JWKS_FILE))
        self.assertEqual(
            settings.service_account_map,
            {"system:serviceaccount:nomnom:orders-worker": "orders-worker"},
        )

    def test_parses_several_service_accounts(self) -> None:
        settings = _vector_settings(
            IDENTITY__SERVICE_ACCOUNTS=" nomnom/orders-worker=orders-worker , ops/job=refunds-job"
        )
        self.assertEqual(
            settings.service_account_map,
            {
                "system:serviceaccount:nomnom:orders-worker": "orders-worker",
                "system:serviceaccount:ops:job": "refunds-job",
            },
        )

    def test_missing_issuer_audience_or_jwks_keeps_rejecting_resolver(self) -> None:
        for env in ("IDENTITY__ISSUER", "IDENTITY__AUDIENCE", "IDENTITY__JWKS_FILE"):
            with self.subTest(empty=env):
                overrides = {env: ""}
                if env == "IDENTITY__ISSUER":
                    overrides["IDENTITY__SERVICE_ACCOUNTS"] = ""
                with self.assertLogs("infrastructure.grpc.identity", level="WARNING"):
                    resolver = create_subject_resolver(_vector_settings(**overrides))
                self.assertIsInstance(resolver, RejectingSubjectResolver)

    def test_without_issuer_valid_token_is_unauthenticated(self) -> None:
        with patch.dict(os.environ, REQUIRED_ENV, clear=True):
            settings = load_settings().identity
        with self.assertLogs("infrastructure.grpc.identity", level="WARNING"):
            resolver = create_subject_resolver(settings)
        context = _bearer(VECTORS["tokens"]["valid"])
        with self.assertRaises(_AbortedError):
            resolver.resolve(cast(grpc.ServicerContext, context))
        self.assertEqual(context.code, grpc.StatusCode.UNAUTHENTICATED)

    def test_issuer_with_empty_allowlist_is_a_config_error(self) -> None:
        with self.assertRaises(ValidationError):
            _vector_settings(IDENTITY__SERVICE_ACCOUNTS="")
        with self.assertRaises(ValidationError):
            _vector_settings(IDENTITY__SERVICE_ACCOUNTS="", IDENTITY__AUDIENCE="")

    def test_malformed_allowlist_is_a_config_error(self) -> None:
        for value in (
            "orders-worker",
            "nomnom/orders-worker",
            "nomnom=orders-worker",
            "nomnom/orders-worker=",
            "nomnom:x/orders-worker=orders-worker",
            "nomnom/orders-worker=orders-worker,",
            "nomnom/orders-worker=a,nomnom/orders-worker=b",
        ):
            with self.subTest(value=value), self.assertRaises(ValidationError):
                _vector_settings(IDENTITY__SERVICE_ACCOUNTS=value)

    def test_malformed_allowlist_fails_load_settings(self) -> None:
        environment = {"IDENTITY__SERVICE_ACCOUNTS": "broken"}
        with (
            patch.dict(os.environ, {**REQUIRED_ENV, **environment}, clear=True),
            self.assertRaises(ValidationError),
        ):
            load_settings()

    def test_unreadable_or_invalid_jwks_is_a_startup_error(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            invalid = {
                "missing.json": None,
                "not-json.json": "{",
                "no-keys.json": json.dumps({"keys": []}),
                "not-a-set.json": json.dumps([]),
                "hmac-only.json": json.dumps(
                    {"keys": [{"kty": "oct", "kid": "h", "alg": "HS256", "k": "c2VjcmV0"}]}
                ),
            }
            for name, content in invalid.items():
                path = Path(directory) / name
                if content is not None:
                    path.write_text(content, encoding="utf-8")
                with self.subTest(jwks=name), self.assertRaises(IdentityConfigError):
                    create_subject_resolver(_vector_settings(IDENTITY__JWKS_FILE=str(path)))

    def test_default_worker_fails_before_listening_on_invalid_jwks(self) -> None:
        container = payments_container()
        self.addCleanup(close_container, container)
        container.config.identity.from_value(
            _vector_settings(IDENTITY__JWKS_FILE="/nonexistent/jwks.json")
        )
        with self.assertRaises(IdentityConfigError):
            container.grpc_worker()


def _handlers(kit: PaymentsTestKit) -> PaymentsHandlers:
    executor = kit.executor()
    return PaymentsHandlers(
        get_payment_status=GetPaymentStatusHandler(kit.payments, kit.permissions),
        create_payment=CreatePaymentHandler(kit.payments, kit.permissions, kit.ids),
        authorize_payment=AuthorizePaymentHandler(executor),
        capture_payment=CapturePaymentHandler(executor),
        cancel_authorization=CancelAuthorizationHandler(executor),
        refund_payment=RefundPaymentHandler(executor),
    )


class ServiceIdentityGrpcTest(unittest.TestCase):
    """AC-459 end to end: the token travels as gRPC metadata and reaches the use case."""

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit()
        self.worker = GrpcWorker(("127.0.0.1", 0), _handlers(self.kit), _vector_resolver())
        self.worker.start()
        self.channel = grpc.insecure_channel(f"127.0.0.1:{self.worker.port}")
        self.client = service_pb2_grpc.PaymentsServiceStub(self.channel)

    @override
    def tearDown(self) -> None:
        self.channel.close()
        self.worker.shutdown(time.monotonic() + 1)

    def test_valid_token_reaches_handler_as_orders_worker(self) -> None:
        self.kit.given_payment(PaymentState.AUTHORIZED, payment_id="payment-1")

        response = self.client.GetPaymentStatus(
            service_pb2.GetPaymentStatusRequest(payment_id="payment-1"),
            metadata=(("authorization", f"Bearer {VECTORS['tokens']['valid']}"),),
        )

        self.assertEqual(response.payment.state, payments_pb2.PAYMENT_STATE_AUTHORIZED)
        self.assertEqual([check[0] for check in self.kit.permissions.checks], [ORDERS_WORKER])

    def test_token_of_other_context_is_unauthenticated(self) -> None:
        with (
            self.assertLogs("infrastructure.grpc.identity", level="WARNING"),
            self.assertRaises(grpc.RpcError) as raised,
        ):
            self.client.GetPaymentStatus(
                service_pb2.GetPaymentStatusRequest(payment_id="payment-1"),
                metadata=(("authorization", f"Bearer {VECTORS['tokens']['wrong_audience']}"),),
            )
        error = cast(grpc.Call, raised.exception)
        self.assertEqual(error.code(), grpc.StatusCode.UNAUTHENTICATED)
        self.assertEqual(error.details(), "invalid caller identity")
        self.assertEqual(self.kit.permissions.checks, [])


if __name__ == "__main__":
    unittest.main()
