"""Use case names and error classification of the payment slice (docs/observability.md#use-cases).

``error.type`` values form a closed set: they become metric labels. A payment declined
by the Provider is a successful call (``DECLINED`` result), not a rejection.
"""

from pkg.application import (
    AuthorizationUnavailableError,
    ConcurrencyConflictError,
    PermissionDeniedError,
)
from pkg.telemetry.usecase import Failure, Outcome, UseCase
from slices.payment.applications.errors import (
    IdempotencyKeyConflictError,
    InvalidPaymentRequestError,
    OperationInProgressError,
    PaymentAlreadyExistsError,
    PaymentNotFoundError,
    PaymentOperationNotFoundError,
    PaymentStateConflictError,
    ProviderResultConflictError,
)
from slices.payment.applications.ports import ProviderUnavailableError

GET_PAYMENT_STATUS = UseCase.query("get_payment_status")
CREATE_PAYMENT = UseCase.command("create_payment")
AUTHORIZE_PAYMENT = UseCase.command("authorize_payment")
CAPTURE_PAYMENT = UseCase.command("capture_payment")
CANCEL_AUTHORIZATION = UseCase.command("cancel_authorization")
REFUND_PAYMENT = UseCase.command("refund_payment")
APPLY_PROVIDER_CALLBACK = UseCase.command("apply_provider_callback")

# Ordered: the first matching error decides; the rest is ``error/internal``.
_FAILURES: tuple[tuple[type[Exception], Failure], ...] = (
    (InvalidPaymentRequestError, Failure(Outcome.REJECTED, "invalid_request")),
    (PaymentNotFoundError, Failure(Outcome.REJECTED, "not_found")),
    (PaymentOperationNotFoundError, Failure(Outcome.REJECTED, "not_found")),
    (PermissionDeniedError, Failure(Outcome.REJECTED, "permission_denied")),
    (PaymentAlreadyExistsError, Failure(Outcome.REJECTED, "already_exists")),
    (PaymentStateConflictError, Failure(Outcome.REJECTED, "invalid_state")),
    (IdempotencyKeyConflictError, Failure(Outcome.REJECTED, "idempotency_conflict")),
    (OperationInProgressError, Failure(Outcome.REJECTED, "operation_in_progress")),
    (ProviderResultConflictError, Failure(Outcome.REJECTED, "provider_result_conflict")),
    # Технические сбои: зависимость недоступна или конфликт версий для retry.
    (AuthorizationUnavailableError, Failure(Outcome.ERROR, "authorization_unavailable")),
    (ProviderUnavailableError, Failure(Outcome.ERROR, "provider_unavailable")),
    (ConcurrencyConflictError, Failure(Outcome.ERROR, "concurrency_conflict")),
)

ERROR_TYPES = frozenset(failure.error_type for _, failure in _FAILURES) | {"internal"}


def classify_payment_error(error: Exception) -> Failure | None:
    """Failure of a payment use case error; ``None`` for an unknown one (``error/internal``)."""
    for error_type, failure in _FAILURES:
        if isinstance(error, error_type):
            return failure
    return None
