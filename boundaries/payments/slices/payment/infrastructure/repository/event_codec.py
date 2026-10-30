"""JSON form of the Payment stream events and snapshots (local ADR 2).

``event_type`` is an explicit stored name, not the name of a Python class: renaming a class
does not break the history. An event of an older ``schema_version`` goes through the
upcasters on read; the stored row never changes. An unknown type or version and a payload
that does not match its schema are errors: an event is never skipped.

Amounts are strings, so they keep their exact scale.
"""

from collections.abc import Callable, Mapping
from dataclasses import dataclass, fields
from datetime import datetime
from typing import Any, override

from slices.payment.domains.payment import (
    MarketId,
    Money,
    OperationId,
    OperationKind,
    OperationState,
    OrderId,
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentCreated,
    PaymentDomainEvent,
    PaymentFailed,
    PaymentId,
    PaymentModel,
    PaymentOperation,
    PaymentOperationDeclined,
    PaymentOperationRequested,
    PaymentOperationSucceeded,
    PaymentRefunded,
    PaymentState,
)

# Поднимается при любом изменении формы снапшота или смысла evolve: снапшот другой версии
# не используется, состояние собирается из всего потока.
SNAPSHOT_SCHEMA_VERSION = 1

type Payload = dict[str, Any]


@dataclass(eq=True)
class UnreadableStoredDataError(Exception):
    """A stored event or snapshot cannot be read by this code: a defect, not a retry."""

    detail: str

    @override
    def __str__(self) -> str:
        return f"unreadable payment stream data: {self.detail}"


@dataclass(frozen=True)
class EncodedEvent:
    """An event as it is stored in ``payment_events``."""

    event_type: str
    schema_version: int
    payload: Payload


@dataclass(frozen=True)
class _Field:
    encode: Callable[[Any], Any]
    decode: Callable[[Any], Any]


def _optional(field: _Field) -> _Field:
    return _Field(
        encode=lambda value: None if value is None else field.encode(value),
        decode=lambda value: None if value is None else field.decode(value),
    )


def _encode_money(money: Money) -> Payload:
    return {"amount": str(money.amount), "currency": money.currency}


def _decode_money(value: Mapping[str, str]) -> Money:
    return Money.parse(value["amount"], value["currency"])


_TEXT = _Field(str, str)
_PAYMENT_ID = _Field(str, PaymentId)
_ORDER_ID = _Field(str, OrderId)
_OPERATION_ID = _Field(str, OperationId)
_MARKET = _Field(str, MarketId)
_MONEY = _Field(_encode_money, _decode_money)
_KIND = _Field(lambda kind: kind.value, OperationKind)
_OPERATION_STATE = _Field(lambda state: state.value, OperationState)
_PAYMENT_STATE = _Field(lambda state: state.value, PaymentState)
_TIME = _Field(datetime.isoformat, datetime.fromisoformat)

_PAYMENT_FACT: Mapping[str, _Field] = {
    "payment_id": _PAYMENT_ID,
    "order_id": _ORDER_ID,
    "market_id": _MARKET,
    "amount": _MONEY,
}


@dataclass(frozen=True)
class _EventSchema:
    event_class: type
    version: int
    fields: Mapping[str, _Field]


# Реестр потока: имя типа — контракт хранения, version — текущая версия payload.
_SCHEMAS: dict[str, _EventSchema] = {
    "PaymentCreated": _EventSchema(PaymentCreated, 1, _PAYMENT_FACT),
    "PaymentOperationRequested": _EventSchema(
        PaymentOperationRequested,
        1,
        {
            "operation_id": _OPERATION_ID,
            "payment_id": _PAYMENT_ID,
            "kind": _KIND,
            "idempotency_key": _TEXT,
            "amount": _MONEY,
            "requested_at": _TIME,
        },
    ),
    "PaymentOperationSucceeded": _EventSchema(
        PaymentOperationSucceeded,
        1,
        {
            "operation_id": _OPERATION_ID,
            "payment_id": _PAYMENT_ID,
            "kind": _KIND,
            "provider_reference": _optional(_TEXT),
            "completed_at": _TIME,
        },
    ),
    "PaymentOperationDeclined": _EventSchema(
        PaymentOperationDeclined,
        1,
        {
            "operation_id": _OPERATION_ID,
            "payment_id": _PAYMENT_ID,
            "kind": _KIND,
            "reason": _TEXT,
            "completed_at": _TIME,
        },
    ),
    "PaymentAuthorized": _EventSchema(
        PaymentAuthorized, 1, {**_PAYMENT_FACT, "provider_reference": _TEXT}
    ),
    "PaymentCaptured": _EventSchema(PaymentCaptured, 1, _PAYMENT_FACT),
    "PaymentAuthorizationCancelled": _EventSchema(PaymentAuthorizationCancelled, 1, _PAYMENT_FACT),
    "PaymentRefunded": _EventSchema(PaymentRefunded, 1, _PAYMENT_FACT),
    "PaymentFailed": _EventSchema(
        PaymentFailed,
        1,
        {
            "payment_id": _PAYMENT_ID,
            "order_id": _ORDER_ID,
            "market_id": _MARKET,
            "reason": _TEXT,
        },
    ),
}

