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
import pytest
from cryptography.hazmat.primitives.asymmetric import ec
from jwt.algorithms import ECAlgorithm  # ty: ignore[possibly-missing-import] - defined when cryptography is installed
from payment_fakes import PaymentsTestKit
from pydantic import ValidationError
from test_service_identity import (
    FIXTURES,
    JWKS_FILE,
    REQUIRED_ENV,
    FakeContext,
    _AbortedError,
    _handlers,
)

from infrastructure.grpc.identity import (
    USER_ASSERTION_METADATA,
    IdentityConfigError,
    ServiceTokenSubjectResolver,
    ServiceTokenVerifier,
    UserAssertionVerifier,
    create_subject_resolver,
    load_jwks,
    load_user_jwks,
)
from infrastructure.grpc.service import (
    GrpcWorker,
    payments_pb2,
    service_pb2,
    service_pb2_grpc,
)
from infrastructure.postgres import PostgresDatabase
from pkg.application import Subject, SubjectKind
from pkg.config import IdentitySettings, load_settings
from slices.payment.domains.payment import PaymentState

# Shared vectors of ADR 32: user assertions are checked with the clock fixed on ``now``.
BFF_JWKS_FILE = FIXTURES / "bff-jwks.json"
USER_VECTORS: dict[str, Any] = json.loads(
    (FIXTURES / "user-vectors.json").read_text(encoding="utf-8")
)
NOW = float(USER_VECTORS["now"])
CUSTOMER = Subject(SubjectKind.USER, USER_VECTORS["expected_subject"])
BFF = Subject(SubjectKind.SERVICE, "bff")
LOGGER = "infrastructure.grpc.identity"


def _metadata(service_token: str, *assertions: str) -> list[tuple[str, str]]:
    metadata = [("authorization", f"Bearer {service_token}")]
    metadata.extend((USER_ASSERTION_METADATA, assertion) for assertion in assertions)
    return metadata


def _context(service_token: str, *assertions: str) -> FakeContext:
    return FakeContext(_metadata(service_token, *assertions))


def _settings(*, user: bool = True, **overrides: str) -> IdentitySettings:
    values = {
        "IDENTITY__ISSUER": USER_VECTORS["service_issuer"],
        "IDENTITY__AUDIENCE": USER_VECTORS["audience"],
        "IDENTITY__JWKS_FILE": str(JWKS_FILE),
        "IDENTITY__SERVICE_ACCOUNTS": USER_VECTORS["service_accounts"],
    }
    if user:
        values.update(
            {
                "IDENTITY__USER_ISSUER": USER_VECTORS["issuer"],
                "IDENTITY__USER_JWKS_FILE": str(BFF_JWKS_FILE),
                "IDENTITY__USER_DELEGATES": USER_VECTORS["delegates"],
            }
        )
    values.update(overrides)
    with patch.dict(os.environ, {**REQUIRED_ENV, **values}, clear=True):
        return IdentitySettings(_env_file=None)  # type: ignore[call-arg]


def _resolver(*, user: bool = True, **overrides: str) -> ServiceTokenSubjectResolver:
    resolver = create_subject_resolver(_settings(user=user, **overrides), clock=lambda: NOW)
    assert isinstance(resolver, ServiceTokenSubjectResolver)
    return resolver


class _ResolverCase(unittest.TestCase):
    resolver: ServiceTokenSubjectResolver

    def _resolve(self, context: FakeContext) -> Subject:
        return self.resolver.resolve(cast(grpc.ServicerContext, context))

    def _assert_rejected(self, context: FakeContext) -> list[str]:
        with self.assertLogs(LOGGER, level="WARNING") as logs, self.assertRaises(_AbortedError):
            self._resolve(context)
        self.assertEqual(context.code, grpc.StatusCode.UNAUTHENTICATED)
        self.assertEqual(context.details, "invalid caller identity")
        return logs.output


