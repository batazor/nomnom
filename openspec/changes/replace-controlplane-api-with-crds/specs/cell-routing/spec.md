# Spec Delta

## RENAMED Requirements

- FROM: `### Requirement: Control plane публикует маршруты после записи`
- TO: `### Requirement: Control plane приводит маршруты к реестру`
- FROM: `### Requirement: Control plane приводит маршруты к реестру при старте`
- TO: `### Requirement: Control plane готов после публикации маршрутов`

## MODIFIED Requirements

### Requirement: Control plane приводит маршруты к реестру
**ID:** FR-630

При любом изменении `Cell`, `Market` или управляемых им ресурсов на публичном gateway control plane SHALL применять полный набор ресурсов из реестра: маршрут каждого проверенного неархивного Market и список Market. Управляемые маршруты Market, которых нет в наборе, MUST удаляться. Ошибка применения MUST приводить к повтору с backoff без действий оператора. Параллельные изменения реестра MUST NOT оставлять ресурсы по более старому снимку реестра. Control plane MUST NOT опрашивать реестр по таймеру.

#### Scenario: Новый Market сразу маршрутизируется
**ID:** AC-806

- **GIVEN** существует `Cell` `cell-1`
- **WHEN** оператор применяет `Market` `berlin` с `cellRef: cell-1`
- **THEN** существует `HTTPRoute market-berlin`, запрос на `/m/berlin/graphql` доходит до Router `cell-1`, а `GET /markets` содержит `berlin`

#### Scenario: Kubernetes API недоступен
**ID:** AC-807

- **GIVEN** control plane не может применить ресурсы
- **WHEN** оператор применяет новый `Market`
- **THEN** `Market` остаётся в реестре, а после восстановления доступа маршрут создаётся без повторного действия оператора и `Ready` становится `True`

#### Scenario: Параллельное создание двух Market
**ID:** AC-808

- **WHEN** оператор одновременно применяет `Market` `berlin` и `hamburg`
- **THEN** в итоге существуют маршруты обоих Market, а `GET /markets` содержит оба

#### Scenario: Удалённый маршрут восстанавливается
**ID:** AC-1015

- **GIVEN** `Market` `berlin` есть в реестре, control plane работает
- **WHEN** `HTTPRoute market-berlin` удаляют вручную
- **THEN** маршрут снова существует без перезапуска control plane

### Requirement: Control plane готов после публикации маршрутов
**ID:** FR-631

Control plane SHALL становиться ready только после первого успешного применения полного набора ресурсов. Пока применение не удалось, он MUST оставаться not ready и повторять попытки. Обязательные параметры `ROUTES__NAMESPACE`, `ROUTES__GATEWAY` и `ROUTES__LISTENER` MUST быть заданы, иначе процесс не стартует.

#### Scenario: Удалённый маршрут восстанавливается при старте
**ID:** AC-809

- **GIVEN** `Market` `berlin` есть в реестре, а `HTTPRoute market-berlin` удалён, пока control plane был остановлен
- **WHEN** control plane запускается
- **THEN** `HTTPRoute market-berlin` существует до того, как control plane станет ready

#### Scenario: Конфиг маршрутов не задан
**ID:** AC-810

- **WHEN** control plane запускается без `ROUTES__LISTENER`
- **THEN** процесс завершается на старте с ошибкой конфигурации

### Requirement: Control plane вне горячего пути
**ID:** FR-633

Обработка клиентских запросов MUST NOT вызывать control plane: ни gateway, ни Cell к нему не обращаются. Чтение ресурсов `Market` через Kubernetes API SHALL требовать RBAC оператора. При недоступном control plane запросы по существующим маршрутам Market и `GET /markets` MUST обслуживаться. Изменения реестра при этом SHALL приниматься API server и применяться после возвращения control plane.

#### Scenario: Control plane остановлен
**ID:** AC-813

- **GIVEN** Market `berlin` маршрутизируется
- **WHEN** control plane остановлен и клиент отправляет запрос на `/m/berlin/graphql`
- **THEN** запрос обслуживается Cell `cell-1`

#### Scenario: Список Market только оператору
**ID:** AC-814

- **WHEN** ServiceAccount `nomnom-bff/bff` читает ресурсы `Market` через Kubernetes API
- **THEN** API server отвечает `Forbidden`

#### Scenario: Изменение реестра при остановленном control plane
**ID:** AC-1016

- **GIVEN** control plane остановлен
- **WHEN** оператор применяет `Market` `hamburg`, а затем control plane запускается
- **THEN** `hamburg` получает маршрут без повторного действия оператора

## REMOVED Requirements

### Requirement: Router Cell в реестре
**ID:** FR-629

**Reason**: `RegisterCell` заменён ресурсом `Cell`.

**Migration**: Требования к Router Cell перенесены в `FR-734` capability `controlplane-topology`.
