# Proposal

## Why

В demo-стенде заказ проходит путь только руками: покупатель оформляет его в UI, кухня и курьер кликают каждый шаг на флоу-борде. Без человека поток заказов стоит, и метрики `nomnom_kitchen_preparation_duration_seconds`, `nomnom_delivery_duration_seconds` и дашборды не показывают ничего, кроме скорости кликов.

## What Changes

- **Simulator** — слайс `simulator` в `orders`, `restaurants` и `delivery`, без новых сервисов и контейнеров. Он вызывает настоящие use cases своего сервиса от имени настоящего участника: покупатель (`orders`), владелец ресторана (`restaurants`), курьер, которому сделан оффер или назначена доставка (`delivery`).
- Каждое действие выполняется через случайную задержку от `SIMULATOR__MIN_DELAY` до `SIMULATOR__MAX_DELAY` (в kind — 30s и 2m). Исход выбирается случайно с весами, в основном успешный.
- Состояние Simulator (включён/выключен) хранится в своём хранилище каждого сервиса и переживает рестарт. Переключают его RPC `GetSimulator` и `SetSimulatorEnabled` с permission `platform#operate_simulator` (только operator).
- **BFF**: query `simulator` и mutation `setSimulatorEnabled(enabled)` — fan-out на три сервиса.
- **UI**: переключатель «Эмулятор» в шапке; виден только тем, кому `simulator` не отвечает `PermissionDenied`.

Не входит: имитация движения курьера по карте, нагрузочное тестирование, payments (платёж авторизует Order Fulfillment как обычно).

## Capabilities

### New Capabilities

- `demo-simulator`.

## Impact

`authorization/schema.zed` (`platform#operate_simulator`), proto трёх сервисов и их generated-копии, слайсы `simulator` в `boundaries/{orders,restaurants,delivery}`, конфиг `SIMULATOR__*` и values kind, GRPCRoute `*-v1-simulator` в `ops/kind/manifests/gateway/gateway.yaml`, схема и слайс BFF, шапка UI.
