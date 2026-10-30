"""Test kit of the payments application: real PostgreSQL storage plus non-storage fakes.

Storage is always the real PostgreSQL adapters (``tests/conftest.py`` provides the
database). Fakes stand in only for the provider, permissions, clock and identifiers;
thin wrappers over the real adapters inject faults (failing Outbox write, stale reads).
"""

import os
import pickle
from collections.abc import Callable, Mapping, Sequence
from datetime import UTC, datetime, timedelta
from typing import Any, TypeVar, override
from unittest.mock import patch

from dependency_injector import providers

from di.payments import PaymentsContainer
from di.resources import sections
from infrastructure.repository import (
    PAYMENT_EVENTS_TOPIC,
    Database,
    DatabaseUnitOfWork,
    OutboxMessage,
    create_database,
)
from pkg.authz import AuthorizationUnavailableError, Subject, SubjectKind
from pkg.config import database, load_settings
from pkg.outbox import AggregateRef
from slices.payment.applications.operation_flow import (
    PaymentOperationExecutor,
    ProviderResultApplier,
)
from slices.payment.applications.ports import (
    PaymentPermission,
    PaymentResource,
    ProviderOperationOutcome,
    ProviderOperationRequest,
    ProviderUnavailableError,
)
from slices.payment.domains.payment import (
    MarketId,
    Money,
    OperationId,
    OperationKind,
    OperationRequester,
    OperationState,
    OrderId,
    Payment,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentCreated,
    PaymentDomainEvent,
    PaymentFailed,
    PaymentId,
    PaymentOperation,
    PaymentOperationDeclined,
    PaymentOperationRequested,
    PaymentOperationSucceeded,
    PaymentRefunded,
    PaymentState,
    RequesterKind,
)
from slices.payment.infrastructure.observability import FsmMetrics
from slices.payment.infrastructure.repository import (
    DatabasePaymentOperationHistory,
    DatabasePaymentOutbox,
    DatabasePaymentRepository,
    DatabasePaymentStatusReader,
    DatabaseProviderMessageInbox,
    DatabaseSubjectKeys,
)
from slices.payment.infrastructure.repository.event_codec import decode_event, encode_event
from slices.payment.infrastructure.repository.event_store import append_events, read_events
from slices.payment.infrastructure.repository.repositories import project_payment

T = TypeVar("T")

ORCHESTRATOR = Subject(SubjectKind.SERVICE, "orders-activity-worker")
OPERATOR = Subject(SubjectKind.USER, "operator-1")
REQUESTER = OperationRequester(RequesterKind.SERVICE, ORCHESTRATOR.id)
NOW = datetime(2026, 10, 25, 10, 0, tzinfo=UTC)
# Market of the test Orders (the launch Market of ADR 41).
MARKET = MarketId("berlin")
# Снапшот почти на каждый commit: все сценарии use cases проходят и через него.
SNAPSHOT_EVERY = 2
# EVENT_STORE__SNAPSHOT_EVERY обязателен: без него load_settings падает.
SUBJECT_KEK = "test-subject-kek-with-at-least-32-characters"
REQUIRED_EVENT_STORE_ENV = {
    "EVENT_STORE__SNAPSHOT_EVERY": "5",
    "EVENT_STORE__SUBJECT_KEK": SUBJECT_KEK,
}
# Only facts with a contract reach the Outbox of the real mapper.
_UNPUBLISHED = (PaymentCreated, PaymentOperationRequested, PaymentOperationSucceeded)


class PickledEventsMapper:
    """Outbox mapper of the test kit: every published Domain Event is pickled as its payload.

    ``event_type`` is the event ``repr`` so that distinct events of one aggregate version
    get distinct event ids.
    """

    def map(self, aggregate: AggregateRef, event: PaymentDomainEvent) -> OutboxMessage | None:
        if isinstance(event, _UNPUBLISHED):
            return None
        return OutboxMessage(repr(event), PAYMENT_EVENTS_TOPIC, pickle.dumps(event))


