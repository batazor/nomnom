from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class PaymentState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PAYMENT_STATE_UNSPECIFIED: _ClassVar[PaymentState]
    PAYMENT_STATE_CREATED: _ClassVar[PaymentState]
    PAYMENT_STATE_AUTHORIZED: _ClassVar[PaymentState]
    PAYMENT_STATE_CAPTURED: _ClassVar[PaymentState]
    PAYMENT_STATE_CANCELLED: _ClassVar[PaymentState]
    PAYMENT_STATE_REFUNDED: _ClassVar[PaymentState]
    PAYMENT_STATE_FAILED: _ClassVar[PaymentState]

class PaymentOperationStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PAYMENT_OPERATION_STATUS_UNSPECIFIED: _ClassVar[PaymentOperationStatus]
    PAYMENT_OPERATION_STATUS_SUCCEEDED: _ClassVar[PaymentOperationStatus]
    PAYMENT_OPERATION_STATUS_DECLINED: _ClassVar[PaymentOperationStatus]
    PAYMENT_OPERATION_STATUS_PENDING: _ClassVar[PaymentOperationStatus]
PAYMENT_STATE_UNSPECIFIED: PaymentState
PAYMENT_STATE_CREATED: PaymentState
PAYMENT_STATE_AUTHORIZED: PaymentState
PAYMENT_STATE_CAPTURED: PaymentState
PAYMENT_STATE_CANCELLED: PaymentState
PAYMENT_STATE_REFUNDED: PaymentState
PAYMENT_STATE_FAILED: PaymentState
PAYMENT_OPERATION_STATUS_UNSPECIFIED: PaymentOperationStatus
PAYMENT_OPERATION_STATUS_SUCCEEDED: PaymentOperationStatus
PAYMENT_OPERATION_STATUS_DECLINED: PaymentOperationStatus
PAYMENT_OPERATION_STATUS_PENDING: PaymentOperationStatus

class Money(_message.Message):
    __slots__ = ("amount", "currency")
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    CURRENCY_FIELD_NUMBER: _ClassVar[int]
    amount: str
    currency: str
    def __init__(self, amount: _Optional[str] = ..., currency: _Optional[str] = ...) -> None: ...

class Payment(_message.Message):
    __slots__ = ("payment_id", "order_id", "amount", "state", "market_id")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    MARKET_ID_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    order_id: str
    amount: Money
    state: PaymentState
    market_id: str
    def __init__(self, payment_id: _Optional[str] = ..., order_id: _Optional[str] = ..., amount: _Optional[_Union[Money, _Mapping]] = ..., state: _Optional[_Union[PaymentState, str]] = ..., market_id: _Optional[str] = ...) -> None: ...

class PaymentOperation(_message.Message):
    __slots__ = ("payment_id", "operation_id", "status", "payment_state", "decline_reason", "replayed")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    OPERATION_ID_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    PAYMENT_STATE_FIELD_NUMBER: _ClassVar[int]
    DECLINE_REASON_FIELD_NUMBER: _ClassVar[int]
    REPLAYED_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    operation_id: str
    status: PaymentOperationStatus
    payment_state: PaymentState
    decline_reason: str
    replayed: bool
    def __init__(self, payment_id: _Optional[str] = ..., operation_id: _Optional[str] = ..., status: _Optional[_Union[PaymentOperationStatus, str]] = ..., payment_state: _Optional[_Union[PaymentState, str]] = ..., decline_reason: _Optional[str] = ..., replayed: _Optional[bool] = ...) -> None: ...
