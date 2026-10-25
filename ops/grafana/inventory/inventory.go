// Package inventory — единственный источник правды о метриках, на которых строятся
// overview-dashboards. docs/observability/metric-inventory.md генерируется из него.
//
// Панель становится живой только когда все её метрики имеют статус Present для
// service.name, который она запрашивает; иначе генератор рисует TODO-панель со
// ссылкой на инвентарь. Статус меняется здесь, когда инструментация доставлена в
// Mimir существующим pipeline, а не когда она только запланирована.
package inventory

import (
	"slices"
	"strings"
)

type Status string

const (
	// Present — метрика экспортируется сервисом по OTLP, а имена и labels
	// подтверждены приёмом через Alloy в Mimir (см. VerifiedPipeline).
	Present Status = "есть"
	// Expected — метрика принята в архитектуре, но не экспортируется или не доставляется.
	Expected Status = "ожидается"
)

type Type string

const (
	Counter   Type = "counter"
	Histogram Type = "histogram"
	Gauge     Type = "gauge"
)

// Service names (OTel service.name) deployable roles. Значения совпадают с
// OTEL_SERVICE_NAME в Helm values; job в Mimir — "nomnom/<service.name>".
const (
	Orders         = "orders"
	OrdersWorker   = "orders-worker"
	OrdersConsumer = "orders-consumer"
	Restaurants    = "restaurants"
	Delivery       = "delivery"
	Payments       = "payments"
	BFF            = "bff"
)

var AllServices = []string{Orders, OrdersWorker, OrdersConsumer, Restaurants, Delivery, Payments, BFF}

// Инфраструктурные цели Platform / Overview: собираются Alloy Prometheus scrape, а не OTLP,
// поэтому job — "infra/<target>", без target_info.
const (
	SpiceDB          = "spicedb"
	PostgreSQLServer = "postgresql"
)

var InfraTargets = []string{SpiceDB, PostgreSQLServer}

// Bounded contexts с ролями на технологиях, а не на имени.
var (
	goServices      = []string{Orders, OrdersWorker, OrdersConsumer}
	grpcServers     = []string{Orders, Restaurants, Delivery, Payments}
	contextServices = []string{Orders, OrdersWorker, OrdersConsumer, Restaurants, Delivery, Payments}
	otelConsumers   = []string{Restaurants, Delivery, Payments}
)

type Section struct {
	ID    string
	Title string
	Intro string
}

type Metric struct {
	// Name — имя в Mimir после OTLP-трансляции (UnderscoreEscapingWithSuffixes):
	// unit suffix и _total добавлены. Для histogram — базовое имя без _bucket.
	Name    string
	Section string
	Type    Type
	// Source — OTel instrument/semconv или компонент, который создаёт метрику.
	Source string
	Labels []string
	// Status по service.name; сервис вне карты метрику не экспортирует.
	Status map[string]Status
	Note   string
}

const RepoURL = "https://github.com/batazor/nomnom/blob/main/"

const DocPath = "docs/observability/metric-inventory.md"

// VerifiedPipeline — где подтверждены метрики со статусом Present.
const VerifiedPipeline = "2026-10-25: `orders`, `orders-worker`, `orders-consumer` → OTLP gRPC → Grafana Alloy → OTLP/HTTP → Mimir 3.2.3 с `-distributor.otel-metric-suffixes-enabled=true` (локальный стенд Docker, Temporal dev server). Тестовая доставка из kind — Grafana Cloud (`ops/grafana-cloud`)."

// Exemplars включаются в запросах только когда это подтверждено end-to-end:
// SDK histograms с exemplars, Alloy и Mimir (-ingester.max-global-exemplars-per-user).
const ExemplarsEnabled = false

// verified — статусы, где часть сервисов подтверждена в Mimir (present), остальные ожидаются.
func verified(present []string, others ...string) map[string]Status {
	m := expected(others...)
	for _, s := range present {
		m[s] = Present
	}
	return m
}

func expected(services ...string) map[string]Status {
	m := make(map[string]Status, len(services))
	for _, s := range services {
		m[s] = Expected
	}
	return m
}

