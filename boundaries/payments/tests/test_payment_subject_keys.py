"""Crypto-shredding of user identifiers in the Payment streams over a real PostgreSQL."""

import io
import json
import os
import unittest
from contextlib import redirect_stdout
from dataclasses import replace
from typing import Any, LiteralString, override
from unittest import mock

import pytest
from payment_fakes import NOW, SUBJECT_KEK, SUBJECT_REF_KEY, PaymentsTestKit

from entrypoints.payments_admin import main as admin
from infrastructure.repository import Database
from pkg.authz import Subject, SubjectKind
from slices.payment.applications.authorize import (
    AuthorizePaymentHandler,
    AuthorizePaymentInput,
)
from slices.payment.domains.payment import (
    OperationId,
    OperationKind,
    OperationRequester,
    PaymentId,
    RequesterKind,
    SealedSubject,
)
from slices.payment.infrastructure.repository import (
    DatabaseSubjectKeys,
    ProjectionRebuilder,
    UnreadableStoredDataError,
)

USER = "operator-1"
OTHER_KEK = "another-subject-kek-with-at-least-32-characters"


@pytest.mark.usefixtures("database")
class SubjectKeysTest(unittest.TestCase):
    database: Database
    uri: str

    @override
    def setUp(self) -> None:
        self.kit = PaymentsTestKit(self.database)
        self.keys = self.kit.subjects

    def rows(self, query: LiteralString, *params: Any) -> list[tuple[Any, ...]]:
        with self.database.connection() as conn:
            return conn.execute(query, params).fetchall()

    def authorized_by_user(self, payment: str = "payment-1", user: str = USER) -> PaymentId:
        payment_id = self.kit.given_payment(payment_id=payment, order_id=f"order-{payment}")
        AuthorizePaymentHandler(self.kit.executor()).handle(
            AuthorizePaymentInput(
                Subject(SubjectKind.USER, user, delegate="bff"), payment_id, f"{payment}:authorize"
            )
        )
        return payment_id

    def requester(self, payment_id: PaymentId) -> OperationRequester:
        requester = self.kit.requester_of(payment_id)
        assert requester is not None
        return requester

    def sealed(self, payment_id: PaymentId) -> SealedSubject:
        identifier = self.requester(payment_id).id
        assert isinstance(identifier, SealedSubject)
        return identifier

    def test_round_trip_and_one_key_per_user(self) -> None:
        first, second = self.keys.seal(USER), self.keys.seal(USER)

        self.assertEqual(first.key_id, second.key_id)
        self.assertNotEqual(first.nonce, second.nonce)
        self.assertNotEqual(first.ciphertext, second.ciphertext)
        self.assertEqual(self.keys.reveal(first), USER)
        self.assertEqual(self.keys.reveal(second), USER)
        self.assertNotEqual(self.keys.seal("operator-2").key_id, first.key_id)

    def test_identifier_is_nowhere_in_plain_text(self) -> None:
        payment_id = self.authorized_by_user()
        for table, column in (
            ("payment_events", "payload"),
            ("payment_snapshots", "state"),
            ("subject_keys", "subject_ref"),
            ("subject_keys", "wrapped_key"),
        ):
            with self.database.connection() as conn:
                rows = conn.execute(f"SELECT {column}::text FROM {table}").fetchall()  # noqa: S608 - fixed names
            with self.subTest(table=table, column=column):
                self.assertNotIn(USER, " ".join(str(row[0]) for row in rows))
        self.assertEqual(self.requester(payment_id).delegate, "bff")

    def test_service_identifier_stays_plain_and_needs_no_key(self) -> None:
        payment_id = self.kit.given_payment()
        AuthorizePaymentHandler(self.kit.executor()).handle(
            AuthorizePaymentInput(
                Subject(SubjectKind.SERVICE, "orders-worker"), payment_id, "key-1"
            )
        )
        self.assertEqual(
            self.requester(payment_id), OperationRequester(RequesterKind.SERVICE, "orders-worker")
        )
        self.assertEqual(self.rows("SELECT key_id FROM subject_keys"), [])

    def test_forgotten_user_stays_unreadable_and_payments_keep_working(self) -> None:
        payment_id = self.authorized_by_user()
        sealed = self.sealed(payment_id)
        events = self.rows("SELECT * FROM payment_events ORDER BY version")

        self.assertTrue(self.keys.forget(USER))

        self.assertIsNone(self.keys.reveal(sealed))
        self.assertEqual(self.rows("SELECT * FROM payment_events ORDER BY version"), events)
        ((reference, key, forgotten),) = self.rows(
            "SELECT subject_ref, wrapped_key, forgotten_at IS NOT NULL FROM subject_keys"
        )
        self.assertEqual((reference, key, forgotten), (None, None, True))
        # Загрузка, снапшоты, пересборка и новые операции ключа не требуют.
        rebuilder = ProjectionRebuilder(self.database, 2)
        rebuilder.rebuild(snapshots=True)
        self.assertEqual(rebuilder.check(), [])
        stored = self.kit.payment(payment_id)
        operation = stored.request_operation(
            OperationId("op-c"),
            OperationKind.CAPTURE,
            "key-c",
            NOW,
            OperationRequester(RequesterKind.SERVICE, "orders-worker"),
        )
        self.kit.payments.save(stored)
        self.assertEqual(self.kit.payment(payment_id).operations[-1].id, operation.id)

    def test_returning_user_gets_a_new_key_and_old_records_stay_forgotten(self) -> None:
        old = self.keys.seal(USER)
        self.keys.forget(USER)

        new = self.keys.seal(USER)

        self.assertNotEqual(new.key_id, old.key_id)
        self.assertEqual(self.keys.reveal(new), USER)
        self.assertIsNone(self.keys.reveal(old))

    def test_forgetting_only_touches_that_user(self) -> None:
        other = self.keys.seal("operator-2")
        self.keys.seal(USER)
        self.keys.forget(USER)
        self.assertEqual(self.keys.reveal(other), "operator-2")

    def test_forgetting_an_unknown_user_changes_nothing(self) -> None:
        self.assertFalse(self.keys.forget("nobody"))
        self.keys.seal(USER)
        self.assertTrue(self.keys.forget(USER))
        self.assertFalse(self.keys.forget(USER))

    def test_rollback_removes_a_new_key(self) -> None:
        def work() -> None:
            self.keys.seal(USER)
            raise RuntimeError("boom")

        with self.assertRaises(RuntimeError):
            self.kit.uow.run(work)
        self.assertEqual(self.rows("SELECT key_id FROM subject_keys"), [])

    def test_tampered_ciphertext_is_an_error(self) -> None:
        sealed = self.keys.seal(USER)
        other = self.keys.seal("operator-2")
        for forged in (
            replace(sealed, ciphertext=other.ciphertext, nonce=other.nonce),
            replace(sealed, ciphertext="not base64 !!"),
            replace(sealed, key_id="missing"),
        ):
            with self.subTest(forged=forged), self.assertRaises(UnreadableStoredDataError):
                self.keys.reveal(forged)

    def test_another_kek_cannot_open_the_keys(self) -> None:
        sealed = self.keys.seal(USER)
        with self.assertRaisesRegex(UnreadableStoredDataError, "does not open"):
            DatabaseSubjectKeys(self.database, {1: OTHER_KEK}, SUBJECT_REF_KEY).reveal(sealed)

    def test_missing_kek_version_is_an_error(self) -> None:
        sealed = self.keys.seal(USER)
        with self.assertRaisesRegex(UnreadableStoredDataError, "not configured"):
            DatabaseSubjectKeys(self.database, {2: OTHER_KEK}, SUBJECT_REF_KEY).reveal(sealed)

    def test_config_without_keks_is_rejected(self) -> None:
        with self.assertRaises(ValueError):
            DatabaseSubjectKeys(self.database, {}, SUBJECT_REF_KEY)

    def rotated(self) -> DatabaseSubjectKeys:
        return DatabaseSubjectKeys(self.database, {1: SUBJECT_KEK, 2: OTHER_KEK}, SUBJECT_REF_KEY)

    def versions(self) -> list[int]:
        return [row[0] for row in self.rows("SELECT kek_version FROM subject_keys ORDER BY 1")]

    def test_rotation_keeps_old_records_readable_and_users_findable(self) -> None:
        sealed = self.keys.seal(USER)
        rotated = self.rotated()

        # Обе версии настроены: старый ключ читается и остаётся тем же для того же пользователя.
        self.assertEqual(rotated.reveal(sealed), USER)
        self.assertEqual(rotated.seal(USER).key_id, sealed.key_id)
        self.assertEqual(self.versions(), [1])
        # Новый пользователь сразу получает старшую версию.
        rotated.seal("operator-2")
        self.assertEqual(self.versions(), [1, 2])

        result = rotated.rewrap()

        self.assertEqual((result.rewrapped, result.remaining), (1, 0))
        self.assertEqual(self.versions(), [2, 2])
        # Старая версия больше не нужна, а шифротекст в журнале не менялся.
        without_old = DatabaseSubjectKeys(self.database, {2: OTHER_KEK}, SUBJECT_REF_KEY)
        self.assertEqual(without_old.reveal(sealed), USER)
        self.assertTrue(without_old.forget(USER))
        self.assertIsNone(without_old.reveal(sealed))

    def test_rewrap_skips_forgotten_keys_and_is_repeatable(self) -> None:
        self.keys.seal(USER)
        self.keys.forget(USER)
        rotated = self.rotated()

        self.assertEqual(rotated.rewrap().rewrapped, 0)
        self.assertEqual(rotated.rewrap().remaining, 0)

    def test_rewrap_moves_more_keys_than_one_batch(self) -> None:
        for number in range(105):
            self.keys.seal(f"operator-{number}")

        result = self.rotated().rewrap()

        self.assertEqual((result.rewrapped, result.remaining), (105, 0))

    def test_failed_rewrap_keeps_the_batch(self) -> None:
        self.keys.seal(USER)
        wrong = DatabaseSubjectKeys(self.database, {1: OTHER_KEK, 2: SUBJECT_KEK}, SUBJECT_REF_KEY)

        with self.assertRaises(UnreadableStoredDataError):
            wrong.rewrap()

        self.assertEqual(self.versions(), [1])

    def run_admin(self, *args: str) -> tuple[int, dict[str, Any]]:
        stdout = io.StringIO()
        env = {
            "DATABASE__URI": self.uri,
            "EVENT_STORE__SNAPSHOT_EVERY": "5",
            "EVENT_STORE__SUBJECT_KEKS": json.dumps({"1": SUBJECT_KEK}),
            "EVENT_STORE__SUBJECT_REF_KEY": SUBJECT_REF_KEY,
        }
        with (
            mock.patch.dict(os.environ, env, clear=True),
            mock.patch.object(admin.telemetry, "configure_process_logging"),
            redirect_stdout(stdout),
        ):
            code = admin.main(list(args))
        output = stdout.getvalue()
        return code, json.loads(output) if output else {}

    def test_admin_shows_history_and_forgets(self) -> None:
        payment_id = self.authorized_by_user()

        code, history = self.run_admin("payments", "history", payment_id)
        self.assertEqual(code, 0)
        self.assertEqual(history["state"], "authorized")
        self.assertEqual(
            history["operations"][0]["requested_by"],
            {"status": "known", "kind": "user", "id": USER, "delegate": "bff"},
        )

        self.assertEqual(self.run_admin("subjects", "forget", USER), (0, {"forgotten": True}))

        _, history = self.run_admin("payments", "history", payment_id)
        self.assertEqual(
            history["operations"][0]["requested_by"],
            {"status": "forgotten", "kind": "user", "id": None, "delegate": "bff"},
        )
        self.assertEqual(self.run_admin("subjects", "forget", USER), (0, {"forgotten": False}))
        self.assertEqual(
            self.run_admin("subjects", "rewrap"), (0, {"rewrapped": 0, "remaining": 0})
        )

    def test_admin_history_of_a_missing_payment_fails(self) -> None:
        with self.assertLogs(admin.logger, "ERROR"):
            self.assertEqual(self.run_admin("payments", "history", "missing"), (1, {}))