class UserAssertionVectorsTest(_ResolverCase):
    """FR-471, FR-472, AC-525–AC-528: shared vectors of ``tests/identity``."""

    @override
    def setUp(self) -> None:
        self.resolver = _resolver()

    def test_vectors_cover_accepted_and_rejected_assertions(self) -> None:
        self.assertEqual(USER_VECTORS["accepted"], ["valid", "valid_string_aud"])
        self.assertIn("ttl_too_long", USER_VECTORS["rejected"])
        self.assertIn("alg_none", USER_VECTORS["rejected"])

    def test_accepted_vectors_resolve_to_user_with_delegate(self) -> None:
        for name in USER_VECTORS["accepted"]:
            with self.subTest(vector=name):
                context = _context(USER_VECTORS["delegate_token"], USER_VECTORS["tokens"][name])
                with self.assertLogs(LOGGER, level="INFO") as logs:
                    subject = self._resolve(context)
                self.assertEqual(subject, CUSTOMER)
                self.assertEqual(subject.kind, SubjectKind.USER)
                self.assertEqual(subject.delegate, "bff")
                output = "\n".join(logs.output)
                self.assertIn("delegate=bff", output)
                self.assertIn("jti=0b6f1c1e-6d2a-4d63-9a59-1f6c7f2d3a10", output)
                self.assertNotIn(USER_VECTORS["tokens"][name], output)

    def test_rejected_vectors_are_unauthenticated(self) -> None:
        for name in USER_VECTORS["rejected"]:
            with self.subTest(vector=name):
                token = USER_VECTORS["tokens"][name]
                output = self._assert_rejected(_context(USER_VECTORS["delegate_token"], token))
                self.assertNotIn(token, "\n".join(output))

    def test_valid_assertion_from_non_delegate_is_unauthenticated(self) -> None:
        # AC-526: orders-worker is an allowed service but not a user delegate.
        context = _context(USER_VECTORS["non_delegate_token"], USER_VECTORS["tokens"]["valid"])
        output = self._assert_rejected(context)
        self.assertIn("not a user delegate", "\n".join(output))

    def test_assertion_without_service_token_is_unauthenticated(self) -> None:
        context = FakeContext([(USER_ASSERTION_METADATA, USER_VECTORS["tokens"]["valid"])])
        self._assert_rejected(context)

    def test_delegate_without_assertion_is_the_service_subject(self) -> None:
        subject = self._resolve(_context(USER_VECTORS["delegate_token"]))
        self.assertEqual(subject, BFF)
        self.assertIsNone(subject.delegate)

    def test_repeated_or_empty_assertion_is_unauthenticated(self) -> None:
        valid = USER_VECTORS["tokens"]["valid"]
        for assertions in ((valid, valid), ("",), (" ",)):
            with self.subTest(count=len(assertions)):
                self._assert_rejected(_context(USER_VECTORS["delegate_token"], *assertions))

    def test_binary_assertion_is_unauthenticated(self) -> None:
        context = FakeContext(
            [
                ("authorization", f"Bearer {USER_VECTORS['delegate_token']}"),
                (USER_ASSERTION_METADATA, USER_VECTORS["tokens"]["valid"].encode()),
            ]
        )
        self._assert_rejected(context)

    def test_malformed_assertion_is_unauthenticated(self) -> None:
        self._assert_rejected(_context(USER_VECTORS["delegate_token"], "not-a-jwt"))


class UserAssertionClockTest(unittest.TestCase):
    """``exp`` > now − 30 s and ``nbf`` ≤ now + 30 s for assertions (iat = nbf = now − 10)."""

    def _accepts(self, now: float) -> bool:
        # The service token keeps the fixed clock: only the assertion moves in time.
        resolver = ServiceTokenSubjectResolver(
            ServiceTokenVerifier(
                issuer=USER_VECTORS["service_issuer"],
                audience=USER_VECTORS["audience"],
                keys=load_jwks(JWKS_FILE),
                service_accounts=_settings().service_account_map,
                clock=lambda: NOW,
            ),
            user_assertions=UserAssertionVerifier(
                issuer=USER_VECTORS["issuer"],
                audience=USER_VECTORS["audience"],
                keys=load_user_jwks(BFF_JWKS_FILE),
                clock=lambda: now,
            ),
            user_delegates={"bff"},
        )
        context = _context(USER_VECTORS["delegate_token"], USER_VECTORS["tokens"]["valid"])
        try:
            with self.assertLogs(LOGGER, level="INFO"):
                resolver.resolve(cast(grpc.ServicerContext, context))
        except _AbortedError:
            return False
        return True

    def test_expiry_leeway_is_thirty_seconds(self) -> None:
        expires_at = 1_700_000_030
        self.assertTrue(self._accepts(expires_at + 29))
        self.assertFalse(self._accepts(expires_at + 30))

    def test_not_before_leeway_is_thirty_seconds(self) -> None:
        not_before = 1_700_000_000
        self.assertTrue(self._accepts(not_before - 30))
        self.assertFalse(self._accepts(not_before - 31))