var Sections = []Section{
	{"resource", "Resource и идентичность", "OTel resource в Mimir: `job=\"nomnom/<service.name>\"`, `instance=<service.instance.id>`, остальные resource attributes — в `target_info`. Promotion resource attributes не используется (docs/observability.md), поэтому environment и version фильтруются join с `target_info`."},
	{"rpc", "gRPC (server и client через internal gateway)", "`otelgrpc` v0.72 (stats handler) в `orders`; grpc-node / tonic / grpcio — при подключении в других контекстах. Используются актуальные semconv RPC: `rpc.server.call.duration`/`rpc.client.call.duration` в секундах, статус — имя кода в `rpc_response_status_code`, сервис и метод — в `rpc_method`."},
	{"graphql", "GraphQL / HTTP (BFF, Cosmo Router)", "Встроенная telemetry Cosmo Router. Router экспортирует метрики по умолчанию, но OTLP exporter к Alloy в `config.yaml` не настроен и Alloy не развёрнут — метрики не доставляются."},
	{"runtime", "Runtime", "Go: `go.opentelemetry.io/contrib/instrumentation/runtime` (semconv runtime Go). Node.js: `@opentelemetry/instrumentation-runtime-node`. Python: `opentelemetry-instrumentation-system-metrics`. Rust: semconv process metrics; метрики tokio runtime не имеют semconv и не инвентаризированы."},
	{"db", "Database client (PostgreSQL, MongoDB)", "Semconv `db.client.*`: `pgx` + otelpgx в `orders`, MongoDB Node.js driver instrumentation в `restaurants`. Хранилища `delivery` и `payments` не выбраны (ADR 8) — метрик для них нет. Серверные метрики PostgreSQL — Platform / Overview (раздел [PostgreSQL](#postgresql)), MongoDB — пока нет; в overview сервисов не дублируются."},
	{"watermill", "Kafka: встроенные метрики Watermill", "`components/metrics.PrometheusMetricsBuilder`, namespace `watermill`, subsystem — deployable role (`consumer`, `outbox_forwarder`). Экспорт через OTel Prometheus bridge (`orders/pkg/telemetry`) в общий OTLP pipeline; имена Prometheus сохраняются без изменений. Референс панелей — Grafana dashboard 9777, отдельный dashboard не создаётся."},
	{"messaging-otel", "Kafka: semconv messaging (не-Go контексты)", "Kafka-клиенты TypeScript, Rust и Python не выбраны; при выборе используется OTel messaging semconv."},
	{"messaging", "Kafka: собственный OTel middleware и consumer lag", "Middleware в composition root поверх Watermill (и эквивалент в других runtime): то, чего нет во встроенных метриках. Порядок: OTel/metrics → Retry (transient) → PoisonQueue (permanent) → handler. Attributes только `nomnom.messaging.handler`, `nomnom.event.type`, `messaging.destination.name`, `nomnom.error.class`; без `event_id`/`aggregate_id`. Consumer lag — Alloy `prometheus.exporter.kafka`, не Watermill."},
	{"outbox", "Outbox", "SQL-метрики Forwarder-роли (опрос таблицы Outbox), не publish-декоратор."},
	{"usecase", "Application use cases", "Декоратор application handlers в composition root (docs/observability.md#use-cases). Один histogram на все use cases; `outcome`: success | rejected | error — бизнес-отказ отделён от технического сбоя. `error_type` — закрытый набор slice, пуст при success. Без ID, subject и payload."},
	{"uow", "Unit of Work", "Декоратор Unit of Work (ADR 20). Deadlock/serialization — классы retry, а не SQL-панели поверх DB instrumentation."},
	{"fsm", "Domain Lifecycle (FSM)", "Декоратор применения FSM-перехода (ADR 21). Labels допустимы только с закрытым набором значений из FSM-таблицы."},
	{"temporal", "Temporal", "Temporal Go SDK `MetricsHandler` (contrib/opentelemetry, `UseMonotonicCounters`) в `orders-worker` и клиенте `orders-consumer`. Timers — histograms в секундах с границами `telemetry.SecondsBuckets`; без монотонных counters SDK пишет UpDownCounter и в Mimir counters приходят без `_total`."},
	{"kitchen", "Kitchen", "Gauges kitchen queue — projection/polling Kitchen Board, не выводятся из counters. Consumer lag ≠ Kitchen Queue Size."},
	{"authz", "Authorization (SpiceDB)", "Декоратор порта авторизации (ADR 18). `denied` — бизнес-результат, `error`/`timeout` — технический сбой. Без subject/resource ID."},
	{"provider", "Payment provider", "Клиентский адаптер платёжного провайдера; провайдер не выбран."},
	{"domain", "Domain metrics", "Бизнес-счётчики увеличиваются в точке идемпотентно подтверждённого перехода (docs/observability.md). Финансовые значения и ID не labels. Gauge «currently in state» требует projection/polling и из counters не выводится."},
	{"pods", "Pods (Kubernetes)", "Alloy: cAdvisor kubelet через API server proxy и kube-state-metrics (только pods прикладных неймспейсов), `job=\"infra/kubernetes\"`. Метрики Prometheus, без `target_info`: сервис выбирается `namespace` и префиксом `pod`. Row Pods в overview каждого сервиса и в Platform / Overview."},
	{"spicedb", "SpiceDB (сервер)", "Alloy scrape `:9090` pod SpiceDB, `job=\"infra/spicedb\"`. Серверная сторона; клиентская — row Authorization сервисов. Platform / Overview."},
	{"postgresql", "PostgreSQL (сервер)", "Alloy `prometheus.exporter.postgres` по владельцу (ADR 8), `job=\"infra/postgresql\"`, label `owner`; `stat_database` и размер — только база владельца. Клиентская сторона — row Dependencies сервисов. Platform / Overview."},
}

