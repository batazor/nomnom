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
from infrastructure.postgres import (
    PAYMENT_EVENTS_TOPIC,
    OutboxMessage,
    PostgresDatabase,
    PostgresPaymentOperationHistory,
    PostgresPaymentOperationRepository,
    PostgresPaymentOutbox,
    PostgresPaymentRepository,
    PostgresPendingOperationQuery,
    PostgresProviderMessageInbox,
    PostgresUnitOfWork,
    create_database,
)
from pkg.application import (
    AggregateRef,
    AuthorizationUnavailableError,
    Subject,
    SubjectKind,
)
from pkg.config import DatabaseSettings, load_settings
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
    Money,
    OperationId,
    OperationKind,
    OperationState,
    Payment,
    PaymentDomainEvent,
    PaymentId,
    PaymentOperation,
    PaymentState,
)

T = TypeVar("T")

ORCHESTRATOR = Subject(SubjectKind.SERVICE, "orders-activity-worker")
OPERATOR = Subject(SubjectKind.USER, "operator-1")
NOW = datetime(2026, 10, 25, 10, 0, tzinfo=UTC)


class PickledEventsMapper:
    """Outbox mapper of the test kit: every Domain Event is stored, pickled as its payload.

    ``event_type`` is the event ``repr`` so that distinct events of one aggregate version
    get distinct event ids.
    """

    def map(self, aggregate: AggregateRef, event: PaymentDomainEvent) -> OutboxMessage:
        return OutboxMessage(repr(event), PAYMENT_EVENTS_TOPIC, pickle.dumps(event))


class FailingOutbox:
    """Real Outbox whose next write can be made to fail."""

    def __init__(self, outbox: PostgresPaymentOutbox) -> None:
        self._outbox = outbox
        self.fail_next = False

    def add(self, aggregate: AggregateRef, events: Sequence[PaymentDomainEvent]) -> None:
        if self.fail_next:
            self.fail_next = False
            raise RuntimeError("outbox write failed")
        self._outbox.add(aggregate, events)


class CountingUnitOfWork:
    """Real Unit of Work that counts outer commits and rollbacks."""

    def __init__(self, uow: PostgresUnitOfWork) -> None:
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


class StaleOperationRepository(PostgresPaymentOperationRepository):
    """Simulates a concurrent transaction committed after this one read the data.

    While ``stale_lookups`` is set, idempotency key lookups miss existing operations
    until the next ``add``.
    """

    def __init__(self, database: PostgresDatabase) -> None:
        super().__init__(database)
        self.stale_lookups = False

    @override
    def get_by_idempotency_key(self, idempotency_key: str) -> PaymentOperation | None:
        return None if self.stale_lookups else super().get_by_idempotency_key(idempotency_key)

    @override
    def add(self, operation: PaymentOperation) -> None:
        self.stale_lookups = False
        super().add(operation)


class StalePendingOperationQuery(PostgresPendingOperationQuery):
    """Pending lookup that misses while the operation repository reads stale data."""

    def __init__(self, database: PostgresDatabase, operations: StaleOperationRepository) -> None:
        super().__init__(database)
        self._operations = operations

    @override
    def pending_operation_id(self, payment_id: PaymentId) -> OperationId | None:
        if self._operations.stale_lookups:
            return None
        return super().pending_operation_id(payment_id)


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

    def __init__(self, database: PostgresDatabase) -> None:
        self.database = database
        with database.connection() as conn:
            conn.execute("TRUNCATE payments, payment_operations, payments_outbox, inbox")
        self.uow = CountingUnitOfWork(PostgresUnitOfWork(database))
        self.payments = PostgresPaymentRepository(database)
        self.operations = StaleOperationRepository(database)
        self.pending = StalePendingOperationQuery(database, self.operations)
        self.history = PostgresPaymentOperationHistory(database)
        self.outbox = FailingOutbox(PostgresPaymentOutbox(database, PickledEventsMapper()))
        self.inbox = PostgresProviderMessageInbox(database)
        self.provider = FakePaymentProvider()
        self.permissions = FakePermissions()
        self.clock = FixedClock()
        self.ids = SequentialIdentifiers()

    def executor(self) -> PaymentOperationExecutor:
        return PaymentOperationExecutor(
            uow=self.uow,
            payments=self.payments,
            operations=self.operations,
            pending=self.pending,
            provider=self.provider,
            permissions=self.permissions,
            clock=self.clock,
            operation_ids=self.ids,
            applier=self.applier(),
        )

    def applier(self) -> ProviderResultApplier:
        return ProviderResultApplier(self.payments, self.operations, self.outbox, self.clock)

    def given_payment(
        self,
        state: PaymentState = PaymentState.PENDING,
        payment_id: str = "payment-0",
        order_id: str = "order-1",
        amount: str = "25.00",
    ) -> PaymentId:
        reference = "auth-ref" if state in _STATES_WITH_REFERENCE else None
        money = Money.parse(amount, "EUR")
        with self.database.connection() as conn:
            conn.execute(
                "INSERT INTO payments (id, order_id, amount, currency, state, "
                "provider_reference, version) VALUES (%s, %s, %s, %s, %s, %s, 1)",
                (payment_id, order_id, money.amount, money.currency, state.value, reference),
            )
        return PaymentId(payment_id)

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
        completed = state is not OperationState.PENDING
        with self.database.connection() as conn:
            conn.execute(
                "INSERT INTO payment_operations (id, payment_id, kind, idempotency_key, amount, "
                "currency, requested_at, state, provider_reference, decline_reason, "
                "completed_at, version) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, 1)",
                (
                    operation_id,
                    payment_id,
                    kind.value,
                    idempotency_key,
                    payment.amount.amount,
                    payment.amount.currency,
                    now,
                    state.value,
                    "op-ref" if state is OperationState.SUCCEEDED else None,
                    decline_reason,
                    now if completed else None,
                ),
            )
        return OperationId(operation_id)

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
        operation = self.operations.get(operation_id)
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
        tables = ("payments", "payment_operations", "payments_outbox", "inbox")
        with self.database.connection() as conn:
            return {
                table: conn.execute(f"SELECT * FROM {table} ORDER BY 1, 2").fetchall()  # noqa: S608 - fixed table names
                for table in tables
            }

    def with_payment_version(self, payment_id: PaymentId, version: int) -> None:
        with self.database.connection() as conn:
            conn.execute("UPDATE payments SET version = %s WHERE id = %s", (version, payment_id))


_STATES_WITH_REFERENCE = {PaymentState.AUTHORIZED, PaymentState.CAPTURED, PaymentState.REFUNDED}


def unreachable_database() -> PostgresDatabase:
    """Pool that never connects: for composition tests that stop before storage."""
    return create_database(DatabaseSettings(DATABASE__URL=UNREACHABLE_DATABASE_URL))


UNREACHABLE_DATABASE_URL = "postgresql://payments@127.0.0.1:1/x"


def payments_container(environment: Mapping[str, str] | None = None) -> PaymentsContainer:
    """``PaymentsContainer`` over settings from ``environment``.

    The database is an unreachable pool (closed by ``close_container``) and both workers
    listen on ephemeral loopback ports.
    """
    env = {"DATABASE__URL": UNREACHABLE_DATABASE_URL, **(environment or {})}
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