class SignedAssertionTest(_ResolverCase):
    """Rules the static vectors do not pin: padded sub, blank jti, P-256 only."""

    @override
    def setUp(self) -> None:
        self.private_key = ec.generate_private_key(ec.SECP256R1())
        jwk = json.loads(ECAlgorithm.to_jwk(self.private_key.public_key()))
        jwk.update({"kid": "bff-key", "alg": "ES256", "use": "sig"})
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        jwks_file = Path(self.directory.name) / "bff-jwks.json"
        jwks_file.write_text(json.dumps({"keys": [jwk]}), encoding="utf-8")
        self.resolver = _resolver(IDENTITY__USER_JWKS_FILE=str(jwks_file))

    def _assertion(self, **overrides: object) -> str:
        claims: dict[str, object] = {
            "iss": USER_VECTORS["issuer"],
            "aud": USER_VECTORS["audience"],
            "sub": "customer-1",
            "jti": "jti-1",
            "iat": int(NOW),
            "nbf": int(NOW),
            "exp": int(NOW) + 60,
        }
        claims.update(overrides)
        payload = {key: value for key, value in claims.items() if value is not None}
        return jwt.encode(payload, self.private_key, algorithm="ES256", headers={"kid": "bff-key"})

    def test_sixty_second_lifetime_is_accepted(self) -> None:
        context = _context(USER_VECTORS["delegate_token"], self._assertion())
        with self.assertLogs(LOGGER, level="INFO"):
            self.assertEqual(self._resolve(context), CUSTOMER)

    def test_invalid_claims_are_unauthenticated(self) -> None:
        cases: dict[str, dict[str, object]] = {
            "ttl_61s": {"exp": int(NOW) + 61},
            "padded_sub": {"sub": " customer-1"},
            "blank_sub": {"sub": "   "},
            "blank_jti": {"jti": " "},
            "numeric_sub": {"sub": 1},
            "missing_exp": {"exp": None},
            "audience_list_without_payments": {"aud": ["nomnom.orders", "nomnom.delivery"]},
        }
        for name, overrides in cases.items():
            with self.subTest(case=name):
                context = _context(USER_VECTORS["delegate_token"], self._assertion(**overrides))
                self._assert_rejected(context)

    def test_audience_list_containing_payments_is_accepted(self) -> None:
        assertion = self._assertion(aud=["nomnom.orders", USER_VECTORS["audience"]])
        context = _context(USER_VECTORS["delegate_token"], assertion)
        with self.assertLogs(LOGGER, level="INFO"):
            self.assertEqual(self._resolve(context), CUSTOMER)

    def test_assertion_without_kid_is_unauthenticated(self) -> None:
        payload = jwt.decode(self._assertion(), options={"verify_signature": False})
        token = jwt.encode(payload, self.private_key, algorithm="ES256")
        self._assert_rejected(_context(USER_VECTORS["delegate_token"], token))


