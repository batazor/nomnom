# 40. SLO сервисов и burn rate alerts

Date: 2026-10-26

## Status

Accepted

Уточняет [29. Grafana Dashboards as Code](0029-grafana-dashboards-as-code.md) и открытый пункт «SLO, alerts» [docs/observability.md](../observability.md); даёт SLO, из которого [26. k6 for Performance Testing](0026-k6-for-performance-testing.md) выводит thresholds.

## Context

Overview-dashboards показывают ошибки и latency, но не отвечают, укладывается ли сервис в допустимую ненадёжность и с какой скоростью её тратит. Alert rules ADR 29 срабатывают на мгновенных порогах (5% ошибок за 5m) и не различают короткий всплеск и медленную деградацию, съедающую месяц. ADR 26 запрещает придумывать thresholds без SLO, поэтому без целей нагрузочные профили не дают pass/fail.

## Decision

**SLI** считаются на входе сервиса, по метрикам инвентаря:

| SLI | Плохое событие | Метрика |
| --- | --- | --- |
| Availability (контексты) | входящий gRPC-вызов с техническим кодом: transport (`CANCELLED`, `DEADLINE_EXCEEDED`, `RESOURCE_EXHAUSTED`, `UNAVAILABLE`) или application (`UNKNOWN`, `UNIMPLEMENTED`, `INTERNAL`, `DATA_LOSS`) | `rpc_server_call_duration_seconds_count` |
| Availability (BFF) | клиентский GraphQL-запрос с ошибкой | `router_http_requests_error_total` / `router_http_requests_total`, `wg_subgraph_name=""` |
| Latency | вызов дольше границы bucket | `rpc_server_call_duration_seconds_bucket{le=…}` / `_count` |

Business rejection (`NOT_FOUND`, `FAILED_PRECONDITION`, …) — корректный ответ и бюджет не тратит. Kafka, Temporal и Outbox — асинхронные пути со своими сигналами (lag, age, DLQ); их SLO (freshness) — отдельное решение.

**Цели** — стартовые, на скользящем окне 30d; пересматриваются по накопленным данным:

| Сервис | Availability | Latency |
| --- | --- | --- |
| `orders` | 99.9% | 95% вызовов ≤ 0.25 s |
| `payments` | 99.9% | 95% вызовов ≤ 0.25 s |
| `restaurants` | 99.5% | 95% вызовов ≤ 0.5 s |
| `delivery` | 99.5% | 95% вызовов ≤ 0.5 s |
| `bff` | 99.5% | не принят: границы buckets Cosmo Router не зафиксированы |

Оформление заказа и оплата — критический путь пользователя, отсюда 99.9%. Порог latency — граница общих buckets (`SecondsBuckets`, одинаковы во всех контекстах): доля вызовов ниже границы считается точно, без интерполяции `histogram_quantile`. Цели задаются явно в `Options.SLO` (`ops/grafana/overview/services.go`); сервис без SLO не получает row и alerts.

**Error budget** = 1 − цель. Burn rate — доля плохих событий за окно, делённая на бюджет: 1 — бюджет кончится ровно за 30d.

**Dashboard.** Row SLO сразу после Service health: для каждого SLI — SLI против цели, остаток бюджета за 30d и burn rate за 1h, 6h и 3d. Row считает сервис целиком: фильтр environment есть, version и instance нет. Environment фильтруется join с `max_over_time(target_info[окно])`, чтобы instance, завершившиеся внутри окна, оставались в расчёте.

**Alerts** — multiwindow burn rate. Длинное окно доказывает расход бюджета, короткое — что он продолжается:

| Окно | Порог | Расход бюджета | Severity | For |
| --- | --- | --- | --- | --- |
| 1h и 5m | 14.4 | 2% за 1h | critical | 2m |
| 6h и 30m | 6 | 5% за 6h | warning | 15m |

Burn rate 1 за 3d (10% бюджета) только показывается на dashboard и разбирается в рабочее время. Правила генерируются рядом с остальными (`ServiceAlerts`) по тому же критерию «метрики есть».

## Consequences

Сервис получает измеримую цель, k6 — thresholds из неё (ADR 26). Мгновенные alerts ADR 29 остаются для диагностики. Запросы за 30d и 3d тяжелее остальных панелей; если backend не справится, понадобятся recording rules — отдельное изменение. Пока head sampling и тестовое окружение дают мало трафика, burn rate шумит: несколько ошибок на малом объёме расходуют заметную долю бюджета.
