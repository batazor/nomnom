# Tasks

- [x] 1.1 UUID-генератор и `StoredCourierLocator` в production-сборке; тесты AC-631, AC-632 (`FR-541`, `FR-542`).
- [ ] 1.2 Роли хранилища вместо `ProcessStore`/`SharedStore` как service locator; узкий `DeliveryPorts` (`FR-543`).
- [ ] 1.3 Сервисы `end_assignment`, `select_candidate`, `make_offer`; Replan под одной проверкой и UoW; одна функция снятия назначения, сверка с FR-447 (`AC-633`).
- [ ] 1.4 Разделить `grpc/identity.rs` на `jwks`, `service_token`, `user_assertion`, `config`; общие векторы проходят.
- [ ] 1.5 README; `cargo test`, `cargo clippy --all-targets`, rustfmt; `openspec validate fix-delivery-runtime-adapters-and-roles --strict`.
