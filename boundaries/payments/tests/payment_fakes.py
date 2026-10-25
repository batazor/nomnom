"""In-memory and fake adapters of the payments application ports for unit tests.

They implement the contracts expected from the future storage and provider adapters:
snapshot/rollback Unit of Work, optimistic concurrency by ``version``, unique
``order_id`` and idempotency key, at most one pending operation per Payment and
provider deduplication by idempotency key.
"""

import os
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field, replace
from datetime import UTC, datetime, timedelta
from typing import TypeVar
from unittest.mock import patch

from dependency_injector import providers

from di.payments import PaymentsContainer
from di.resources import sections
from infrastructure.postgres import PostgresDatabase, create_database
from pkg.application import (
    AggregateRef,
    AuthorizationUnavailableError,
    ConcurrencyConflictError,
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
    DuplicateIdempotencyKeyError,
    DuplicatePaymentError,
    Money,
    OperationAlreadyPendingError,
    OperationId,
    OperationKind,
    OperationState,
    OrderId,
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


@dataclass(frozen=True)
class PaymentRecord:
    id: PaymentId
    order_id: OrderId
    amount: Money
    state: PaymentState
    provider_reference: str | None
    version: int


@dataclass(frozen=True)
class OperationRecord:
    id: OperationId
    payment_id: PaymentId
    kind: OperationKind
    idempotency_key: str
    amount: Money
    requested_at: datetime
    state: OperationState
    provider_reference: str | None
    decline_reason: str | None
    completed_at: datetime | None
    version: int


@dataclass(frozen=True)
class OutboxRecord:
    aggregate: AggregateRef
    event: PaymentDomainEvent


@dataclass
class InMemoryPaymentStore:
    payments: dict[PaymentId, PaymentRecord] = field(default_factory=dict)
    operations: dict[OperationId, OperationRecord] = field(default_factory=dict)
    outbox: list[OutboxRecord] = field(default_factory=list)
    provider_messages: set[str] = field(default_factory=set)

    def snapshot(self) -> InMemoryPaymentStore:
        return InMemoryPaymentStore(
            dict(self.payments),
            dict(self.operations),
            list(self.outbox),
            set(self.provider_messages),
        )

    def restore(self, snapshot: InMemoryPaymentStore) -> None:
        self.payments = dict(snapshot.payments)
        self.operations = dict(snapshot.operations)
        self.outbox = list(snapshot.outbox)
        self.provider_messages = set(snapshot.provider_messages)


class InMemoryUnitOfWork:
    def __init__(self, store: InMemoryPaymentStore) -> None:
        self._store = store
        self._active = False
        self.commits = 0
        self.rollbacks = 0

    def run(self, work: Callable[[], T]) -> T:
        if self._active:
            return work()
        snapshot = self._store.snapshot()
        self._active = True
        try:
            result = work()
        except BaseException:
            self._store.restore(snapshot)
            self.rollbacks += 1
            raise
        finally:
            self._active = False
        self.commits += 1
        return result


class InMemoryPaymentRepository:
    def __init__(self, store: InMemoryPaymentStore) -> None:
        self._store = store

    def get(self, payment_id: PaymentId) -> Payment | None:
        record = self._store.payments.get(payment_id)
        return None if record is None else self._to_domain(record)

    def get_by_order(self, order_id: OrderId) -> Payment | None:
        for record in self._store.payments.values():
            if record.order_id == order_id:
                return self._to_domain(record)
        return None

    def add(self, payment: Payment) -> int:
        if any(r.order_id == payment.order_id for r in self._store.payments.values()):
            raise DuplicatePaymentError(payment.order_id)
        self._store.payments[payment.id] = self._to_record(payment, version=1)
        return 1

    def save(self, payment: Payment) -> int:
        current = self._store.payments.get(payment.id)
        if current is None or current.version != payment.version:
            raise ConcurrencyConflictError("Payment", payment.id)
        version = payment.version + 1
        self._store.payments[payment.id] = self._to_record(payment, version)
        return version

    @staticmethod
    def _to_record(payment: Payment, version: int) -> PaymentRecord:
        return PaymentRecord(
            payment.id,
            payment.order_id,
            payment.amount,
            payment.state,
            payment.provider_reference,
            version,
        )

    @staticmethod
    def _to_domain(record: PaymentRecord) -> Payment:
        return Payment.restore(
            record.id,
            record.order_id,
            record.amount,
            record.state,
            record.provider_reference,
            record.version,
        )


class InMemoryPaymentOperationRepository:
    def __init__(self, store: InMemoryPaymentStore) -> None:
        self._store = store
        # Simulates a concurrent transaction committed after this one read the data:
        # lookups miss existing operations until the next ``add``.
        self.stale_lookups = False

    def get(self, operation_id: OperationId) -> PaymentOperation | None:
        record = self._store.operations.get(operation_id)
        return None if record is None else self._to_domain(record)

    def get_by_idempotency_key(self, idempotency_key: str) -> PaymentOperation | None:
        if self.stale_lookups:
            return None
        for record in self._store.operations.values():
            if record.idempotency_key == idempotency_key:
                return self._to_domain(record)
        return None

    def find_pending(self, payment_id: PaymentId) -> PaymentOperation | None:
        if self.stale_lookups:
            return None
        for record in self._store.operations.values():
            if record.payment_id == payment_id and record.state is OperationState.PENDING:
                return self._to_domain(record)
        return None

    def list_for_payment(self, payment_id: PaymentId) -> list[PaymentOperation]:
        records = [r for r in self._store.operations.values() if r.payment_id == payment_id]
        return [self._to_domain(r) for r in sorted(records, key=lambda r: r.requested_at)]

    def add(self, operation: PaymentOperation) -> None:
        self.stale_lookups = False
        if self.get_by_idempotency_key(operation.idempotency_key) is not None:
            raise DuplicateIdempotencyKeyError(operation.idempotency_key)
        if self.find_pending(operation.payment_id) is not None:
            raise OperationAlreadyPendingError(operation.payment_id)
        self._store.operations[operation.id] = self._to_record(operation, version=1)

    def save(self, operation: PaymentOperation) -> int:
        current = self._store.operations.get(operation.id)
        if current is None or current.version != operation.version:
            raise ConcurrencyConflictError("PaymentOperation", operation.id)
        version = operation.version + 1
        self._store.operations[operation.id] = self._to_record(operation, version)
        return version

    def insert(self, record: OperationRecord) -> None:
        """Test helper: store an operation directly, bypassing uniqueness checks."""
        self._store.operations[record.id] = record

    @staticmethod
    def _to_record(operation: PaymentOperation, version: int) -> OperationRecord:
        return OperationRecord(
            operation.id,
            operation.payment_id,
            operation.kind,
            operation.idempotency_key,
            operation.amount,
            operation.requested_at,
            operation.state,
            operation.provider_reference,
            operation.decline_reason,
            operation.completed_at,
            version,
        )

    @staticmethod
    def _to_domain(record: OperationRecord) -> PaymentOperation:
        return PaymentOperation.restore(
            record.id,
            record.payment_id,
            record.kind,
            record.idempotency_key,
            record.amount,
            record.requested_at,
            record.state,
            record.provider_reference,
            record.decline_reason,
            record.completed_at,
            record.version,
        )


class InMemoryPaymentOutbox:
    def __init__(self, store: InMemoryPaymentStore) -> None:
        self._store = store
        self.fail_next = False

    def add(self, aggregate: AggregateRef, events: Sequence[PaymentDomainEvent]) -> None:
        if self.fail_next:
            self.fail_next = False
            raise RuntimeError("outbox write failed")
        self._store.outbox.extend(OutboxRecord(aggregate, event) for event in events)


class InMemoryProviderMessageInbox:
    def __init__(self, store: InMemoryPaymentStore) -> None:
        self._store = store

    def register(self, provider_message_id: str) -> bool:
        if provider_message_id in self._store.provider_messages:
            return False
        self._store.provider_messages.add(provider_message_id)
        return True


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
    """Wires every fake around one in-memory store."""

    def __init__(self) -> None:
        self.store = InMemoryPaymentStore()
        self.uow = InMemoryUnitOfWork(self.store)
        self.payments = InMemoryPaymentRepository(self.store)
        self.operations = InMemoryPaymentOperationRepository(self.store)
        self.outbox = InMemoryPaymentOutbox(self.store)
        self.inbox = InMemoryProviderMessageInbox(self.store)
        self.provider = FakePaymentProvider()
        self.permissions = FakePermissions()
        self.clock = FixedClock()
        self.ids = SequentialIdentifiers()

    def executor(self) -> PaymentOperationExecutor:
        return PaymentOperationExecutor(
            uow=self.uow,
            payments=self.payments,
            operations=self.operations,
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
        record = PaymentRecord(
            PaymentId(payment_id),
            OrderId(order_id),
            Money.parse(amount, "EUR"),
            state,
            reference,
            version=1,
        )
        self.store.payments[record.id] = record
        return record.id

    def given_operation(
        self,
        payment_id: PaymentId,
        kind: OperationKind,
        idempotency_key: str,
        state: OperationState = OperationState.PENDING,
        operation_id: str = "operation-0",
        decline_reason: str | None = None,
    ) -> OperationId:
        payment = self.store.payments[payment_id]
        completed = state is not OperationState.PENDING
        record = OperationRecord(
            OperationId(operation_id),
            payment_id,
            kind,
            idempotency_key,
            payment.amount,
            self.clock.now(),
            state,
            "op-ref" if state is OperationState.SUCCEEDED else None,
            decline_reason,
            self.clock.now() if completed else None,
            version=1,
        )
        self.operations.insert(record)
        return record.id

    def payment_state(self, payment_id: PaymentId) -> PaymentState:
        return self.store.payments[payment_id].state

    def operation(self, operation_id: OperationId) -> OperationRecord:
        return self.store.operations[operation_id]

    def published(self) -> list[PaymentDomainEvent]:
        return [record.event for record in self.store.outbox]

    def with_payment_version(self, payment_id: PaymentId, version: int) -> None:
        self.store.payments[payment_id] = replace(self.store.payments[payment_id], version=version)


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
