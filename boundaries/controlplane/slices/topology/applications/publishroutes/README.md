# Publish Routes

- **Тип:** сценарий reconcile control plane; запускается при старте, при изменении `Cell` и `Market` и при изменении управляемых ресурсов публичного gateway.
- **Порядок:** снимок реестра → проверка значений, часового пояса и Cell каждого Market → server-side apply ресурсов публичного gateway → удаление маршрутов Market вне набора → запись `Ready` в `status`.
- **Результат:** маршрут каждого проверенного неархивного Market и список Market применены. Market с неизвестной Cell (`CellNotFound`), неизвестным поясом (`InvalidTimeZone`) или неверным значением (`InvalidValue`) не публикуется и не мешает остальным. Архивный Market получает `Ready=False` и reason `Archived`.
- **Ошибка:** не применилось — `Ready=False`, `RoutesUnavailable`, `ErrRoutesUnavailable`; контроллер повторяет публикацию с backoff.

Handler: [`publishroutes.go`](publishroutes.go); адаптеры — `infrastructure/registry` и `infrastructure/gatewayroutes`.
