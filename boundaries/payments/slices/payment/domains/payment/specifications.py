"""Pure payment eligibility policies."""

from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import StrEnum

from .vo.money import CurrencyMismatchError, Money


class PaymentPolicyReason(StrEnum):
    """Machine-readable reason why a payment policy is not satisfied."""

    REFUND_WINDOW_EXPIRED = "refund_window_expired"
    REFUND_AMOUNT_EXCEEDS_AVAILABLE = "refund_amount_exceeds_available"
    AUTHORIZATION_EXPIRED = "authorization_expired"
    CAPTURE_AMOUNT_EXCEEDS_AVAILABLE = "capture_amount_exceeds_available"


@dataclass(frozen=True)
class SpecificationResult:
    """Outcome of a specification: every violated reason, empty when satisfied."""

    reasons: tuple[PaymentPolicyReason, ...]

    @property
    def satisfied(self) -> bool:
        """Whether no reason was reported."""
        return not self.reasons


@dataclass(frozen=True)
class PaymentRefundContext:
    """Facts the ``PaymentRefundable`` specification decides on; all amounts in one currency."""

    now: datetime
    captured_at: datetime
    refund_window: timedelta
    captured_amount: Money
    already_refunded: Money
    requested_amount: Money


def PaymentRefundable(context: PaymentRefundContext) -> SpecificationResult:  # noqa: N802 - name fixed by the gRPC/specification contract
    """Check that a Refund is still inside the refund window and fits the captured amount.

    Raises:
        CurrencyMismatchError: The amounts in ``context`` use different currencies.
    """
    reasons: list[PaymentPolicyReason] = []
    if context.now > context.captured_at + context.refund_window:
        reasons.append(PaymentPolicyReason.REFUND_WINDOW_EXPIRED)
    if _exceeds_available(
        context.requested_amount,
        context.captured_amount,
        context.already_refunded,
    ):
        reasons.append(PaymentPolicyReason.REFUND_AMOUNT_EXCEEDS_AVAILABLE)
    return SpecificationResult(tuple(reasons))


@dataclass(frozen=True)
class PaymentCaptureContext:
    """Facts the ``PaymentCapturable`` specification decides on; all amounts in one currency."""

    now: datetime
    authorized_at: datetime
    authorization_window: timedelta
    authorized_amount: Money
    already_captured: Money
    requested_amount: Money


def PaymentCapturable(context: PaymentCaptureContext) -> SpecificationResult:  # noqa: N802 - name fixed by the gRPC/specification contract
    """Check that the Authorization has not expired and a Capture fits the authorized amount.

    Raises:
        CurrencyMismatchError: The amounts in ``context`` use different currencies.
    """
    reasons: list[PaymentPolicyReason] = []
    if context.now > context.authorized_at + context.authorization_window:
        reasons.append(PaymentPolicyReason.AUTHORIZATION_EXPIRED)
    if _exceeds_available(
        context.requested_amount,
        context.authorized_amount,
        context.already_captured,
    ):
        reasons.append(PaymentPolicyReason.CAPTURE_AMOUNT_EXCEEDS_AVAILABLE)
    return SpecificationResult(tuple(reasons))


def _exceeds_available(requested: Money, total: Money, consumed: Money) -> bool:
    currencies = {requested.currency, total.currency, consumed.currency}
    if len(currencies) != 1:
        raise CurrencyMismatchError("payment policy requires one currency")
    return requested.amount > total.amount - consumed.amount