_TYPE_OF = {schema.event_class: name for name, schema in _SCHEMAS.items()}

# (event_type, schema_version) → функция, поднимающая payload на следующую версию.
UPCASTERS: dict[tuple[str, int], Callable[[Payload], Payload]] = {}


def encode_event(event: PaymentDomainEvent) -> EncodedEvent:
    """Return the stored form of ``event`` at the current version of its schema."""
    name = _TYPE_OF[type(event)]
    schema = _SCHEMAS[name]
    return EncodedEvent(name, schema.version, _encode(schema.fields, event))


def decode_event(event_type: str, schema_version: int, payload: Payload) -> PaymentDomainEvent:
    """Return the event of a stored row, upcast to the current version of its schema.

    Raises:
        UnreadableStoredDataError: The type or version is unknown, an upcaster is missing
            or the payload does not match the schema.
    """
    schema = _SCHEMAS.get(event_type)
    if schema is None:
        raise UnreadableStoredDataError(f"unknown event type {event_type!r}")
    if schema_version > schema.version:
        raise UnreadableStoredDataError(f"unknown version {schema_version} of {event_type}")
    upcast = payload
    for version in range(schema_version, schema.version):
        upcaster = UPCASTERS.get((event_type, version))
        if upcaster is None:
            raise UnreadableStoredDataError(f"no upcaster of {event_type} from version {version}")
        upcast = upcaster(upcast)
    return schema.event_class(**_decode(schema.fields, upcast, event_type))


_OPERATION_FIELDS: Mapping[str, _Field] = {
    "id": _OPERATION_ID,
    "payment_id": _PAYMENT_ID,
    "kind": _KIND,
    "idempotency_key": _TEXT,
    "amount": _MONEY,
    "requested_at": _TIME,
    "state": _OPERATION_STATE,
    "provider_reference": _optional(_TEXT),
    "decline_reason": _optional(_TEXT),
    "completed_at": _optional(_TIME),
}

_SNAPSHOT_FIELDS: Mapping[str, _Field] = {
    "id": _PAYMENT_ID,
    "order_id": _ORDER_ID,
    "market_id": _MARKET,
    "amount": _MONEY,
    "state": _PAYMENT_STATE,
    "provider_reference": _optional(_TEXT),
}


def encode_snapshot(model: PaymentModel) -> Payload:
    """Return the stored form of a snapshot at ``SNAPSHOT_SCHEMA_VERSION``."""
    return {
        **_encode(_SNAPSHOT_FIELDS, model),
        "operations": [_encode(_OPERATION_FIELDS, op) for op in model.operations],
    }


def decode_snapshot(state: Payload) -> PaymentModel:
    """Return the state of a snapshot of ``SNAPSHOT_SCHEMA_VERSION``.

    Raises:
        UnreadableStoredDataError: The stored state does not match the schema.
    """
    try:
        fields_only = {name: value for name, value in state.items() if name != "operations"}
        operations = tuple(
            PaymentOperation(**_decode(_OPERATION_FIELDS, op, "snapshot operation"))
            for op in state["operations"]
        )
        return PaymentModel(
            **_decode(_SNAPSHOT_FIELDS, fields_only, "snapshot"), operations=operations
        )
    except (KeyError, TypeError, ValueError) as error:
        raise UnreadableStoredDataError(f"snapshot: {error}") from error


def _encode(schema: Mapping[str, _Field], value: object) -> Payload:
    return {name: field.encode(getattr(value, name)) for name, field in schema.items()}


def _decode(schema: Mapping[str, _Field], payload: Payload, what: str) -> Payload:
    if set(payload) != set(schema):
        raise UnreadableStoredDataError(f"{what}: fields {sorted(payload)}")
    try:
        return {name: field.decode(payload[name]) for name, field in schema.items()}
    except (KeyError, TypeError, ValueError) as error:
        raise UnreadableStoredDataError(f"{what}: {error}") from error


def _check_registry() -> None:
    # Реестр и dataclass расходятся только из-за дефекта: ошибка при импорте, а не на чтении.
    for name, schema in _SCHEMAS.items():
        declared = {field.name for field in fields(schema.event_class)}
        if declared != set(schema.fields):
            raise TypeError(f"schema of {name} does not match {schema.event_class.__name__}")


_check_registry()
