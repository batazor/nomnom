from nomnom.payments.v1 import payments_pb2 as _payments_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class PaymentAuthorized(_message.Message):
    __slots__ = ("payment_id", "order_id", "amount", "market_id")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    MARKET_ID_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    order_id: str
    amount: _payments_pb2.Money
    market_id: str
    def __init__(self, payment_id: _Optional[str] = ..., order_id: _Optional[str] = ..., amount: _Optional[_Union[_payments_pb2.Money, _Mapping]] = ..., market_id: _Optional[str] = ...) -> None: ...

class PaymentCaptured(_message.Message):
    __slots__ = ("payment_id", "order_id", "amount", "market_id")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    MARKET_ID_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    order_id: str
    amount: _payments_pb2.Money
    market_id: str
    def __init__(self, payment_id: _Optional[str] = ..., order_id: _Optional[str] = ..., amount: _Optional[_Union[_payments_pb2.Money, _Mapping]] = ..., market_id: _Optional[str] = ...) -> None: ...

class PaymentAuthorizationCancelled(_message.Message):
    __slots__ = ("payment_id", "order_id", "amount", "market_id")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    MARKET_ID_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    order_id: str
    amount: _payments_pb2.Money
    market_id: str
    def __init__(self, payment_id: _Optional[str] = ..., order_id: _Optional[str] = ..., amount: _Optional[_Union[_payments_pb2.Money, _Mapping]] = ..., market_id: _Optional[str] = ...) -> None: ...

class PaymentRefunded(_message.Message):
    __slots__ = ("payment_id", "order_id", "amount", "market_id")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    MARKET_ID_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    order_id: str
    amount: _payments_pb2.Money
    market_id: str
    def __init__(self, payment_id: _Optional[str] = ..., order_id: _Optional[str] = ..., amount: _Optional[_Union[_payments_pb2.Money, _Mapping]] = ..., market_id: _Optional[str] = ...) -> None: ...

class PaymentFailed(_message.Message):
    __slots__ = ("payment_id", "order_id", "reason", "market_id")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    MARKET_ID_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    order_id: str
    reason: str
    market_id: str
    def __init__(self, payment_id: _Optional[str] = ..., order_id: _Optional[str] = ..., reason: _Optional[str] = ..., market_id: _Optional[str] = ...) -> None: ...
