"""Mapping of payments application errors to gRPC status codes."""

from collections.abc import Iterator
from contextlib import contextmanager
from typing import NoReturn

import grpc

from pkg.authz import AuthorizationUnavailableError, PermissionDeniedError
from pkg.usecase import ConcurrencyConflictError
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

# Ordered: the first matching application error decides the gRPC status code.
ERROR_CODES: tuple[tuple[type[Exception], grpc.StatusCode], ...] = (
    (InvalidPaymentRequestError, grpc.StatusCode.INVALID_ARGUMENT),
    (PaymentNotFoundError, grpc.StatusCode.NOT_FOUND),
    (PaymentOperationNotFoundError, grpc.StatusCode.NOT_FOUND),
    (PermissionDeniedError, grpc.StatusCode.PERMISSION_DENIED),
    (AuthorizationUnavailableError, grpc.StatusCode.UNAVAILABLE),
    (ProviderUnavailableError, grpc.StatusCode.UNAVAILABLE),
    (PaymentAlreadyExistsError, grpc.StatusCode.ALREADY_EXISTS),
    (PaymentStateConflictError, grpc.StatusCode.FAILED_PRECONDITION),
    (IdempotencyKeyConflictError, grpc.StatusCode.FAILED_PRECONDITION),
    (OperationInProgressError, grpc.StatusCode.ABORTED),
    (ConcurrencyConflictError, grpc.StatusCode.ABORTED),
    (ProviderResultConflictError, grpc.StatusCode.ABORTED),
)


@contextmanager
def application_errors(context: grpc.ServicerContext) -> Iterator[None]:
    """Translate application errors into gRPC status codes.

    Subjects are resolved before entering this block: ``context.abort`` raises a plain
    ``Exception`` that must not be remapped to ``INTERNAL``.
    """
    try:
        yield
    except Exception as error:  # noqa: BLE001 - supervisor must catch every failure
        for error_type, code in ERROR_CODES:
            if isinstance(error, error_type):
                _abort(context, code, str(error))
        _abort(context, grpc.StatusCode.INTERNAL, "internal error")


def _abort(context: grpc.ServicerContext, code: grpc.StatusCode, message: str) -> NoReturn:
    context.abort(code, message)
