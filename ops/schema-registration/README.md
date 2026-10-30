# schema-registration

Регистрирует Protobuf-схемы Integration Events из buf-модулей контекстов в Schema Registry
(OpenSpec `integration-event-schemas`: FR-712, FR-713, FR-717). Это единственный, кто пишет в
Registry: SMT `SchemaFraming` и consumers только читают.

- Файлы событий и модули, разрешающие их импорты, перечислены в [`event-files.yaml`](event-files.yaml).
- Subject каждого сообщения верхнего уровня — его полное имя, то есть `event_type`. Импортированные
  файлы модулей регистрируются под subject = путь файла и указываются в `references`; well-known
  types Registry знает сам.
- Перед регистрацией глобальный режим совместимости становится `FULL_TRANSITIVE`. Несовместимая
  версия (например, смена типа поля или удаление сообщения) — `409` и ненулевой код выхода.
  Удаление поля и перестановку номеров Registry пропускает: их ловит `buf breaking` в PR.
- Повторный запуск без изменений `.proto` не создаёт новых версий.

```bash
SCHEMA_REGISTRY__URL=http://127.0.0.1:8081 go run . --root ../.. --config event-files.yaml
go test ./...   # Kafka и Confluent Schema Registry из testcontainers
```

В kind утилиту запускает hook Job `schema-registration`
(chart [`ops/kind/schema-registration`](../kind/schema-registration)), release `schema-registration` ставится до `kafka-connect`.
