"""Sandbox provider in process memory: deterministic results, no real money moved."""

import threading
from decimal import Decimal

from slices.payment.applications.ports import (
    ProviderOperationOutcome,
    ProviderOperationRequest,
    ProviderUnavailable,
)
from slices.payment.domains.payment import OperationKind

# A sandbox Authorization of this amount is declined, like a test card of a real provider.
DECLINE_AMOUNT = Decimal("666.00")


class UnconfiguredProvider:
    def execute(self, request: ProviderOperationRequest) -> ProviderOperationOutcome:
        raise ProviderUnavailable("payment provider is not configured")


class FakePaymentProvider:
    """Deduplicates by idempotency key like a real provider: a repeat returns the original result.

    Every operation completes synchronously; the asynchronous path goes through webhooks.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._results: dict[str, ProviderOperationOutcome] = {}

    def execute(self, request: ProviderOperationRequest) -> ProviderOperationOutcome:
        with self._lock:
            known = self._results.get(request.idempotency_key)
            if known is None:
                known = self._decide(request)
                self._results[request.idempotency_key] = known
            return known

    @staticmethod
    def _decide(request: ProviderOperationRequest) -> ProviderOperationOutcome:
        if request.kind is OperationKind.AUTHORIZE:
            if request.amount.amount == DECLINE_AMOUNT:
                return ProviderOperationOutcome.declined("sandbox_card_declined")
            return ProviderOperationOutcome.succeeded(f"fake_auth_{request.operation_id}")
        if not request.authorization_reference:
            return ProviderOperationOutcome.declined("authorization_not_found")
        return ProviderOperationOutcome.succeeded(
            f"fake_{request.kind.value}_{request.operation_id}"
        )