class FailingOutbox:
    """Real Outbox whose next write can be made to fail."""

    def __init__(self, outbox: DatabasePaymentOutbox) -> None:
        self._outbox = outbox
        self.fail_next = False

    def add(self, aggregate: AggregateRef, events: Sequence[PaymentDomainEvent]) -> None:
        if self.fail_next:
            self.fail_next = False
            raise RuntimeError("outbox write failed")
        self._outbox.add(aggregate, events)


class CountingUnitOfWork:
    """Real Unit of Work that counts outer commits and rollbacks."""

    def __init__(self, uow: DatabaseUnitOfWork) -> None:
        self._uow = uow
        self._depth = 0
        self.commits = 0
        self.rollbacks = 0

    def run(self, work: Callable[[], T]) -> T:
        self._depth += 1
        try:
            result = self._uow.run(work)
        except BaseException:
            if self._depth == 1:
                self.rollbacks += 1
            raise
        finally:
            self._depth -= 1
        if self._depth == 0:
            self.commits += 1
        return result


class StalePaymentRepository(DatabasePaymentRepository):
    """Simulates a concurrent transaction committed after this one read the stream.

    While ``stale_events`` is set, the next ``get`` returns the Payment without that many
    latest events, as if they were committed right after the read.
    """

    def __init__(self, database: Database, snapshot_every: int) -> None:
        super().__init__(database, snapshot_every)
        self.stale_events = 0

    @override
    def get(self, payment_id: PaymentId) -> Payment | None:
        stale, self.stale_events = self.stale_events, 0
        if not stale:
            return super().get(payment_id)
        with self._db.connection() as conn:
            rows = read_events(conn, payment_id, 0)
        events = [decode_event(r.event_type, r.schema_version, r.payload) for r in rows]
        return Payment.from_history(None, 0, events[:-stale])


class FakePaymentProvider:
    """Provider double that deduplicates requests by idempotency key like a real one."""

    def __init__(self) -> None:
        self.requests: list[ProviderOperationRequest] = []
        self.effects: dict[str, ProviderOperationOutcome] = {}
        self.outcomes: dict[OperationKind, ProviderOperationOutcome] = {}
        self.unavailable_times = 0
        self.lost_responses = 0

    def respond(self, kind: OperationKind, outcome: ProviderOperationOutcome) -> None:
        self.outcomes[kind] = outcome

    def execute(self, request: ProviderOperationRequest) -> ProviderOperationOutcome:
        self.requests.append(request)
        if self.unavailable_times > 0:
            self.unavailable_times -= 1
            raise ProviderUnavailableError("provider is unavailable")
        outcome = self.effects.get(request.idempotency_key)
        if outcome is None:
            outcome = self.outcomes.get(
                request.kind,
                ProviderOperationOutcome.succeeded(f"{request.kind.value}-ref-{len(self.effects)}"),
            )
            self.effects[request.idempotency_key] = outcome
        if self.lost_responses > 0:
            self.lost_responses -= 1
            raise ProviderUnavailableError("response lost after the provider applied the operation")
        return outcome

    def effect_count(self, kind: OperationKind) -> int:
        keys = {r.idempotency_key for r in self.requests if r.kind is kind}
        return len(keys & set(self.effects))


class FakePermissions:
    def __init__(self) -> None:
        self.denied: set[PaymentPermission] = set()
        self.unavailable = False
        self.checks: list[tuple[Subject, PaymentPermission, PaymentResource]] = []

    def is_allowed(
        self, subject: Subject, permission: PaymentPermission, resource: PaymentResource
    ) -> bool:
        self.checks.append((subject, permission, resource))
        if self.unavailable:
            raise AuthorizationUnavailableError("authorization check timed out")
        return permission not in self.denied


class FixedClock:
    def __init__(self, now: datetime = NOW) -> None:
        self._now = now

    def now(self) -> datetime:
        return self._now

    def advance(self, delta: timedelta) -> None:
        self._now += delta


