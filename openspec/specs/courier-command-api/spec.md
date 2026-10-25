# courier-command-api Specification

## Purpose
TBD - created by archiving change close-order-lifecycle-in-kind. Update Purpose after archive.

## Requirements

### Requirement: Сценарии курьера доступны по gRPC
**ID:** FR-580

`delivery` SHALL публиковать по gRPC Register Courier, Update Courier Location, Set Courier Availability, List Available Delivery Offers, Accept Delivery Offer, Reject Delivery Offer, Confirm Pickup, Confirm Delivery и Report Delivery Failure. Каждый RPC MUST вызывать свой application handler с его authorization, Unit of Work и Outbox; ошибки MUST переводиться общей таблицей transport `delivery`.

#### Scenario: Курьер проходит доставку
**ID:** AC-730

- **WHEN** курьер с `courier#act` принимает предложение, подтверждает Pickup и Delivery Completion через gRPC
- **THEN** доставка delivered, курьер available, а Outbox содержит факты назначения, Pickup и Completion в этом порядке

#### Scenario: Чужой курьер
**ID:** AC-731

- **WHEN** subject без `courier#act` на указанного курьера вызывает `ConfirmPickup`
- **THEN** ответ `PERMISSION_DENIED`, доставка и Outbox не меняются

### Requirement: Регистрация курьера — действие оператора
**ID:** FR-581

`RegisterCourier` MUST требовать `platform:nomnom#register_courier` и выполнять `TOUCH courier#self` нового курьера до сохранения.

#### Scenario: Регистрация оператором
**ID:** AC-732

- **WHEN** operator регистрирует курьера для пользователя
- **THEN** ответ содержит `courier_id`, а пользователь получает `courier#act` на этого курьера

#### Scenario: Регистрация не оператором
**ID:** AC-733

- **WHEN** пользователь без relation `operator` вызывает `RegisterCourier`
- **THEN** ответ `PERMISSION_DENIED`, курьер не создан
