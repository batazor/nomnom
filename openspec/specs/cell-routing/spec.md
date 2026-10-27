# cell-routing Specification

## Purpose
Определяет маршрутизацию клиентских запросов по Market на публичном gateway: маршруты `/m/{market_id}/…` в Cosmo Router Cell, их публикацию control plane, список Market по `GET /markets`, BFF внутри одной Cell и адресацию Market путём в UI.

## Requirements

### Requirement: Маршрут Market на публичном gateway
**ID:** FR-628

Публичный gateway SHALL направлять запрос с путём `/m/{market_id}/…` в Router Cell Market. Маршрут MUST снимать префикс и выставлять `X-Nomnom-Market` из маршрута, перезаписывая значение клиента. Путь без маршрута MUST получать `404` gateway; Cell «по умолчанию» MUST NOT существовать. Маршрут MUST NOT иметь request timeout, чтобы не обрывать SSE-подписки.

#### Scenario: Запрос Market доходит до его Cell
**ID:** AC-801

- **GIVEN** Market `berlin` размещён в `cell-1`
- **WHEN** клиент отправляет GraphQL query на `/m/berlin/graphql`
- **THEN** Router `cell-1` получает запрос на `/graphql` с `X-Nomnom-Market: berlin` и отвечает данными Market `berlin`

#### Scenario: Заголовок клиента не принимается
**ID:** AC-802

- **WHEN** клиент отправляет запрос на `/m/berlin/graphql` с заголовком `X-Nomnom-Market: hamburg`
- **THEN** Cell получает `X-Nomnom-Market: berlin`

#### Scenario: Неизвестный Market
**ID:** AC-803

- **WHEN** клиент отправляет запрос на `/m/atlantis/graphql` или `/m/berlinx/graphql`, а таких Market нет
- **THEN** gateway отвечает `404`, ни одна Cell запрос не получает

### Requirement: Router Cell в реестре
**ID:** FR-629

`RegisterCell` SHALL принимать `cell_id` и `router` — Kubernetes Service Router Cell (`namespace`, `name`, `port`). `namespace` и `name` MUST быть DNS-1123 label, `port` — от 1 до 65535, иначе `INVALID_ARGUMENT`. Повтор с теми же значениями MUST быть успешным с `created = false`, с другими для известного `cell_id` — `ALREADY_EXISTS`. `Cell` MUST NOT содержать адрес internal gateway; RPC `GetPlacementMap` MUST NOT существовать.

#### Scenario: Регистрация Cell с Router
**ID:** AC-804

- **WHEN** оператор вызывает `RegisterCell(cell_id: "cell-1", router: {namespace: "nomnom-bff", name: "bff", port: 3010})`
- **THEN** Cell `cell-1` зарегистрирована с этим Service, `created = true`

#### Scenario: Неверный Service Router
**ID:** AC-805

- **WHEN** оператор вызывает `RegisterCell` с `router.name = "Bff_1"` или `router.port = 0`
- **THEN** ответ `INVALID_ARGUMENT`, реестр не меняется

### Requirement: Control plane публикует маршруты после записи
**ID:** FR-630

После фиксации `RegisterCell` или `CreateMarket`, включая повтор с `created = false`, control plane SHALL применить полный набор ресурсов из реестра: маршрут каждого Market и список Market. Ошибка применения MUST давать `UNAVAILABLE` (`ROUTES_UNAVAILABLE`) без отката записи; повтор команды SHALL применять ресурсы снова. Параллельные записи MUST NOT оставлять ресурсы по более старому снимку реестра.

#### Scenario: Новый Market сразу маршрутизируется
**ID:** AC-806

- **GIVEN** зарегистрирована Cell `cell-1`
- **WHEN** оператор вызывает `CreateMarket(market_id: "berlin", cell_id: "cell-1")`
- **THEN** существует `HTTPRoute market-berlin`, запрос на `/m/berlin/graphql` доходит до Router `cell-1`, а `GET /markets` содержит `berlin`

#### Scenario: Kubernetes API недоступен
**ID:** AC-807

- **GIVEN** control plane не может применить ресурсы
- **WHEN** оператор вызывает `CreateMarket` для нового Market
- **THEN** ответ `UNAVAILABLE` с reason `ROUTES_UNAVAILABLE`, Market сохранён в реестре, а повтор той же команды после восстановления API возвращает `created = false` и создаёт маршрут

#### Scenario: Параллельное создание двух Market
**ID:** AC-808

- **WHEN** оператор параллельно создаёт Market `berlin` и `hamburg`
- **THEN** после обоих ответов существуют маршруты обоих Market, а `GET /markets` содержит оба

### Requirement: Control plane приводит маршруты к реестру при старте
**ID:** FR-631

