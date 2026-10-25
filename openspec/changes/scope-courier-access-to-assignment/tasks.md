# Tasks

## 1. Schema

- [ ] 1.1 `authorization/schema.zed`: убрать `order#courier`, `view = customer + restaurant->view_orders + delivery->act_as_courier`; `platform#dispatch_delivery = operator`. `validation.yaml`: assertions AC-517, AC-695; `make authz-validate`.

## 2. delivery

- [ ] 2.1 `Delivery::active_courier()` в домене: `courier_id` при assigned или picked up; тесты по всем состояниям.
- [ ] 2.2 Confirm Delivery и Report Delivery Failure отзывают `delivery#courier` через `release_assignment` с записью revision; `Revoke::Keep` удалён. Тесты AC-690, AC-691, AC-692.
- [ ] 2.3 Reconciler строит ожидаемые `delivery#courier` из `active_courier()`; тест AC-693.
- [ ] 2.4 Report Delivery Failure от диспетчера проверяет `platform#dispatch_delivery`; SpiceDB mapping и тест AC-694.
- [ ] 2.5 README delivery, use-case README Confirm Delivery и Report Delivery Failure.

## 3. orders

- [x] 3.1 SpiceDB adapter: Record Courier Assignment пишет только `order#delivery` (смена Delivery — `TOUCH`+`DELETE` одним запросом, та же Delivery — без запроса); Release удаляет `order#delivery` только при `DeliveryCancelled`. Тесты AC-518, AC-519, AC-534, AC-535.
- [x] 3.2 Reconciler не сверяет `courier`; тесты AC-523, AC-524, AC-667.
- [x] 3.3 README orders, README Record Courier Assignment и Release.

## 4. Документация и проверка

- [ ] 4.1 `docs/security.md`: путь доступа курьера и право диспетчера.
- [ ] 4.2 `openspec validate scope-courier-access-to-assignment --strict`; проверки delivery и orders.