var Metrics = []Metric{
	// resource
	{Name: "target_info", Section: "resource", Type: Gauge, Source: "OTel resource → Mimir OTLP", Labels: []string{"job", "instance", "deployment_environment_name", "service_version"}, Status: verified([]string{OrdersConsumer}, Orders, OrdersWorker, Restaurants, Delivery, Payments, BFF), Note: "Нужна для variables `environment`/`version`/`service_instance`, фильтра environment и deployment annotations."},

	// rpc
	{Name: "rpc_server_call_duration_seconds", Section: "rpc", Type: Histogram, Source: "`rpc.server.call.duration` (otelgrpc v0.72)", Labels: []string{"rpc_system_name", "rpc_method", "rpc_response_status_code"}, Status: verified([]string{Orders}, Restaurants, Delivery, Payments), Note: "`rpc_method` — `Service/Method`; `rpc_response_status_code` — имя кода (`OK`, `NOT_FOUND`). Инструментации других runtime закрепляются на тех же semconv-именах."},
	{Name: "rpc_client_call_duration_seconds", Section: "rpc", Type: Histogram, Source: "`rpc.client.call.duration` (otelgrpc v0.72)", Labels: []string{"rpc_system_name", "rpc_method", "rpc_response_status_code", "error_type", "server_address", "server_port"}, Status: verified([]string{OrdersWorker}, BFF), Note: "Вызовы через internal gateway (ADR 15); `server_address`/`server_port` — адрес gateway. SpiceDB — в row Authorization."},

	// graphql
	{Name: "router_http_requests_total", Section: "graphql", Type: Counter, Source: "Cosmo `router.http.requests`", Labels: []string{"wg_operation_type", "wg_operation_name", "wg_subgraph_name", "http_status_code"}, Status: expected(BFF), Note: "`wg_operation_name` ограничен persisted operations клиента."},
	{Name: "router_http_requests_error_total", Section: "graphql", Type: Counter, Source: "Cosmo `router.http.requests.error`", Labels: []string{"wg_operation_type", "wg_operation_name", "wg_subgraph_name", "http_status_code"}, Status: expected(BFF), Note: "GraphQL errors в ответе 200."},
	{Name: "router_http_request_duration_milliseconds", Section: "graphql", Type: Histogram, Source: "Cosmo `router.http.request.duration_milliseconds`", Labels: []string{"wg_operation_type", "wg_operation_name", "wg_subgraph_name", "http_status_code"}, Status: expected(BFF), Note: "Без `wg_subgraph_name` — клиентский запрос, с ним — вызов subgraph (BFF plugin → internal gateway)."},

	// runtime
	{Name: "process_cpu_time_seconds_total", Section: "runtime", Type: Counter, Source: "`process.cpu.time`", Labels: []string{"cpu_mode"}, Status: verified([]string{Orders, OrdersWorker, OrdersConsumer}, Restaurants, Delivery, Payments), Note: "Go: `go.opentelemetry.io/contrib/instrumentation/host` (метрики `system.*` отбрасываются View)."},
	{Name: "process_memory_usage_bytes", Section: "runtime", Type: Gauge, Source: "`process.memory.usage`", Status: expected(Restaurants, Delivery, Payments), Note: "Для Go не экспортируется; память Go-ролей — `go_memory_used_bytes`."},
	{Name: "process_thread_count", Section: "runtime", Type: Gauge, Source: "`process.thread.count`", Status: expected(Delivery, Payments), Note: "Python: worker model gRPC thread pool."},
	{Name: "go_memory_used_bytes", Section: "runtime", Type: Gauge, Source: "`go.memory.used`", Labels: []string{"go_memory_type"}, Status: verified([]string{Orders, OrdersWorker, OrdersConsumer})},
	{Name: "go_memory_allocated_bytes_total", Section: "runtime", Type: Counter, Source: "`go.memory.allocated`", Status: verified([]string{Orders, OrdersWorker, OrdersConsumer})},
	{Name: "go_memory_gc_goal_bytes", Section: "runtime", Type: Gauge, Source: "`go.memory.gc.goal`", Status: verified([]string{Orders, OrdersWorker, OrdersConsumer})},
	{Name: "go_goroutine_count", Section: "runtime", Type: Gauge, Source: "`go.goroutine.count`", Status: verified([]string{Orders, OrdersWorker, OrdersConsumer})},
	{Name: "go_processor_limit", Section: "runtime", Type: Gauge, Source: "`go.processor.limit`", Status: verified([]string{Orders, OrdersWorker, OrdersConsumer})},
	{Name: "v8js_memory_heap_used_bytes", Section: "runtime", Type: Gauge, Source: "`v8js.memory.heap.used`", Labels: []string{"v8js_heap_space_name"}, Status: expected(Restaurants)},
	{Name: "v8js_memory_heap_limit_bytes", Section: "runtime", Type: Gauge, Source: "`v8js.memory.heap.limit`", Labels: []string{"v8js_heap_space_name"}, Status: expected(Restaurants)},
	{Name: "nodejs_eventloop_delay_p99_seconds", Section: "runtime", Type: Gauge, Source: "`nodejs.eventloop.delay.p99`", Status: expected(Restaurants)},
	{Name: "v8js_gc_duration_seconds", Section: "runtime", Type: Histogram, Source: "`v8js.gc.duration`", Labels: []string{"v8js_gc_type"}, Status: expected(Restaurants)},
	{Name: "cpython_gc_collections_total", Section: "runtime", Type: Counter, Source: "`cpython.gc.collections`", Labels: []string{"cpython_gc_generation"}, Status: expected(Payments)},

	// db
	{Name: "db_client_operation_duration_seconds", Section: "db", Type: Histogram, Source: "`db.client.operation.duration`", Labels: []string{"db_system_name", "db_operation_name", "db_collection_name", "error_type"}, Status: expected(Orders, OrdersWorker, OrdersConsumer, Restaurants)},
	{Name: "db_client_connection_count", Section: "db", Type: Gauge, Source: "`db.client.connection.count`", Labels: []string{"db_client_connection_pool_name", "db_client_connection_state"}, Status: expected(Orders, OrdersWorker, OrdersConsumer, Restaurants)},
	{Name: "db_client_connection_max", Section: "db", Type: Gauge, Source: "`db.client.connection.max`", Labels: []string{"db_client_connection_pool_name"}, Status: expected(Orders, OrdersWorker, OrdersConsumer, Restaurants)},
	{Name: "db_client_connection_wait_time_seconds", Section: "db", Type: Histogram, Source: "`db.client.connection.wait_time`", Labels: []string{"db_client_connection_pool_name"}, Status: expected(Orders, OrdersWorker, OrdersConsumer, Restaurants)},

	// watermill (built-in)
	{Name: "watermill_consumer_subscriber_messages_received_total", Section: "watermill", Type: Counter, Source: "`PrometheusMetricsBuilder` subsystem `consumer`", Labels: []string{"acked", "handler_name", "subscriber_name"}, Status: verified([]string{OrdersConsumer}), Note: "`acked`: acked | nacked. Через OTel Prometheus bridge (`pkg/telemetry`)."},
	{Name: "watermill_consumer_handler_execution_time_seconds", Section: "watermill", Type: Histogram, Source: "`PrometheusMetricsBuilder` subsystem `consumer`", Labels: []string{"handler_name", "success"}, Status: verified([]string{OrdersConsumer}), Note: "Снаружи DLQ: `success=\"false\"` — только исчерпанный transient (nack); permanent уходит в DLQ и считается успехом."},
	{Name: "watermill_consumer_publish_time_seconds", Section: "watermill", Type: Histogram, Source: "`PrometheusMetricsBuilder` subsystem `consumer`", Labels: []string{"success", "handler_name", "publisher_name"}, Status: verified([]string{OrdersConsumer}), Note: "Publisher consumer-роли — dead letters в `<stream>.dlq`."},
	{Name: "watermill_outbox_forwarder_publish_time_seconds", Section: "watermill", Type: Histogram, Source: "`PrometheusMetricsBuilder` subsystem `outbox_forwarder`", Labels: []string{"success", "handler_name", "publisher_name"}, Status: expected(goServices...), Note: "Handler `orders-outbox-forwarder` (`cmd/orders-forwarder`); telemetry bootstrap forwarder-роли ещё не подключён."},

	// messaging-otel
	{Name: "messaging_client_consumed_messages_total", Section: "messaging-otel", Type: Counter, Source: "`messaging.client.consumed.messages`", Labels: []string{"messaging_system", "messaging_destination_name", "messaging_consumer_group_name", "error_type"}, Status: expected(otelConsumers...)},
	{Name: "messaging_client_sent_messages_total", Section: "messaging-otel", Type: Counter, Source: "`messaging.client.sent.messages`", Labels: []string{"messaging_system", "messaging_destination_name", "error_type"}, Status: expected(otelConsumers...)},
	{Name: "messaging_client_operation_duration_seconds", Section: "messaging-otel", Type: Histogram, Source: "`messaging.client.operation.duration`", Labels: []string{"messaging_system", "messaging_operation_name", "messaging_destination_name", "error_type"}, Status: expected(otelConsumers...)},

	// messaging (custom middleware + lag)
	{Name: "nomnom_messaging_process_duration_seconds", Section: "messaging", Type: Histogram, Source: "OTel middleware `nomnom.messaging.process.duration`", Labels: []string{"nomnom_messaging_handler", "nomnom_event_type", "messaging_destination_name", "nomnom_error_class"}, Status: verified([]string{OrdersConsumer}, Restaurants, Delivery, Payments), Note: "`error_class` только при неуспехе: permanent | deserialization | business | transient; успешный retry — без класса. `handler_name` — handler group."},
	{Name: "nomnom_messaging_retry_attempts_total", Section: "messaging", Type: Counter, Source: "OTel middleware `nomnom.messaging.retry.attempts`", Labels: []string{"nomnom_messaging_handler", "nomnom_event_type", "messaging_destination_name"}, Status: verified([]string{OrdersConsumer}, Restaurants, Delivery, Payments), Note: "Ретраится только класс transient, поэтому `error_class` не пишется."},
	{Name: "nomnom_messaging_poison_messages_total", Section: "messaging", Type: Counter, Source: "OTel middleware вокруг PoisonQueue", Labels: []string{"nomnom_messaging_handler", "nomnom_event_type", "messaging_destination_name", "nomnom_error_class"}, Status: verified([]string{OrdersConsumer}, Restaurants, Delivery, Payments), Note: "Сообщения, отправленные в `<stream>.dlq`; `error_class`: permanent | deserialization."},
	{Name: "nomnom_messaging_message_age_seconds", Section: "messaging", Type: Histogram, Source: "OTel middleware", Labels: []string{"nomnom_messaging_handler", "nomnom_event_type", "messaging_destination_name"}, Status: verified([]string{OrdersConsumer}, Restaurants, Delivery, Payments), Note: "`now − occurred_at` в момент обработки."},
	{Name: "nomnom_inbox_duplicates_total", Section: "messaging", Type: Counter, Source: "OTel middleware / Inbox adapter", Labels: []string{"nomnom_messaging_handler", "nomnom_event_type", "messaging_destination_name"}, Status: verified([]string{OrdersConsumer}, Restaurants, Delivery, Payments), Note: "Outcome duplicate Inbox (`(consumer, event_id)` уже обработан)."},
	{Name: "kafka_consumergroup_lag", Section: "messaging", Type: Gauge, Source: "Alloy `prometheus.exporter.kafka`", Labels: []string{"consumergroup", "topic", "partition"}, Status: expected(OrdersConsumer, Restaurants, Delivery, Payments), Note: "Серверная метрика с job exporter'а; фильтр по `consumergroup` сервиса, без target_info join."},

	// outbox
	{Name: "nomnom_outbox_pending_messages", Section: "outbox", Type: Gauge, Source: "SQL-метрика Forwarder", Labels: []string{"messaging_destination_name"}, Status: expected(contextServices...)},
	{Name: "nomnom_outbox_oldest_pending_age_seconds", Section: "outbox", Type: Gauge, Source: "SQL-метрика Forwarder", Labels: []string{"messaging_destination_name"}, Status: expected(contextServices...)},
	{Name: "nomnom_outbox_publish_retries_total", Section: "outbox", Type: Counter, Source: "Forwarder / relay", Labels: []string{"messaging_destination_name", "nomnom_event_type", "nomnom_error_class"}, Status: expected(contextServices...)},
	{Name: "nomnom_outbox_publish_duration_seconds", Section: "outbox", Type: Histogram, Source: "Forwarder / relay", Labels: []string{"messaging_destination_name", "nomnom_event_type", "nomnom_outcome"}, Status: expected(contextServices...), Note: "Publish одной записи до ack брокера; `nomnom_outcome`: success | error."},
	{Name: "nomnom_outbox_publish_lag_seconds", Section: "outbox", Type: Histogram, Source: "Forwarder / relay", Labels: []string{"messaging_destination_name", "nomnom_event_type"}, Status: expected(contextServices...), Note: "occurred_at → ack брокера: задержка доставки события соседям."},

	// usecase
	{Name: "nomnom_usecase_duration_seconds", Section: "usecase", Type: Histogram, Source: "Use case decorator (`pkg/telemetry`)", Labels: []string{"nomnom_usecase_name", "nomnom_usecase_kind", "nomnom_outcome", "error_type"}, Status: expected(Orders, OrdersWorker, OrdersConsumer), Note: "`kind`: command | query. Rate по `_count` — частота вызовов и доля `rejected`/`error`."},

	// uow
	{Name: "nomnom_uow_transactions_total", Section: "uow", Type: Counter, Source: "Unit of Work decorator", Labels: []string{"nomnom_outcome"}, Status: expected(contextServices...), Note: "`outcome`: committed | rolled_back | commit_failed."},
	{Name: "nomnom_uow_duration_seconds", Section: "uow", Type: Histogram, Source: "Unit of Work decorator", Labels: []string{"nomnom_outcome"}, Status: expected(contextServices...)},
	{Name: "nomnom_uow_retries_total", Section: "uow", Type: Counter, Source: "Unit of Work decorator", Labels: []string{"nomnom_reason"}, Status: expected(contextServices...), Note: "`reason`: serialization | deadlock | write_conflict | other."},

	// fsm
	{Name: "nomnom_fsm_transitions_total", Section: "fsm", Type: Counter, Source: "FSM transition decorator", Labels: []string{"nomnom_fsm_aggregate_type", "nomnom_fsm_from_state", "nomnom_fsm_to_state", "nomnom_fsm_trigger", "nomnom_fsm_result", "nomnom_fsm_terminal"}, Status: expected(contextServices...), Note: "`result`: applied | rejected (invalid transition); `terminal`: true | false."},

	// temporal
	{Name: "temporal_request_total", Section: "temporal", Type: Counter, Source: "Temporal SDK client", Labels: []string{"operation", "namespace"}, Status: verified([]string{OrdersWorker, OrdersConsumer}), Note: "`operation=\"StartWorkflowExecution\"` — запуск workflow (клиент `orders-consumer`)."},
	{Name: "temporal_workflow_completed_total", Section: "temporal", Type: Counter, Source: "Temporal SDK worker", Labels: []string{"workflow_type", "namespace", "task_queue"}, Status: verified([]string{OrdersWorker})},
	{Name: "temporal_workflow_failed_total", Section: "temporal", Type: Counter, Source: "Temporal SDK worker", Labels: []string{"workflow_type", "namespace", "task_queue"}, Status: verified([]string{OrdersWorker})},
	{Name: "temporal_workflow_endtoend_latency_seconds", Section: "temporal", Type: Histogram, Source: "Temporal SDK worker", Labels: []string{"workflow_type", "namespace", "task_queue"}, Status: verified([]string{OrdersWorker})},
	{Name: "temporal_activity_execution_latency_seconds", Section: "temporal", Type: Histogram, Source: "Temporal SDK worker", Labels: []string{"activity_type", "namespace", "task_queue"}, Status: verified([]string{OrdersWorker})},
	{Name: "temporal_activity_execution_failed_total", Section: "temporal", Type: Counter, Source: "Temporal SDK worker", Labels: []string{"activity_type", "namespace", "task_queue"}, Status: verified([]string{OrdersWorker}), Note: "Каждая неуспешная попытка; retries = попытки сверх первой."},
	{Name: "temporal_worker_task_slots_used", Section: "temporal", Type: Gauge, Source: "Temporal SDK worker", Labels: []string{"worker_type", "task_queue"}, Status: verified([]string{OrdersWorker})},
	{Name: "temporal_worker_task_slots_available", Section: "temporal", Type: Gauge, Source: "Temporal SDK worker", Labels: []string{"worker_type", "task_queue"}, Status: verified([]string{OrdersWorker})},
	{Name: "nomnom_orders_fulfillment_oldest_running_age_seconds", Section: "temporal", Type: Gauge, Source: "Polling Temporal Visibility", Status: expected(OrdersWorker), Note: "Возраст старейшего выполняющегося Order Fulfillment Workflow; SDK его не даёт."},
	{Name: "nomnom_orders_compensations_total", Section: "temporal", Type: Counter, Source: "Compensation Activity decorator", Labels: []string{"nomnom_compensation_step", "nomnom_outcome"}, Status: expected(OrdersWorker), Note: "`outcome`: attempted | failed."},

	// kitchen
	{Name: "nomnom_kitchen_queue_size", Section: "kitchen", Type: Gauge, Source: "Kitchen Board projection", Status: expected(Restaurants)},
	{Name: "nomnom_kitchen_queue_oldest_age_seconds", Section: "kitchen", Type: Gauge, Source: "Kitchen Board projection", Status: expected(Restaurants)},
	{Name: "nomnom_kitchen_preparations_active", Section: "kitchen", Type: Gauge, Source: "Kitchen Board projection", Status: expected(Restaurants)},
	{Name: "nomnom_kitchen_ready_for_pickup", Section: "kitchen", Type: Gauge, Source: "Kitchen Board projection", Status: expected(Restaurants)},
	{Name: "nomnom_kitchen_preparation_duration_seconds", Section: "kitchen", Type: Histogram, Source: "Kitchen use case decorator", Status: expected(Restaurants)},
	{Name: "nomnom_kitchen_delays_total", Section: "kitchen", Type: Counter, Source: "Kitchen use case decorator", Labels: []string{"nomnom_reason"}, Status: expected(Restaurants)},

	// authz
	{Name: "nomnom_authz_checks_total", Section: "authz", Type: Counter, Source: "Authorization port decorator", Labels: []string{"nomnom_authz_permission", "nomnom_authz_resource_type", "nomnom_authz_result"}, Status: expected(contextServices...), Note: "`result`: allowed | denied | error | timeout."},
	{Name: "nomnom_authz_check_duration_seconds", Section: "authz", Type: Histogram, Source: "Authorization port decorator", Labels: []string{"nomnom_authz_permission", "nomnom_authz_resource_type", "nomnom_authz_result"}, Status: expected(contextServices...)},

	// provider
	{Name: "nomnom_payments_provider_request_duration_seconds", Section: "provider", Type: Histogram, Source: "Provider gateway adapter", Labels: []string{"nomnom_payment_operation", "nomnom_outcome"}, Status: expected(Payments), Note: "`operation`: authorize | capture | refund | cancel; `outcome`: success | declined | error | timeout. `declined` — бизнес-результат."},

	// domain: orders
	{Name: "nomnom_orders_placed_total", Section: "domain", Type: Counter, Source: "Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork)", Status: expected(Orders)},
	{Name: "nomnom_orders_completed_total", Section: "domain", Type: Counter, Source: "Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork)", Status: expected(OrdersWorker, OrdersConsumer)},
	{Name: "nomnom_orders_cancelled_total", Section: "domain", Type: Counter, Source: "Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork)", Labels: []string{"nomnom_reason"}, Status: expected(Orders, OrdersWorker)},
	{Name: "nomnom_orders_failed_total", Section: "domain", Type: Counter, Source: "Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork)", Labels: []string{"nomnom_reason"}, Status: expected(OrdersWorker)},
	{Name: "nomnom_orders_cancellation_requests_total", Section: "domain", Type: Counter, Source: "use case decorator", Labels: []string{"nomnom_outcome"}, Status: expected(Orders)},
	{Name: "nomnom_orders_fulfillment_duration_seconds", Section: "domain", Type: Histogram, Source: "Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork)", Status: expected(OrdersWorker)},
	{Name: "nomnom_orders_in_state", Section: "domain", Type: Gauge, Source: "projection/polling", Labels: []string{"nomnom_order_state"}, Status: expected(Orders), Note: "Требует отдельной projection; PromQL по counters не эмулируется."},
	// domain: restaurants
	{Name: "nomnom_restaurants_orders_received_total", Section: "domain", Type: Counter, Source: "use case decorator", Status: expected(Restaurants)},
	{Name: "nomnom_restaurants_orders_accepted_total", Section: "domain", Type: Counter, Source: "use case decorator", Status: expected(Restaurants)},
	{Name: "nomnom_restaurants_orders_rejected_total", Section: "domain", Type: Counter, Source: "use case decorator", Labels: []string{"nomnom_reason"}, Status: expected(Restaurants)},
	// domain: delivery
	{Name: "nomnom_delivery_created_total", Section: "domain", Type: Counter, Source: "use case decorator", Status: expected(Delivery)},
	{Name: "nomnom_delivery_assignments_total", Section: "domain", Type: Counter, Source: "use case decorator", Status: expected(Delivery)},
	{Name: "nomnom_delivery_reassignments_total", Section: "domain", Type: Counter, Source: "use case decorator", Labels: []string{"nomnom_reason"}, Status: expected(Delivery)},
	{Name: "nomnom_delivery_offers_total", Section: "domain", Type: Counter, Source: "use case decorator", Labels: []string{"nomnom_outcome"}, Status: expected(Delivery), Note: "`outcome`: accepted | rejected | expired."},
	{Name: "nomnom_delivery_pickups_total", Section: "domain", Type: Counter, Source: "use case decorator", Status: expected(Delivery)},
	{Name: "nomnom_delivery_completed_total", Section: "domain", Type: Counter, Source: "use case decorator", Status: expected(Delivery)},
	{Name: "nomnom_delivery_failed_total", Section: "domain", Type: Counter, Source: "use case decorator", Labels: []string{"nomnom_reason"}, Status: expected(Delivery)},
	{Name: "nomnom_delivery_assignment_latency_seconds", Section: "domain", Type: Histogram, Source: "use case decorator", Status: expected(Delivery)},
	{Name: "nomnom_delivery_duration_seconds", Section: "domain", Type: Histogram, Source: "use case decorator", Status: expected(Delivery)},
	// domain: payments
	{Name: "nomnom_payments_authorizations_total", Section: "domain", Type: Counter, Source: "Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork)", Labels: []string{"nomnom_outcome"}, Status: expected(Payments), Note: "`outcome`: authorized | declined | failed; attempts = сумма."},
	{Name: "nomnom_payments_captures_total", Section: "domain", Type: Counter, Source: "Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork)", Labels: []string{"nomnom_outcome"}, Status: expected(Payments), Note: "`outcome`: captured | failed."},
	{Name: "nomnom_payments_refunds_total", Section: "domain", Type: Counter, Source: "Domain Events после commit Unit of Work (ObservedStore / ObservedUnitOfWork)", Labels: []string{"nomnom_outcome"}, Status: expected(Payments), Note: "`outcome`: refunded | failed."},

	// pods (Alloy: cAdvisor kubelet + kube-state-metrics, job infra/kubernetes)
	{Name: "container_cpu_usage_seconds_total", Section: "pods", Type: Counter, Source: "cAdvisor kubelet", Labels: []string{"namespace", "pod", "container"}, Status: podStatus(), Note: "Только контейнеры: pod-level cgroup и pause отбрасываются в Alloy."},
	{Name: "container_memory_working_set_bytes", Section: "pods", Type: Gauge, Source: "cAdvisor kubelet", Labels: []string{"namespace", "pod", "container"}, Status: podStatus(), Note: "Working set — то, что учитывает OOM killer и eviction."},
	{Name: "container_network_receive_bytes_total", Section: "pods", Type: Counter, Source: "cAdvisor kubelet", Labels: []string{"namespace", "pod"}, Status: podStatus(), Note: "Уровень pod, без `lo`."},
	{Name: "container_network_transmit_bytes_total", Section: "pods", Type: Counter, Source: "cAdvisor kubelet", Labels: []string{"namespace", "pod"}, Status: podStatus(), Note: "Уровень pod, без `lo`."},
	{Name: "kube_pod_status_ready", Section: "pods", Type: Gauge, Source: "kube-state-metrics v2.20", Labels: []string{"namespace", "pod", "condition"}, Status: podStatus(), Note: "В Alloy остаётся только `condition=\"true\"`."},
	{Name: "kube_pod_container_status_restarts_total", Section: "pods", Type: Counter, Source: "kube-state-metrics v2.20", Labels: []string{"namespace", "pod", "container"}, Status: podStatus()},
	{Name: "kube_pod_container_status_waiting_reason", Section: "pods", Type: Gauge, Source: "kube-state-metrics v2.20", Labels: []string{"namespace", "pod", "container", "reason"}, Status: podStatus(), Note: "`CrashLoopBackOff`, `ImagePullBackOff`, `CreateContainerConfigError` и т. п."},

	// spicedb (Alloy scrape :9090, job infra/spicedb)
	{Name: "grpc_server_handled_total", Section: "spicedb", Type: Counter, Source: "SpiceDB go-grpc-prometheus", Labels: []string{"grpc_service", "grpc_method", "grpc_code", "grpc_type"}, Status: verified([]string{SpiceDB}), Note: "Health-check `grpc.health.v1.Health` отбрасывается в Alloy."},
	{Name: "grpc_server_handling_seconds", Section: "spicedb", Type: Histogram, Source: "SpiceDB go-grpc-prometheus", Labels: []string{"grpc_service", "grpc_method", "grpc_type"}, Status: verified([]string{SpiceDB})},
	{Name: "spicedb_v1_check_permissionship_total", Section: "spicedb", Type: Counter, Source: "SpiceDB", Labels: []string{"method", "permissionship"}, Status: verified([]string{SpiceDB}), Note: "`permissionship`: HAS_PERMISSION | NO_PERMISSION | CONDITIONAL_PERMISSION — решение, не ошибка."},
	{Name: "spicedb_dispatch_client_check_total", Section: "spicedb", Type: Counter, Source: "SpiceDB dispatcher", Status: verified([]string{SpiceDB})},
	{Name: "spicedb_dispatch_client_check_from_cache_total", Section: "spicedb", Type: Counter, Source: "SpiceDB dispatcher", Status: verified([]string{SpiceDB})},
	{Name: "spicedb_datastore_query_latency", Section: "spicedb", Type: Histogram, Source: "SpiceDB datastore", Labels: []string{"operation", "query_shape"}, Status: verified([]string{SpiceDB}), Note: "Доставляются только `_sum`/`_count` (средняя latency): buckets — ~100 series."},
	{Name: "process_cpu_seconds_total", Section: "spicedb", Type: Counter, Source: "Prometheus Go client", Status: verified([]string{SpiceDB})},
	{Name: "process_resident_memory_bytes", Section: "spicedb", Type: Gauge, Source: "Prometheus Go client", Status: verified([]string{SpiceDB})},

	// postgresql (Alloy prometheus.exporter.postgres, job infra/postgresql)
	{Name: "pg_up", Section: "postgresql", Type: Gauge, Source: "postgres_exporter", Labels: []string{"owner"}, Status: verified([]string{PostgreSQLServer})},
	{Name: "pg_stat_database_numbackends", Section: "postgresql", Type: Gauge, Source: "postgres_exporter `stat_database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer})},
	{Name: "pg_stat_database_xact_commit", Section: "postgresql", Type: Counter, Source: "postgres_exporter `stat_database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer}), Note: "Counter без `_total` (имя postgres_exporter)."},
	{Name: "pg_stat_database_xact_rollback", Section: "postgresql", Type: Counter, Source: "postgres_exporter `stat_database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer})},
	{Name: "pg_stat_database_blks_hit", Section: "postgresql", Type: Counter, Source: "postgres_exporter `stat_database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer})},
	{Name: "pg_stat_database_blks_read", Section: "postgresql", Type: Counter, Source: "postgres_exporter `stat_database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer})},
	{Name: "pg_stat_database_deadlocks", Section: "postgresql", Type: Counter, Source: "postgres_exporter `stat_database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer})},
	{Name: "pg_stat_database_conflicts", Section: "postgresql", Type: Counter, Source: "postgres_exporter `stat_database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer})},
	{Name: "pg_stat_database_temp_bytes", Section: "postgresql", Type: Counter, Source: "postgres_exporter `stat_database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer}), Note: "Запросы, вылезшие за `work_mem`."},
	{Name: "pg_database_size_bytes", Section: "postgresql", Type: Gauge, Source: "postgres_exporter `database`", Labels: []string{"owner", "datname"}, Status: verified([]string{PostgreSQLServer})},
}

// podStatus — pods ролей, развёрнутых в kind и подтверждённых в Grafana Cloud;
// orders-worker и BFF в тестовом окружении пока не запускаются.
func podStatus() map[string]Status {
	return verified([]string{Orders, OrdersConsumer, Restaurants, Delivery, Payments}, OrdersWorker, BFF)
}

// ForbiddenLabels — индивидуальные идентификаторы, которые не становятся labels.
var ForbiddenLabels = []string{"order_id", "user_id", "payment_id", "event_id", "workflow_id", "run_id", "aggregate_id", "trace_id", "request_id", "message_id", "courier_id", "restaurant_id", "delivery_id", "customer_id", "amount"}

// ResourceLabels допустимы в matchers любой метрики.
var ResourceLabels = []string{"job", "instance", "le"}

func Lookup(name string) (Metric, bool) {
	base := BaseName(name)
	for _, m := range Metrics {
		if m.Name == base {
			return m, true
		}
	}
	return Metric{}, false
}

// BaseName убирает суффиксы histogram-серий.
func BaseName(series string) string {
	for _, suffix := range []string{"_bucket", "_count", "_sum"} {
		if base, ok := strings.CutSuffix(series, suffix); ok {
			if m, found := lookupExact(base); found && m.Type == Histogram {
				return base
			}
		}
	}
	return series
}

func lookupExact(name string) (Metric, bool) {
	i := slices.IndexFunc(Metrics, func(m Metric) bool { return m.Name == name })
	if i < 0 {
		return Metric{}, false
	}
	return Metrics[i], true
}

// Ready сообщает, есть ли метрика хотя бы у одного из сервисов.
func Ready(name string, services []string) bool {
	m, ok := Lookup(name)
	if !ok {
		return false
	}
	for _, s := range services {
		if m.Status[s] == Present {
			return true
		}
	}
	return false
}

func Anchor(section string) string { return RepoURL + DocPath + "#" + section }
