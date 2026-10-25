# Spec Delta

## Purpose

Определяет, как приложения nomnom выполняют синхронные вызовы бизнес-API друг друга:
единая точка маршрутизации, сохранение семантики gRPC и сетевой запрет обхода.

## ADDED Requirements

### Requirement: Синхронные вызовы бизнес-API через internal gateway

Синхронный вызов бизнес-API другого приложения SHALL проходить через internal gateway.
Это правило MUST применяться к BFF, bounded contexts и Temporal Activity workers.
Клиентские приложения SHALL обращаться только к публичному API BFF.

#### Scenario: Bounded context вызывает другой bounded context

- **WHEN** приложение вызывает gRPC-метод, опубликованный другим приложением
- **THEN** вызов отправляется на адрес internal gateway и gateway доставляет его backend'у владельца метода

#### Scenario: Temporal Activity вызывает бизнес-API

- **WHEN** Activity worker `orders` вызывает бизнес-API другого bounded context
- **THEN** вызов проходит через internal gateway так же, как вызов из API-процесса

### Requirement: Адрес gateway задаётся конфигурацией

Вызывающее приложение SHALL получать адрес gateway только из конфигурации
`INTERNAL_GATEWAY_ADDR`. Адреса, порты и имена Kubernetes Service backend'ов MUST NOT быть
настройками вызывающих приложений; соответствие логического API backend'у SHALL храниться
только в маршрутах gateway.

#### Scenario: Backend переносится в другой Service

- **WHEN** владелец API меняет Kubernetes Service или namespace своего backend
- **THEN** изменяется только маршрут gateway, а конфигурация вызывающих приложений остаётся прежней

### Requirement: Маршрутизация по имени gRPC-сервиса и метода

Gateway SHALL выбирать backend по полному имени gRPC-сервиса и методу из proto-контракта
владельца. Вызов неизвестного сервиса или метода MUST завершаться ошибкой `UNIMPLEMENTED`
без доставки какому-либо backend.

#### Scenario: Опубликованный метод

- **WHEN** caller вызывает метод, для которого есть маршрут
- **THEN** вызов получает backend, указанный в маршруте, и caller получает его ответ

#### Scenario: Метод без маршрута

- **WHEN** caller вызывает метод, отсутствующий в маршрутах, в том числе реализованный backend
- **THEN** caller получает `UNIMPLEMENTED`, а backend вызов не получает

### Requirement: Сохранение семантики gRPC

Gateway SHALL передавать backend'у gRPC metadata и W3C `traceparent` без изменений, а
caller'у — status code и message backend'а. Gateway MUST NOT сокращать deadline клиента
собственным таймаутом и SHALL передавать отмену вызова backend'у.

#### Scenario: Trace context

- **WHEN** caller отправляет `traceparent`
- **THEN** backend получает тот же `traceparent`

#### Scenario: Короткий deadline

- **WHEN** caller задаёт deadline меньше времени обработки backend'ом
- **THEN** caller получает `DEADLINE_EXCEEDED` по истечении своего deadline, а backend получает отмену

#### Scenario: Длинный deadline

- **WHEN** caller задаёт deadline больше встроенного таймаута gateway и backend укладывается в него
- **THEN** вызов завершается успешно

### Requirement: Автоматические retries только для идемпотентных методов

Gateway SHALL автоматически повторять вызов только для методов с определённой политикой
идемпотентности и только на транспортных отказах. Для остальных методов retries MUST быть
выключены. Перечень методов с retries SHALL храниться в реестре маршрутов.

#### Scenario: Неидемпотентный метод

- **WHEN** метод не объявлен идемпотентным в реестре маршрутов
- **THEN** gateway не повторяет вызов

### Requirement: Запрет обхода gateway

Backend бизнес-API SHALL принимать сетевые подключения к порту бизнес-API только от data
plane internal gateway. Прикладные pod SHALL иметь egress только к gateway, DNS и явно
разрешённой инфраструктуре своего владельца. Gateway SHALL принимать подключения только из
прикладных неймспейсов. Kubelet probes и системные компоненты Kubernetes MUST продолжать работать.

#### Scenario: Прямой вызов backend

- **WHEN** приложение подключается к Service или pod backend'а другого приложения напрямую
- **THEN** подключение отбрасывается сетевой политикой, а тот же вызов через gateway в том же окружении проходит

#### Scenario: Подключение извне прикладного контура

- **WHEN** pod вне прикладных неймспейсов подключается к gateway или backend бизнес-API
- **THEN** подключение отбрасывается

#### Scenario: Инфраструктурные подключения

- **WHEN** приложение подключается к Kafka, Temporal Server, Alloy или к БД, которой оно владеет
- **THEN** подключение разрешено, а подключение к БД другого владельца отбрасывается
