"""Apply Provider Callback: apply a verified webhook to a known operation at most once.

Infrastructure verifies authenticity of the callback and maps its payload; the use case
deduplicates the provider message in the same Unit of Work as the state change. The
provider is not a subject of the payments authorization graph, so no permission check.
"""

from dataclasses import dataclass

from pkg.application import UnitOfWork
from slices.payment.domains.payment import OperationId

from ..errors import InvalidPaymentRequest
from ..operation_flow import (
    ProviderResultApplication,
    ProviderResultApplier,
    ProviderResultStatus,
    to_domain_outcome,
)
from ..ports import ProviderMessageInbox, ProviderOperationOutcome


@dataclass(frozen=True)
class ApplyProviderCallbackInput:
    provider_message_id: str
    operation_id: str
    outcome: ProviderOperationOutcome


ApplyProviderCallbackResult = ProviderResultApplication


class ApplyProviderCallbackHandler:
    def __init__(
        self, uow: UnitOfWork, inbox: ProviderMessageInbox, applier: ProviderResultApplier
    ) -> None:
        self._uow = uow
        self._inbox = inbox
        self._applier = applier

    def handle(self, command: ApplyProviderCallbackInput) -> ApplyProviderCallbackResult:
        message_id = command.provider_message_id.strip()
        if not message_id:
            raise InvalidPaymentRequest("provider message id is required")
        if not command.operation_id.strip():
            raise InvalidPaymentRequest("operation id is required")
        operation_id = OperationId(command.operation_id)
        to_domain_outcome(command.outcome)

        def apply() -> ApplyProviderCallbackResult:
            if not self._inbox.register(message_id):
                return ProviderResultApplication(
                    ProviderResultStatus.DUPLICATE_MESSAGE, operation_id
                )
            return ProviderResultApplication.of(self._applier.apply(operation_id, command.outcome))

        return self._uow.run(apply)
