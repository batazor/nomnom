"""Pure payment eligibility policies."""

from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import StrEnum

from .vo.money import CurrencyMismatchError, Money


class PaymentPolicyReason(StrEnum):
    REFUND_WINDOW_EXPIRED = "refund_window_expired"
    REFUND_AMOUNT_EXCEEDS_AVAILABLE = "refund_amount_exceeds_available"
    AUTHORIZATION_EXPIRED = "authorization_expired"
    CAPTURE_AMOUNT_EXCEEDS_AVAILABLE = "capture_amount_exceeds_available"


@dataclass(frozen=True)
class SpecificationResult:
    reasons: tuple[PaymentPolicyReason, ...]

    @property
    def satisfied(self) -> bool:
        return not self.reasons


@dataclass(frozen=True)
class PaymentRefundContext:
    now: datetime
    captured_at: datetime
    refund_window: timedelta
    captured_amount: Money
    already_refunded: Money
    requested_amount: Money


def PaymentRefundable(context: PaymentRefundContext) -> SpecificationResult:  # noqa: N802 - name fixed by the gRPC/specification contract
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
    now: datetime
    authorized_at: datetime
    authorization_window: timedelta
    authorized_amount: Money
    already_captured: Money
    requested_amount: Money


def PaymentCapturable(context: PaymentCaptureContext) -> SpecificationResult:  # noqa: N802 - name fixed by the gRPC/specification contract
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
