# Design

## Статистика ожидания

Wait Time Statistics — блок `waitTime` документа `Restaurant` (слайс `restaurant`):

| Поле | Значение |
| --- | --- |
| `durations` | последние `WAIT_TIME__SAMPLE_SIZE` длительностей «принят → готов» с моментом готовности, новые в конце |
| `open` | `order_id → accepted_at` незавершённых заказов |
| `level` | `NORMAL` или `ELEVATED` |

Блок — read model, а не часть агрегата: consumer записывает его `$set` без проверки и без увеличения `version` ресторана (`VersionedCollection.setUnversioned`). Сохранение агрегата заменяет документ, поэтому коллекция ресторанов объявляет `waitTime` полем read model: `save` заменяет документ pipeline-update'ом, который сохраняет это поле как есть. Update, Open и Close Restaurant не конфликтуют со статистикой по версии; одновременная запись в одном документе — write conflict транзакции, который драйвер повторяет.

Consumer `restaurant.wait-time` в `restaurants-consumer` читает `restaurants.v1.restaurant-order-events`:

| Событие | Действие |
| --- | --- |
| `RestaurantOrderAccepted` | `open[order_id] = occurred_at` |
| `RestaurantOrderReady` | если `order_id` есть в `open`: длительность `occurred_at − accepted_at` в `durations`, ключ удаляется |
| `RestaurantOrderCancelled`, `PreparationFailed` | ключ удаляется из `open`, длительность не пишется |
| остальные | явный no-op |

Consumer — свой `MessageProcessor` с Inbox `restaurants.wait-time` в группе `KAFKA__CONSUMER_GROUP`; worker группы направляет сообщение processor'у его стрима. Повтор безопасен и без Inbox: повторный Accepted для известного заказа ничего не меняет, Ready и отмена после удаления заказа тоже. Partition key — id Restaurant Order, поэтому факты одного заказа приходят по порядку. Ресторан берётся из нового поля `restaurant_id` событий. События, опубликованные до этого change, `restaurant_id` не несут: consumer подтверждает их без изменений, а статистика наполняется с новых заказов. Остальные типы стрима processor подтверждает без обработки.

Ресторана нет — ошибка consumer'а (DLQ), а не пропуск.

## Расчёт

- **Базовое время** `B` — медиана `durations`, когда их не меньше `WAIT_TIME__SAMPLE_SIZE`. Иначе `B = KITCHEN__PREPARATION_TIME`.
- **Текущее ожидание** `W = max(R, A)`:
  - `R` — медиана длительностей, готовых за последние `WAIT_TIME__RECENT_WINDOW`. Таких нет — `R = B`;
  - `A` — возраст самого старого заказа в `open` на момент расчёта. `open` пуст — `A = 0`.
- **Уровень:** `ELEVATED`, если `W > B × WAIT_TIME__ELEVATED_FACTOR`; `NORMAL`, если `W ≤ B × WAIT_TIME__NORMAL_FACTOR`; в промежутке остаётся прежний. `NORMAL_FACTOR` MUST быть меньше `ELEVATED_FACTOR`, иначе процесс падает на старте.

Пока выборка не набрана, `B` и `R` равны `KITCHEN__PREPARATION_TIME`. Это явный параметр «базовое время до накопления истории», а не скрытая подмена. Метку тогда может поставить только `A`.

Функция расчёта чистая: (статистика, now, параметры) → `{B, W, level}`, это domain-код слайса `restaurant`. Ею пользуются consumer (уровень), представление ресторана (`W` на момент чтения) и Ready Time Estimator.

Параметры `WAIT_TIME__SAMPLE_SIZE` (50), `WAIT_TIME__RECENT_WINDOW` (30m), `WAIT_TIME__ELEVATED_FACTOR` (1.5) и `WAIT_TIME__NORMAL_FACTOR` (1.2) — обязательный конфиг. Значения задаются явно в `.env.example` и values; пустое или невалидное значение — падение на старте.

Улучшение на потом: базовое время отдельно по часам суток, чтобы обычный вечерний пик не выглядел перегрузкой.

## Смена уровня

Consumer после update вычисляет уровень. Если он изменился, в той же транзакции MongoDB consumer записывает новый `level` и `RestaurantWaitLevelChanged` (`restaurant_id`, `market_id`, `elevated`) в Outbox. `aggregate_type` события — `RestaurantWaitTime`, версия — номер смены уровня (`revision` статистики): id события Outbox строится из типа, id и версии агрегата, а версия `Restaurant` при смене уровня не растёт. Событие идёт в `restaurants.v1.restaurant-events`, partition key — `restaurant_id`.

Уровень пересчитывается только на событиях ресторана, без таймеров и polling (ADR 36). Если кухня встала и событий нет, сохранённый уровень не меняется до следующего факта. Минуты при этом считаются при чтении и растут вместе с `A`.

## Каталог и кеш

Get Restaurant и List Restaurants возвращают `CatalogRestaurantView` — `RestaurantView` с `waitTime { elevated, expectedWaitMinutes }`: сохранённый `level` и `W` на момент чтения, округлённое вверх до минуты. В proto это `Restaurant.wait_time`, его заполняют только эти два RPC; ответы команд и List Owned Restaurants его не несут. Кешированная запись или страница без ожидания — промах.

Кеш каталога (ADR 0003 restaurants) обновляется по `RestaurantWaitLevelChanged` так же, как по `RestaurantOpened` и `RestaurantClosed`: перечитывается ресторан и начинается новое поколение страниц его Market. Минуты в кеше устаревают не больше чем на `CACHE__TTL`. Этого достаточно: время в минутах — оценка, а не обещание.

## Ready Time Estimator

`FixedPreparationTimeEstimator` заменяется `WaitTimeReadyTimeEstimator`: `expected_ready_at = accepted_at + W`. Статистику он читает через порт к слайсу `restaurant` внутри Unit of Work Accept Order. Срок, который кухня назвала сама (FR-609), имеет приоритет. Уже принятые заказы не пересчитываются.

## Customer UI

- Список ресторанов и страница ресторана: бейдж «Повышенное время ожидания», если `waitTime.elevated`.
- Оформление заказа: «Ожидание ≈N мин» из `waitTime.expectedWaitMinutes`.
- GraphQL: `Restaurant.waitTime: RestaurantWaitTime` — заполняют `restaurants` и `restaurant`; в заказе, на кухне и в `myRestaurants` — `null`, как `menu`, а не нули.
- Данные приходят с каталогом. Отдельной подписки и polling нет.

## Риски

- Пока выборка не набрана, оценка — фиксированное время: первые заказы нового ресторана метку почти не получают.
- `A` растёт от одного «забытого» заказа. Это и есть сигнал: заказ висит, покупатель ждёт.
- Уровень, который не обновился у вставшей кухни, описан выше. Таймер мы не вводим сознательно.
