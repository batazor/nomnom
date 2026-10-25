# Spec Delta

## ADDED Requirements

### Requirement: List Restaurants доступен по gRPC
**ID:** FR-496

`restaurants` SHALL публиковать RPC `ListRestaurants` с размером страницы, курсором и флагом закрытых ресторанов по правилам FR-205, без authorization.

#### Scenario: Первая страница открытых ресторанов
**ID:** AC-565

- **WHEN** вызывающий запрашивает `ListRestaurants` с `page_size = 1` при двух открытых ресторанах
- **THEN** ответ содержит один ресторан и непустой `next_page_token`