class SequentialIdentifiers:
    def __init__(self) -> None:
        self._payments = 0
        self._operations = 0

    def new_payment_id(self) -> PaymentId:
        self._payments += 1
        return PaymentId(f"payment-{self._payments}")

    def new_operation_id(self) -> OperationId:
        self._operations += 1
        return OperationId(f"operation-{self._operations}")


class PaymentsTestKit:
    """Real PostgreSQL adapters over a truncated database plus the non-storage fakes."""

    def __init__(self, database: Database) -> None:
        self.database = database
        with database.connection() as conn:
            conn.execute(
                "TRUNCATE subject_keys, payment_events, payment_snapshots, payments, "
                "payment_operations, payments_outbox, inbox"
            )
        self.uow = CountingUnitOfWork(DatabaseUnitOfWork(database))
        self.payments = StalePaymentRepository(database, SNAPSHOT_EVERY)
        self.subjects = DatabaseSubjectKeys(database, SUBJECT_KEK)
        self.history = DatabasePaymentOperationHistory(database)
        self.statuses = DatabasePaymentStatusReader(database)
        self.outbox = FailingOutbox(DatabasePaymentOutbox(database, PickledEventsMapper()))
        self.inbox = DatabaseProviderMessageInbox(database)
        self.provider = FakePaymentProvider()
        self.permissions = FakePermissions()
        self.clock = FixedClock()
        self.ids = SequentialIdentifiers()
        self.transitions = FsmMetrics()

    def executor(self) -> PaymentOperationExecutor:
        return PaymentOperationExecutor(
            uow=self.uow,
            payments=self.payments,
            provider=self.provider,
            permissions=self.permissions,
            clock=self.clock,
            operation_ids=self.ids,
            applier=self.applier(),
            transitions=self.transitions,
            subjects=self.subjects,
        )

    def applier(self) -> ProviderResultApplier:
        return ProviderResultApplier(self.payments, self.outbox, self.clock, self.transitions)

    def given_payment(
        self,
        state: PaymentState = PaymentState.PENDING,
        payment_id: str = "payment-0",
        order_id: str = "order-1",
        amount: str = "25.00",
        market_id: MarketId = MARKET,
    ) -> PaymentId:
        target = PaymentId(payment_id)
        order = OrderId(order_id)
        money = Money.parse(amount, "EUR")
        fact = (target, order, market_id, money)
        events: list[PaymentDomainEvent] = [PaymentCreated(*fact)]
        if state in {PaymentState.AUTHORIZED, PaymentState.CAPTURED, PaymentState.REFUNDED}:
            events.append(PaymentAuthorized(*fact, "auth-ref"))
        if state in {PaymentState.CAPTURED, PaymentState.REFUNDED}:
            events.append(PaymentCaptured(*fact))
        if state is PaymentState.REFUNDED:
            events.append(PaymentRefunded(*fact))
        if state is PaymentState.FAILED:
            events.append(PaymentFailed(target, order, market_id, "failed"))
        self._given_events(target, 0, events)
        return target

    def given_operation(
        self,
        payment_id: PaymentId,
        kind: OperationKind,
        idempotency_key: str,
        state: OperationState = OperationState.PENDING,
        operation_id: str = "operation-0",
        decline_reason: str | None = None,
    ) -> OperationId:
        payment = self.payment(payment_id)
        now = self.clock.now()
        target = OperationId(operation_id)
        events: list[PaymentDomainEvent] = [
            PaymentOperationRequested(
                target, payment_id, kind, idempotency_key, payment.amount, now, REQUESTER
            )
        ]
        if state is OperationState.SUCCEEDED:
            events.append(PaymentOperationSucceeded(target, payment_id, kind, "op-ref", now))
        if state is OperationState.DECLINED:
            events.append(
                PaymentOperationDeclined(target, payment_id, kind, decline_reason or "", now)
            )
        self._given_events(payment_id, payment.version, events)
        return target

    def _given_events(
        self, payment_id: PaymentId, after_version: int, events: Sequence[PaymentDomainEvent]
    ) -> None:
        with self.database.connection() as conn:
            append_events(conn, payment_id, after_version, [encode_event(e) for e in events])
        payment = self.payment(payment_id)
        with self.database.connection() as conn:
            project_payment(conn, payment.model, payment.version)

    def payment(self, payment_id: PaymentId) -> Payment:
        payment = self.payments.get(payment_id)
        assert payment is not None, payment_id
        return payment

    def payment_state(self, payment_id: PaymentId) -> PaymentState:
        return self.payment(payment_id).state

    def payment_ids(self) -> list[PaymentId]:
        with self.database.connection() as conn:
            rows = conn.execute("SELECT id FROM payments ORDER BY id").fetchall()
        return [PaymentId(row[0]) for row in rows]

    def operation(self, operation_id: OperationId) -> PaymentOperation:
        payment = self.payments.get_by_operation(operation_id)
        assert payment is not None, operation_id
        operation = payment.model.operation(operation_id)
        assert operation is not None, operation_id
        return operation

    def stored_operations(self) -> list[PaymentOperation]:
        with self.database.connection() as conn:
            rows = conn.execute("SELECT id FROM payment_operations ORDER BY id").fetchall()
        return [self.operation(OperationId(row[0])) for row in rows]

    def provider_messages(self) -> set[str]:
        with self.database.connection() as conn:
            rows = conn.execute("SELECT event_id FROM inbox").fetchall()
        return {row[0] for row in rows}

    # Порядок записи Outbox: seq больше нет, строки одной транзакции различает только ctid
    # (в тестах таблица без удалений, ctid растёт по вставке).
    def outbox_aggregates(self) -> list[AggregateRef]:
        with self.database.connection() as conn:
            rows = conn.execute(
                "SELECT aggregate_type, aggregate_id, aggregate_version "
                "FROM payments_outbox ORDER BY created_at, ctid"
            ).fetchall()
        return [AggregateRef(t, i, int(v)) for t, i, v in rows]

    def published(self) -> list[PaymentDomainEvent]:
        with self.database.connection() as conn:
            rows = conn.execute(
                "SELECT payload FROM payments_outbox ORDER BY created_at, ctid"
            ).fetchall()
        return [pickle.loads(row[0]) for row in rows]  # noqa: S301 - payloads written by this kit

    def snapshot(self) -> dict[str, list[tuple[Any, ...]]]:
        """Every row of every payments table, for asserting that a query changed nothing."""
        tables = (
            "payment_events",
            "payment_snapshots",
            "payments",
            "payment_operations",
            "payments_outbox",
            "inbox",
        )
        with self.database.connection() as conn:
            return {
                table: conn.execute(f"SELECT * FROM {table} ORDER BY 1, 2").fetchall()  # noqa: S608 - fixed table names
                for table in tables
            }


def unreachable_database() -> Database:
    """Pool that never connects: for composition tests that stop before storage."""
    return create_database(database.Config(DATABASE__URI=UNREACHABLE_DATABASE_URI))


UNREACHABLE_DATABASE_URI = "postgresql://payments@127.0.0.1:1/x"


def payments_container(environment: Mapping[str, str] | None = None) -> PaymentsContainer:
    """``PaymentsContainer`` over settings from ``environment``.

    The database is an unreachable pool (closed by ``close_container``) and both workers
    listen on ephemeral loopback ports.
    """
    env = {
        "DATABASE__URI": UNREACHABLE_DATABASE_URI,
        **REQUIRED_EVENT_STORE_ENV,
        **(environment or {}),
    }
    with patch.dict(os.environ, env, clear=True):
        settings = load_settings()
    container = PaymentsContainer()
    container.config.from_dict(sections(settings))
    container.database.override(providers.Object(unreachable_database()))
    container.grpc_address.override(providers.Object(("127.0.0.1", 0)))
    container.webhook_address.override(providers.Object(("127.0.0.1", 0)))
    return container


def close_container(container: PaymentsContainer) -> None:
    container.database().close()
