"""Pure payments domain model."""

from .completion import complete_operation
from .events import (
    PaymentAuthorizationCancelled,
    PaymentAuthorized,
    PaymentCaptured,
    PaymentDomainEvent,
    PaymentEvent,
    PaymentFailed,
    PaymentOperationDeclined,
    PaymentRefunded,
)
from .fsm import (
    TERMINAL_STATES,
    AlreadyInStateError,
    GuardFailedError,
    InvalidTransitionError,
    PaymentState,
    PaymentTrigger,
    TransitionResult,
)
from .ids import OperationId, OperationKind, OrderId, PaymentId
from .operation import (
    InvalidOperationTransitionError,
    OperationAlreadyCompletedError,
    OperationMismatchError,
    OperationOutcome,
    OperationState,
    OperationTrigger,
    PaymentOperation,
)
from .operation_kinds import (
    OPERATION_KINDS,
    OperationKindRules,
    UnknownOperationKindError,
    rules_of,
)
from .payment import Payment
from .permissions import PaymentPermission
from .repository import (
    DuplicateIdempotencyKeyError,
    DuplicatePaymentError,
    OperationAlreadyPendingError,
    PaymentOperationRepository,
    PaymentRepository,
)
from .specifications import (
    PaymentCapturable,
    PaymentCaptureContext,
    PaymentPolicyReason,
    PaymentRefundable,
    PaymentRefundContext,
    SpecificationResult,
)
from .vo.market import InvalidMarketIdError, MarketId
from .vo.money import CurrencyMismatchError, InvalidMoneyError, Money

__all__ = [
    "OPERATION_KINDS",
    "TERMINAL_STATES",
    "AlreadyInStateError",
    "CurrencyMismatchError",
    "DuplicateIdempotencyKeyError",
    "DuplicatePaymentError",
    "GuardFailedError",
    "InvalidMarketIdError",
    "InvalidMoneyError",
    "InvalidOperationTransitionError",
    "InvalidTransitionError",
    "MarketId",
    "Money",
    "OperationAlreadyCompletedError",
    "OperationAlreadyPendingError",
    "OperationId",
    "OperationKind",
    "OperationKindRules",
    "OperationMismatchError",
    "OperationOutcome",
    "OperationState",
    "OperationTrigger",
    "OrderId",
    "Payment",
    "PaymentAuthorizationCancelled",
    "PaymentAuthorized",
    "PaymentCapturable",
    "PaymentCaptureContext",
    "PaymentCaptured",
    "PaymentDomainEvent",
    "PaymentEvent",
    "PaymentFailed",
    "PaymentId",
    "PaymentOperation",
    "PaymentOperationDeclined",
    "PaymentOperationRepository",
    "PaymentPermission",
    "PaymentPolicyReason",
    "PaymentRefundContext",
    "PaymentRefundable",
    "PaymentRefunded",
    "PaymentRepository",
    "PaymentState",
    "PaymentTrigger",
    "SpecificationResult",
    "TransitionResult",
    "UnknownOperationKindError",
    "complete_operation",
    "rules_of",
]
