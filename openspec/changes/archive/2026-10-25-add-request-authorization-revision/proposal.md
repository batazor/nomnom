# Proposal

## Why

Проверки `platform:nomnom` в `restaurants` всегда идут с `minimize_latency`, поэтому выданное только что право оператора (`platform#operator`) видно не сразу: `RegisterRestaurant` сразу после выдачи отказывает несколько секунд. `docs/security.md` предусматривает передачу ZedToken в gRPC metadata, но ключ не был выбран.

## What Changes

- Metadata `authorization-revision` с ZedToken вызывающего; `restaurants` применяет его к проверкам `platform:nomnom` и ресторанов без сохранённой revision.
- `Subject.authorizationRevision` переносит token от транспорта к адаптеру; права token не расширяет.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `authorization-revisions`: свежесть платформенных проверок по revision вызывающего.

## Impact

`boundaries/restaurants` (`Subject`, `service.ts`, SpiceDB-адаптер, тесты), `docs/security.md`.
