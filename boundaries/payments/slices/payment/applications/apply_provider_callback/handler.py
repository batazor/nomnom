"""Apply Provider Callback: apply a verified webhook to a known operation at most once.

Infrastructure verifies authenticity of the callback and maps its payload; the use case
deduplicates the provider message in the same Unit of Work as the state change. The
provider is not a subject of the payments authorization graph, so no permission check.
"""

from dataclasses import dataclass

from pkg.unit_of_work import UnitOfWork
from slices.payment.domains.payment import OperationId

from ..errors import InvalidPaymentRequestError
from ..operation_flow import (
    ProviderResultApplication,
    ProviderResultApplier,
    ProviderResultStatus,
    to_domain_outcome,
)
from ..ports import ProviderMessageInbox, ProviderOperationOutcome


@dataclass(frozen=True)
class ApplyProviderCallbackInput:
    """Verified Provider webhook mapped to a final or accepted outcome of one operation."""

    provider_message_id: str
    operation_id: str
    outcome: ProviderOperationOutcome


ApplyProviderCallbackResult = ProviderResultApplication


class ApplyProviderCallbackHandler:
    """Applies a Provider webhook result once per Provider message and once per operation."""

    def __init__(
        self, uow: UnitOfWork, inbox: ProviderMessageInbox, applier: ProviderResultApplier
    ) -> None:
        self._uow = uow
        self._inbox = inbox
        self._applier = applier

    def handle(self, command: ApplyProviderCallbackInput) -> ApplyProviderCallbackResult:
        """Deduplicate the message and apply its result in one Unit of Work.

        Returns ``duplicate_message`` for an already registered message, otherwise the
        outcome of applying the result to the operation.

        Raises:
            InvalidPaymentRequestError: The ids are blank or the outcome is not final.
            PaymentOperationNotFoundError: The operation is unknown.
            ProviderResultConflictError: The result contradicts the recorded one.
        """
        message_id = command.provider_message_id.strip()
        if not message_id:
            raise InvalidPaymentRequestError("provider message id is required")
        if not command.operation_id.strip():
            raise InvalidPaymentRequestError("operation id is required")
        operation_id = OperationId(command.operation_id)
        to_domain_outcome(command.outcome)

        def apply() -> ApplyProviderCallbackResult:
            if not self._inbox.register(message_id):
                return ProviderResultApplication(
                    ProviderResultStatus.DUPLICATE_MESSAGE, operation_id
                )
            return ProviderResultApplication.of(self._applier.apply(operation_id, command.outcome))

        return self._uow.run(apply)
