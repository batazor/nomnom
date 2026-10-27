# kubernetes-test-environment Specification

## Purpose
Определяет воспроизводимое тестовое окружение Kubernetes для интеграционных тестов и E2E,
которым нужны настоящие сеть, CNI и Gateway API.

## Requirements

### Requirement: Отдельный воспроизводимый тестовый кластер

Окружение SHALL создавать отдельный kind-кластер из одного узла без
default CNI. Имя кластера SHALL параметризоваться и по умолчанию быть `nomnom-test`. Команды
MUST явно использовать kube-context тестового кластера и MUST NOT изменять другие кластеры.

#### Scenario: Создание с именем по умолчанию

- **WHEN** разработчик или CI создаёт окружение без параметров
- **THEN** появляется кластер `nomnom-test` из одного узла с контекстом `kind-nomnom-test`

### Requirement: Закреплённые версии

Версии kind, node image, CNI, gateway controller и Gateway API CRD SHALL быть закреплены в
одном месте. Установленная версия Gateway API CRD MUST сверяться с закреплённой.

#### Scenario: Расхождение версии

- **WHEN** установленная версия kind или Gateway API CRD отличается от закреплённой
- **THEN** создание окружения завершается ошибкой

### Requirement: Запуск собранной версии приложений

Образы приложений SHALL собираться существующими multi-stage Dockerfile, получать уникальный
тег сборки и загружаться в кластер без registry. Workloads MUST запускать именно загруженный
образ.

#### Scenario: Изменённый исходный код

- **WHEN** исходный код приложения изменён после предыдущей сборки
- **THEN** новая сборка получает другой тег и кластер запускает её

### Requirement: Неинтерактивный прогон с диагностикой

Полный цикл — создание кластера, установка платформы, сборка и загрузка образов,
развёртывание, ожидание readiness, проверки и удаление — SHALL выполняться одной
неинтерактивной командой. Ожидание readiness workloads и статусов Gateway API MUST иметь
таймаут. При ошибке SHALL сохраняться pod logs, events, состояние Gateway API и диагностика CNI.

#### Scenario: Ошибка прогона

- **WHEN** компонент не становится ready за отведённое время или проверка не проходит
- **THEN** команда завершается ненулевым кодом, диагностика сохранена, кластер удалён, если не запрошено его сохранение

### Requirement: Независимость unit-тестов от кластера

Unit-тесты доменной и прикладной логики MUST NOT зависеть от kind-окружения.

#### Scenario: Проверки контекста

- **WHEN** выполняется `check` bounded context
- **THEN** кластер не требуется

### Requirement: restaurants-consumer в тестовом окружении
**ID:** FR-499

Тестовое kind-окружение SHALL разворачивать `restaurants-consumer` отдельным release chart `restaurants` с тем же образом, без Service и миграций, после release `restaurants`, с Kafka `nomnom-infra` и MongoDB `restaurants`.

#### Scenario: Consumer готов
**ID:** AC-569

- **WHEN** `make kind-deploy` завершён
- **THEN** pod `restaurants-consumer` Ready, подключён к Kafka и MongoDB под существующими NetworkPolicy

### Requirement: BFF развёрнут в kind из воспроизводимого образа
**ID:** FR-502

Тестовое окружение SHALL разворачивать BFF из образа с закреплённой версией и checksum Cosmo Router; сборка MUST падать при несовпадении checksum. BFF SHALL вызывать контексты только через internal gateway.

#### Scenario: Подмена Router
**ID:** AC-574

- **WHEN** скачанный архив Router не совпадает с закреплённым SHA-256
- **THEN** сборка образа BFF завершается ошибкой

### Requirement: User identity проверяется насквозь
**ID:** FR-503

Проверка kind SHALL подтверждать цепочку ADR 32: запрос к BFF без access token отклоняется, запрос с access token тестового identity provider доходит до `orders` как `Subject{user}`, а тот же user assertion от другого workload отклоняется `UNAUTHENTICATED`.

#### Scenario: Отсутствующий заказ через BFF
**ID:** AC-575

- **WHEN** проверка запрашивает `order(id)` несуществующего заказа с валидным access token
- **THEN** ответ содержит `order = null` без ошибок, что подтверждает принятие assertion в `orders`

#### Scenario: Запрос без access token
**ID:** AC-576

- **WHEN** проверка запрашивает `order(id)` без заголовка `Authorization`
- **THEN** ответ содержит ошибку аутентификации

### Requirement: Fulfillment-процессы orders в тестовом окружении
**ID:** FR-584

Тестовое окружение SHALL разворачивать `orders-worker` и `orders-forwarder` из chart `orders` с той же сборкой образа, что и API `orders`, с readiness и диагностикой, как у остальных процессов.

#### Scenario: Заказ начинает исполняться
**ID:** AC-739

- **WHEN** покупатель оформил заказ через BFF
- **THEN** consumer `orders` получает `OrderPlaced` и запускает workflow, workflow авторизует оплату, заказ получает статус `PAID`, и только после этого у ресторана появляется Restaurant Order в `RECEIVED`

### Requirement: Путь заказа проверяется насквозь
**ID:** FR-585

`make kind-test` SHALL выполнять Chainsaw-тест полного пути заказа: подготовка ресторана, меню и курьера, оформление заказа покупателем через BFF, действия кухни и курьера через internal gateway и итоговое состояние заказа в BFF. Действия кухни и курьера MUST выполняться fixture `e2e-driver`, которому только в kind-values разрешено передавать user assertion в `restaurants` и `delivery`; operator MUST выдаваться kind-only bootstrap SpiceDB.

#### Scenario: Заказ доставлен
**ID:** AC-740

- **WHEN** тест проходит путь от `placeOrder` до Confirm Delivery
- **THEN** `order(id)` в BFF возвращает `DELIVERED` с milestones `COURIER_ASSIGNED`, `PICKED_UP` и `DELIVERED`, а оплата списана

#### Scenario: Делегат только в kind
**ID:** AC-741

- **WHEN** проверяются chart-default values `restaurants` и `delivery`
- **THEN** `e2e-driver` не входит ни в allowlist, ни в `userDelegates`
