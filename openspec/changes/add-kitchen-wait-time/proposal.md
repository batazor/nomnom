# Proposal

## Why

Когда кухня перегружена, покупатель узнаёт об этом только по просрочке на экране заказа. Каталог показывает все открытые рестораны одинаково, а начальный `expected_ready_at` считает `FixedPreparationTimeEstimator`: время одно для всех ресторанов и не зависит от того, что происходит на кухне. В модели кухни открыт вопрос Kitchen Capacity — как загрузка влияет на `expected_ready_at` (`docs/kitchen.md`).

Ограничивать приём заказов (load shedding, пауза приёма, скрытие ресторана) мы не будем. Покупатель видит метку «повышенное время ожидания» и реальную оценку в минутах, а дальше решает сам.

## What Changes

- **Статистика ожидания ресторана.** Consumer в `restaurants` читает собственные `RestaurantOrderAccepted`, `RestaurantOrderReady`, `RestaurantOrderCancelled` и `PreparationFailed` и хранит в документе `Restaurant` блок статистики:
  - последние N длительностей «принят → готов»;
  - момент принятия каждого незавершённого заказа;
  - текущий уровень ожидания.

  Линия поваров и Cook Pool в расчёт не входят.
- **Оценка ожидания.** Базовое время — медиана последних N длительностей. Текущее ожидание — большее из двух величин: медианы заказов, готовых за последнее окно, и возраста самого старого незавершённого заказа. Пока длительностей меньше N, обе величины равны `KITCHEN__PREPARATION_TIME`.
- **Уровень с гистерезисом.** `ELEVATED` ставится, когда текущее ожидание больше базового в `k_on` раз. `NORMAL` возвращается, когда оно не больше базового в `k_off` раз. Если уровень сменился, в той же транзакции через Outbox публикуется `RestaurantWaitLevelChanged`, и по нему обновляется кеш каталога.
- **`expected_ready_at` по статистике.** Ready Time Estimator возвращает `accepted_at` + текущее ожидание; `FixedPreparationTimeEstimator` удаляется. Сроки не пересчитываются и не гарантируются.
- **Каталог.** `GetRestaurant` и `ListRestaurants` отдают уровень ожидания и текущее ожидание. В GraphQL у `Restaurant` появляется `waitTime { elevated, expectedWaitMinutes }`: его заполняют запросы каталога, в остальных местах — `null`, как `menu`.
- **UI.** Бейдж «Повышенное время ожидания» в списке ресторанов и на странице ресторана. При оформлении заказа показывается «≈N мин».
- В `RestaurantOrderAccepted`, `RestaurantOrderReady`, `RestaurantOrderCancelled` и `PreparationFailed` добавляется `restaurant_id`, изменение аддитивное.

Не входит:

- ограничение приёма, пауза, скрытие ресторана; технический rate limiting на gateway не меняется;
- базовое время по часам суток — точнее, но позже (design.md);
- пересчёт `expected_ready_at` уже принятых заказов;
- премиум-приоритет — change `add-premium-priority`.

## Capabilities

### New Capabilities

- `kitchen-wait-time`: статистика ожидания ресторана, уровень и событие его смены.

### Modified Capabilities

- `fulfillment-eta`: Ready Time Estimator по статистике.
- `restaurant-management`: публичное представление ресторана с ожиданием.
- `customer-graphql-contract`: ожидание ресторана в customer-схеме.
- `customer-web-application`: бейдж и оценка при оформлении заказа.

## Impact

- **`boundaries/restaurants`:**
  - слайс `restaurant`: блок статистики в документе, consumer событий Restaurant Order, `RestaurantWaitLevelChanged`, представление ресторана, обновление кеша;
  - слайс `restaurantorder`: `restaurant_id` в Integration Events, Ready Time Estimator читает статистику через порт;
  - proto `events.proto`, `restaurants.proto`;
  - конфиг `WAIT_TIME__*`, values `restaurants` и `restaurants-consumer`.
- **`boundaries/bff`:** клиент `restaurants`, поля `Restaurant`, presenter, тесты.
- **`boundaries/ui`:** каталог, страница ресторана, оформление заказа, i18n, моки.
- **Документация:**
  - `docs/kitchen.md` (Kitchen Capacity, открытые вопросы);
  - `docs/ubiquitous-language.md`;
  - ADR 0003 restaurants (кеш);
  - README `restaurants`, `bff`, `ui`.
