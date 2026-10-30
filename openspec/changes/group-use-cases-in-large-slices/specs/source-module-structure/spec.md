# Spec Delta

## ADDED Requirements

### Requirement: Сценарии слайса могут объединяться в группы
**ID:** FR-732

`applications/` слайса MAY содержать группы `applications/<group>/<usecase>/`. Группа SHALL называться частью бизнес-процесса на Ubiquitous Language. Группа MUST NOT содержать вложенные группы, `domains`, `infrastructure` и `di`. Общий код нескольких групп SHALL оставаться на верхнем уровне `applications/`. Порог числа сценариев и автоматическая проверка размера MUST NOT вводиться.

#### Scenario: Общий код двух групп
**ID:** AC-999

- **WHEN** общий каталог используют сценарии групп `offer` и `assignment`
- **THEN** он находится на верхнем уровне `applications/`, а не внутри одной из групп

### Requirement: Группировка delivery, order и restaurantorder не меняет поведение
**ID:** FR-733

Слайсы `delivery/slices/delivery`, `orders/slices/order` и `restaurants/slices/restaurantorder` SHALL быть сгруппированы по раскладке этого change. Перемещение MUST NOT менять имена пакетов и модулей сценариев, имена телеметрии, контракты, теги `.feature` и поведение.

#### Scenario: Перемещение сценария
**ID:** AC-1000

- **WHEN** `confirmorder` переезжает в `applications/fulfillmentfacts/confirmorder`
- **THEN** Go-пакет называется `confirmorder`, имя телеметрии и теги сценариев не изменились, а `make check` в `orders` проходит
