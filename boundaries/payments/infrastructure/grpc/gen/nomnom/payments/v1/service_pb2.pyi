from nomnom.payments.v1 import payments_pb2 as _payments_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class GetPaymentStatusRequest(_message.Message):
    __slots__ = ("payment_id",)
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    def __init__(self, payment_id: _Optional[str] = ...) -> None: ...

class GetPaymentStatusResponse(_message.Message):
    __slots__ = ("payment",)
    PAYMENT_FIELD_NUMBER: _ClassVar[int]
    payment: _payments_pb2.Payment
    def __init__(self, payment: _Optional[_Union[_payments_pb2.Payment, _Mapping]] = ...) -> None: ...

class CreatePaymentRequest(_message.Message):
    __slots__ = ("order_id", "amount")
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    order_id: str
    amount: _payments_pb2.Money
    def __init__(self, order_id: _Optional[str] = ..., amount: _Optional[_Union[_payments_pb2.Money, _Mapping]] = ...) -> None: ...

class CreatePaymentResponse(_message.Message):
    __slots__ = ("payment_id", "state", "created")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    CREATED_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    state: _payments_pb2.PaymentState
    created: bool
    def __init__(self, payment_id: _Optional[str] = ..., state: _Optional[_Union[_payments_pb2.PaymentState, str]] = ..., created: _Optional[bool] = ...) -> None: ...

class AuthorizePaymentRequest(_message.Message):
    __slots__ = ("payment_id", "idempotency_key")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    idempotency_key: str
    def __init__(self, payment_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class AuthorizePaymentResponse(_message.Message):
    __slots__ = ("operation",)
    OPERATION_FIELD_NUMBER: _ClassVar[int]
    operation: _payments_pb2.PaymentOperation
    def __init__(self, operation: _Optional[_Union[_payments_pb2.PaymentOperation, _Mapping]] = ...) -> None: ...

class CapturePaymentRequest(_message.Message):
    __slots__ = ("payment_id", "idempotency_key")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    idempotency_key: str
    def __init__(self, payment_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class CapturePaymentResponse(_message.Message):
    __slots__ = ("operation",)
    OPERATION_FIELD_NUMBER: _ClassVar[int]
    operation: _payments_pb2.PaymentOperation
    def __init__(self, operation: _Optional[_Union[_payments_pb2.PaymentOperation, _Mapping]] = ...) -> None: ...

class CancelAuthorizationRequest(_message.Message):
    __slots__ = ("payment_id", "idempotency_key")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    idempotency_key: str
    def __init__(self, payment_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class CancelAuthorizationResponse(_message.Message):
    __slots__ = ("operation",)
    OPERATION_FIELD_NUMBER: _ClassVar[int]
    operation: _payments_pb2.PaymentOperation
    def __init__(self, operation: _Optional[_Union[_payments_pb2.PaymentOperation, _Mapping]] = ...) -> None: ...

class RefundPaymentRequest(_message.Message):
    __slots__ = ("payment_id", "idempotency_key")
    PAYMENT_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    payment_id: str
    idempotency_key: str
    def __init__(self, payment_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class RefundPaymentResponse(_message.Message):
    __slots__ = ("operation",)
    OPERATION_FIELD_NUMBER: _ClassVar[int]
    operation: _payments_pb2.PaymentOperation
    def __init__(self, operation: _Optional[_Union[_payments_pb2.PaymentOperation, _Mapping]] = ...) -> None: ...