До готовности (readiness) control plane SHALL один раз применить полный набор ресурсов из реестра. Ошибка применения MUST останавливать старт процесса. Обязательные параметры `ROUTES__NAMESPACE`, `ROUTES__GATEWAY` и `ROUTES__LISTENER` MUST быть заданы, иначе процесс не стартует.

#### Scenario: Удалённый маршрут восстанавливается при старте
**ID:** AC-809

- **GIVEN** Market `berlin` есть в реестре, а `HTTPRoute market-berlin` удалён вручную
- **WHEN** control plane перезапускается
- **THEN** `HTTPRoute market-berlin` снова существует до того, как control plane станет ready

#### Scenario: Конфиг маршрутов не задан
**ID:** AC-810

- **WHEN** control plane запускается без `ROUTES__LISTENER`
- **THEN** процесс завершается на старте с ошибкой конфигурации

### Requirement: Список Market на публичном gateway
**ID:** FR-632

Публичный gateway SHALL отвечать на `GET /markets` сам, без вызова control plane и Cell: `200`, `application/json`, `{"markets":[{"id","name","currency"}]}` по name, затем по id. Ответ MUST оставаться доступным при недоступном control plane. Символ `%` в названии Market MUST отдаваться без изменений.

#### Scenario: Список для выбора Market
**ID:** AC-811

- **GIVEN** в реестре Market `hamburg` «Hamburg» и `berlin` «Berlin», оба в EUR
- **WHEN** браузер запрашивает `GET /markets`
- **THEN** ответ `{"markets":[{"id":"berlin","name":"Berlin","currency":"EUR"},{"id":"hamburg","name":"Hamburg","currency":"EUR"}]}`

#### Scenario: Название с процентом
**ID:** AC-812

- **GIVEN** Market с названием «Berlin 100%»
- **WHEN** браузер запрашивает `GET /markets`
- **THEN** название в ответе — «Berlin 100%»

### Requirement: Control plane вне горячего пути
**ID:** FR-633

Обработка клиентских запросов MUST NOT вызывать control plane: ни gateway, ни Cell к нему не обращаются. `ListMarkets` SHALL требовать `platform#manage_markets`. При недоступном control plane запросы по существующим маршрутам Market и `GET /markets` MUST обслуживаться; недоступными становятся только записи реестра.

#### Scenario: Control plane остановлен
**ID:** AC-813

- **GIVEN** Market `berlin` маршрутизируется
- **WHEN** control plane остановлен и клиент отправляет запрос на `/m/berlin/graphql`
- **THEN** запрос обслуживается Cell `cell-1`

#### Scenario: Список Market только оператору
**ID:** AC-814

- **WHEN** пользователь без `platform#manage_markets` вызывает `ListMarkets`
- **THEN** ответ `PERMISSION_DENIED`

### Requirement: BFF обслуживает одну Cell
**ID:** FR-634

BFF SHALL вызывать контексты только через internal gateway своей Cell (`BFF__INTERNAL_GATEWAY_ADDR`) и MUST NOT выбирать Cell или читать реестр control plane. Market запроса BFF SHALL брать из входящей metadata `x-nomnom-market`: нет значения, их несколько или формат не `^[a-z][a-z0-9-]{1,31}$` — `INVALID_ARGUMENT`. Списочные операции и `RegisterCourier` SHALL передавать Market полем `market_id`. Client schema MUST NOT содержать Query `markets`.

#### Scenario: Запрос в обход маршрута без Market
**ID:** AC-815

- **WHEN** запрос приходит в Router Cell без заголовка `X-Nomnom-Market`
- **THEN** операция завершается `INVALID_ARGUMENT`, контексты не вызываются

#### Scenario: Список ресторанов Market
**ID:** AC-816

- **WHEN** клиент запрашивает рестораны на `/m/berlin/graphql`
- **THEN** BFF вызывает `ListRestaurants(market_id: "berlin")` через internal gateway `cell-1`

### Requirement: UI адресует Market путём
**ID:** FR-635

UI SHALL получать список Market из `GET {VITE_PUBLIC_API_URL}/markets` и отправлять GraphQL-операции и SSE-подписки выбранного Market на `{VITE_PUBLIC_API_URL}/m/{market_id}/graphql`. UI MUST NOT отправлять заголовок `X-Nomnom-Market`. `VITE_PUBLIC_API_URL` MUST быть задан, иначе UI не стартует. Смена Market SHALL создавать новые GraphQL-клиенты и кеши.

#### Scenario: Операции выбранного Market
**ID:** AC-817

- **GIVEN** viewer выбрал Market `berlin`
- **WHEN** UI выполняет query и открывает подписку
- **THEN** оба запроса идут на `/m/berlin/graphql` без заголовка `X-Nomnom-Market`

#### Scenario: Смена Market
**ID:** AC-818

- **WHEN** viewer переключается с `berlin` на `hamburg`
- **THEN** следующие операции идут на `/m/hamburg/graphql`, данные `berlin` из кеша не показываются
