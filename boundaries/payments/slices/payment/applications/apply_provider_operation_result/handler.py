"""Apply Provider Operation Result: apply a result received outside the original request.

The asynchronous provider adapter correlates the result with the local operation; a
result equal to the recorded one is a no-op and a contradicting one is rejected.
"""

from dataclasses import dataclass

from pkg.unit_of_work import UnitOfWork
from slices.payment.domains.payment import OperationId

from ..errors import InvalidPaymentRequestError
from ..operation_flow import ProviderResultApplication, ProviderResultApplier, to_domain_outcome
from ..ports import ProviderOperationOutcome


@dataclass(frozen=True)
class ApplyProviderOperationResultInput:
    """Final Provider result of an operation that was accepted asynchronously."""

    operation_id: str
    outcome: ProviderOperationOutcome


ApplyProviderOperationResultResult = ProviderResultApplication


class ApplyProviderOperationResultHandler:
    """Applies an asynchronous Provider result to its operation at most once."""

    def __init__(self, uow: UnitOfWork, applier: ProviderResultApplier) -> None:
        self._uow = uow
        self._applier = applier

    def handle(
        self, command: ApplyProviderOperationResultInput
    ) -> ApplyProviderOperationResultResult:
        """Apply the result in one Unit of Work; a repeated equal result is a no-op.

        Raises:
            InvalidPaymentRequestError: The operation id is blank or the outcome is not final.
            PaymentOperationNotFoundError: The operation is unknown.
            ProviderResultConflictError: The result contradicts the recorded one.
        """
        if not command.operation_id.strip():
            raise InvalidPaymentRequestError("operation id is required")
        operation_id = OperationId(command.operation_id)
        to_domain_outcome(command.outcome)
        return self._uow.run(
            lambda: ProviderResultApplication.of(self._applier.apply(operation_id, command.outcome))
        )
