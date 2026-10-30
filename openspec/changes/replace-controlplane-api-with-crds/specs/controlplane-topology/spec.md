# Spec Delta

## ADDED Requirements

### Requirement: Ресурс Cell
**ID:** FR-734

Реестр Cell SHALL храниться в cluster-scoped ресурсах `Cell` группы `controlplane.nomnom.dev/v1alpha1`. `metadata.name` SHALL быть `cell_id` формата `^[a-z][a-z0-9-]{1,31}$`, а `spec.router` — Kubernetes Service Router Cell (`namespace`, `name`, `port`). `namespace` и `name` MUST быть DNS-1123 label, `port` — от 1 до 65535. `spec` MUST быть неизменяемым. `Cell` MUST NOT содержать адрес internal gateway.

#### Scenario: Объявление Cell
**ID:** AC-1001

- **WHEN** оператор применяет `Cell` `cell-1` с `router: {namespace: nomnom-bff, name: bff, port: 3010}`
- **THEN** `Cell` создана, её `status` содержит `Ready=True`

#### Scenario: Неверный Service Router
**ID:** AC-1002

- **WHEN** оператор применяет `Cell` с `router.name: Bff_1` или `router.port: 0`
- **THEN** API server отклоняет запись, реестр не меняется

#### Scenario: Изменение Cell
**ID:** AC-1003

- **GIVEN** существует `Cell` `cell-1` с портом `3010`
- **WHEN** оператор применяет её с портом `3011`
- **THEN** API server отклоняет запись, а повторное применение с портом `3010` успешно и ничего не меняет

### Requirement: Ресурс Market
**ID:** FR-735

Реестр Market SHALL храниться в cluster-scoped ресурсах `Market` группы `controlplane.nomnom.dev/v1alpha1`. `metadata.name` SHALL быть `market_id` формата `^[a-z][a-z0-9-]{1,31}$`. `spec.displayName` MUST содержать от 1 до 64 символов без пробелов по краям, `spec.currency` — соответствовать `^[A-Z]{3}$`, `spec.timeZone` и `spec.cellRef` MUST быть непустыми. Поля `displayName`, `currency`, `timeZone` и `cellRef` MUST быть неизменяемыми.

#### Scenario: Объявление Market
**ID:** AC-1004

- **GIVEN** существует `Cell` `cell-1`
- **WHEN** оператор применяет `Market` `berlin` с `displayName: Berlin`, `currency: EUR`, `timeZone: Europe/Berlin`, `cellRef: cell-1`
- **THEN** `Market` создан, его `status` содержит `Ready=True`

#### Scenario: Неверный формат значений
**ID:** AC-1005

- **WHEN** оператор применяет `Market` с именем `Berlin_1`, `currency: eur` или пустым `timeZone`
- **THEN** API server отклоняет запись, реестр не меняется

#### Scenario: Перенос через изменение cellRef
**ID:** AC-1006

- **GIVEN** существует `Market` `berlin` с `cellRef: cell-1`
- **WHEN** оператор применяет его с `cellRef: cell-2`
- **THEN** API server отклоняет запись, Market Placement не меняется

### Requirement: Архивирование вместо удаления
**ID:** FR-736

Market SHALL архивироваться установкой `spec.archived: true`. Архивный Market MUST NOT иметь маршрута и MUST NOT входить в список Market. `spec.archived` MUST NOT возвращаться из `true` в `false`. Удаление `Market` и `Cell` MUST отклоняться `ValidatingAdmissionPolicy`, поэтому `market_id` не используется повторно. CRD и политика MUST NOT удаляться при `helm uninstall`.

#### Scenario: Архивирование Market
**ID:** AC-1007

- **GIVEN** `Market` `hamburg` маршрутизируется
- **WHEN** оператор устанавливает `spec.archived: true`
- **THEN** `HTTPRoute market-hamburg` удалён, `GET /markets` не содержит `hamburg`, запрос на `/m/hamburg/graphql` получает `404`

#### Scenario: Возврат из архива
**ID:** AC-1008

- **GIVEN** `Market` `hamburg` архивирован
- **WHEN** оператор устанавливает `spec.archived: false`
- **THEN** API server отклоняет запись

#### Scenario: Удаление Market и Cell
**ID:** AC-1009

- **WHEN** оператор удаляет `Market` `hamburg` или `Cell` `cell-1`
- **THEN** API server отклоняет запрос, ресурс остаётся

### Requirement: Проверка реестра контроллером
**ID:** FR-737

Control plane SHALL записывать результат проверки и публикации в условие `Ready` в `status` с `observedGeneration`. `Market`, чья Cell не существует, SHALL иметь `Ready=False` с reason `CellNotFound`, а с часовым поясом вне встроенной базы IANA — `Ready=False` с reason `InvalidTimeZone`. Такой `Market` MUST NOT получать маршрут и MUST NOT мешать публикации остальных Market. При ошибке применения ресурсов `Ready` SHALL быть `False` с reason `RoutesUnavailable`.

#### Scenario: Market объявлен раньше Cell
**ID:** AC-1010

- **GIVEN** `Cell` `cell-2` не существует
- **WHEN** оператор применяет `Market` `hamburg` с `cellRef: cell-2`, а затем `Cell` `cell-2`
- **THEN** сначала `hamburg` имеет `Ready=False` и reason `CellNotFound`, а после появления Cell — `Ready=True` и маршрут

#### Scenario: Неизвестный часовой пояс
**ID:** AC-1011

- **GIVEN** `Market` `berlin` маршрутизируется
- **WHEN** оператор применяет `Market` `hamburg` с `timeZone: Mars/Olympus`
- **THEN** `hamburg` имеет `Ready=False` и reason `InvalidTimeZone`, маршрута `hamburg` нет, а маршрут `berlin` работает

### Requirement: Доступ к реестру через Kubernetes RBAC
**ID:** FR-738

Запись в `Cell` и `Market` SHALL разрешаться только Kubernetes RBAC через `ClusterRole` `nomnom-topology-editor`. ServiceAccount control plane MUST иметь только чтение `Cell` и `Market` и запись их подресурса `status`. Разрешение `platform#manage_markets` MUST NOT существовать в схеме SpiceDB.

#### Scenario: Сервис без роли
**ID:** AC-1012

- **WHEN** ServiceAccount `nomnom-bff/bff` создаёт `Market`
- **THEN** API server отвечает `Forbidden`

#### Scenario: Control plane не меняет spec
**ID:** AC-1013

- **WHEN** ServiceAccount control plane изменяет `spec` `Market`
- **THEN** API server отвечает `Forbidden`, а запись в `markets/status` разрешена

### Requirement: Control plane без API и базы данных
**ID:** FR-739

Control plane SHALL работать контроллером Kubernetes. Он MUST NOT предоставлять gRPC API и бизнес-порт и MUST NOT зависеть от PostgreSQL и SpiceDB. Сервис `nomnom.controlplane.v1.ControlPlaneService` MUST NOT существовать.

#### Scenario: Состав зависимостей
**ID:** AC-1014

- **WHEN** control plane развёрнут в kind
- **THEN** у него нет конфигурации `POSTGRES__DSN`, `SPICEDB__*` и `IDENTITY__*`, NetworkPolicy не разрешает ему egress к PostgreSQL и SpiceDB, а маршруты публикуются
