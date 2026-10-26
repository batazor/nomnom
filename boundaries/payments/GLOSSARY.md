# Payments

Контекст описывает денежную операцию по заказу и её прохождение через платёжного провайдера.

## Language

**Payment**:
Денежная операция на определённую сумму, связанная с одним заказом.
_Avoid_: Order, transaction

**Market**:
Город доставки, к которому относится заказ и его Payment (`market_id`, slug, например `berlin`); задаётся в Create Payment и не меняется ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)).
_Avoid_: Region, tenant, cell

**Authorization**:
Подтверждение платёжным провайдером возможности последующего списания суммы.
_Avoid_: Capture, payment confirmation

**Capture**:
Списание ранее авторизованной суммы.
_Avoid_: Charge request, authorization

**Cancel Authorization**:
Снятие резерва средств по Authorization без списания.
_Avoid_: Void, refund

**Refund**:
Полный возврат ранее списанной суммы.
_Avoid_: Cancellation, reversal

**Provider Reference**:
Идентификатор успешной операции на стороне платёжного провайдера.
_Avoid_: Payment ID, order ID

**Payment Operation**:
Одна логическая попытка Authorization, Capture, Cancel Authorization или Refund с idempotency key и стабильным результатом.
_Avoid_: Transaction, attempt