class UserIdentityConfigTest(_ResolverCase):
    """FR-473, AC-529: user assertions are opt-in, an invalid BFF JWKS stops startup."""

    def test_user_assertions_are_disabled_by_default(self) -> None:
        with patch.dict(os.environ, REQUIRED_ENV, clear=True):
            identity = load_settings().identity
        self.assertFalse(identity.user_assertions_enabled)
        self.assertEqual(identity.user_delegate_set, frozenset())

    def test_reads_user_identity_from_environment(self) -> None:
        settings = _settings(IDENTITY__USER_DELEGATES=" bff , admin-bff ")
        self.assertTrue(settings.user_assertions_enabled)
        self.assertEqual(settings.user_issuer, "nomnom-bff")
        self.assertEqual(settings.user_jwks_file, str(BFF_JWKS_FILE))
        self.assertEqual(settings.user_delegate_set, frozenset({"bff", "admin-bff"}))

    def test_malformed_delegates_are_a_config_error(self) -> None:
        for value in ("bff,", ",bff", "bff,,orders", "bff orders", "bff,bff"):
            with self.subTest(value=value), self.assertRaises(ValidationError):
                _settings(IDENTITY__USER_DELEGATES=value)

    def test_without_user_settings_assertion_is_rejected_but_service_call_passes(self) -> None:
        # AC-529: the same delegate call is the service subject bff without an assertion.
        self.resolver = _resolver(user=False)
        output = self._assert_rejected(
            _context(USER_VECTORS["delegate_token"], USER_VECTORS["tokens"]["valid"])
        )
        self.assertIn("not configured", "\n".join(output))
        self.assertEqual(self._resolve(_context(USER_VECTORS["delegate_token"])), BFF)

    def test_partial_user_settings_are_a_config_error(self) -> None:
        for env in (
            "IDENTITY__USER_ISSUER",
            "IDENTITY__USER_JWKS_FILE",
            "IDENTITY__USER_DELEGATES",
        ):
            with self.subTest(empty=env), self.assertRaises(IdentityConfigError):
                create_subject_resolver(_settings(user=True, **{env: ""}))

    def test_unreadable_or_invalid_user_jwks_is_a_startup_error(self) -> None:
        p384 = json.loads(ECAlgorithm.to_jwk(ec.generate_private_key(ec.SECP384R1()).public_key()))
        p384.update({"kid": "p384", "alg": "ES256", "use": "sig"})
        with tempfile.TemporaryDirectory() as directory:
            invalid = {
                "missing.json": None,
                "not-json.json": "{",
                "no-keys.json": json.dumps({"keys": []}),
                # The cluster JWKS: RS256 keys are not BFF assertion keys.
                "rsa-only.json": JWKS_FILE.read_text(encoding="utf-8"),
                "p384.json": json.dumps({"keys": [p384]}),
            }
            for name, content in invalid.items():
                path = Path(directory) / name
                if content is not None:
                    path.write_text(content, encoding="utf-8")
                with self.subTest(jwks=name), self.assertRaises(IdentityConfigError):
                    create_subject_resolver(_settings(IDENTITY__USER_JWKS_FILE=str(path)))

    def test_bff_jwks_fixture_loads(self) -> None:
        self.assertEqual(list(load_user_jwks(BFF_JWKS_FILE)), ["nomnom-bff-test-key"])


@pytest.mark.usefixtures("postgres")
class UserIdentityGrpcTest(unittest.TestCase):
    """AC-525 end to end: the use case sees ``user:<sub>`` from the metadata of BFF."""

    database: PostgresDatabase

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.worker = GrpcWorker(("127.0.0.1", 0), _handlers(self.kit), _resolver())
        self.worker.start()
        self.channel = grpc.insecure_channel(f"127.0.0.1:{self.worker.port}")
        self.client = service_pb2_grpc.PaymentsServiceStub(self.channel)

    @override
    def tearDown(self) -> None:
        self.channel.close()
        self.worker.shutdown(time.monotonic() + 1)

    def _get(self, metadata: Sequence[tuple[str, str]]) -> service_pb2.GetPaymentStatusResponse:
        return self.client.GetPaymentStatus(
            service_pb2.GetPaymentStatusRequest(payment_id="payment-1"),
            metadata=tuple(metadata),
        )

    def test_assertion_of_bff_reaches_handler_as_user(self) -> None:
        self.kit.given_payment(PaymentState.AUTHORIZED, payment_id="payment-1")

        with self.assertLogs(LOGGER, level="INFO"):
            response = self._get(
                _metadata(USER_VECTORS["delegate_token"], USER_VECTORS["tokens"]["valid"])
            )

        self.assertEqual(response.payment.state, payments_pb2.PAYMENT_STATE_AUTHORIZED)
        self.assertEqual([check[0] for check in self.kit.permissions.checks], [CUSTOMER])

    def test_assertion_of_other_workload_is_unauthenticated(self) -> None:
        with self.assertLogs(LOGGER, level="WARNING"), self.assertRaises(grpc.RpcError) as raised:
            self._get(
                _metadata(USER_VECTORS["non_delegate_token"], USER_VECTORS["tokens"]["valid"])
            )
        error = cast(grpc.Call, raised.exception)
        self.assertEqual(error.code(), grpc.StatusCode.UNAUTHENTICATED)
        self.assertEqual(self.kit.permissions.checks, [])


if __name__ == "__main__":
    unittest.main()
