"""Identifier generators of the payments boundary."""

import uuid

from slices.payment.domains.payment import OperationId, PaymentId


class UuidIdentifiers:
    """Random UUIDv4 identifiers of Payments and Payment Operations."""

    def new_payment_id(self) -> PaymentId:
        """Return a fresh, never reused Payment ID."""
        return PaymentId(str(uuid.uuid4()))

    def new_operation_id(self) -> OperationId:
        """Return a fresh, never reused Payment Operation ID."""
        return OperationId(str(uuid.uuid4()))
