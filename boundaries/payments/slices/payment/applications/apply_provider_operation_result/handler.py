"""Apply Provider Operation Result: apply a result received outside the original request.

The asynchronous provider adapter correlates the result with the local operation; a
result equal to the recorded one is a no-op and a contradicting one is rejected.
"""

from dataclasses import dataclass

from pkg.application import UnitOfWork
from slices.payment.domains.payment import OperationId

from ..errors import InvalidPaymentRequestError
from ..operation_flow import ProviderResultApplication, ProviderResultApplier, to_domain_outcome
from ..ports import ProviderOperationOutcome


@dataclass(frozen=True)
class ApplyProviderOperationResultInput:
    operation_id: str
    outcome: ProviderOperationOutcome


ApplyProviderOperationResultResult = ProviderResultApplication


class ApplyProviderOperationResultHandler:
    def __init__(self, uow: UnitOfWork, applier: ProviderResultApplier) -> None:
        self._uow = uow
        self._applier = applier

    def handle(
        self, command: ApplyProviderOperationResultInput
    ) -> ApplyProviderOperationResultResult:
        if not command.operation_id.strip():
            raise InvalidPaymentRequestError("operation id is required")
        operation_id = OperationId(command.operation_id)
        to_domain_outcome(command.outcome)
        return self._uow.run(
            lambda: ProviderResultApplication.of(self._applier.apply(operation_id, command.outcome))
        )
