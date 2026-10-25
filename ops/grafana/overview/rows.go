package overview

import (
	"fmt"

	"nomnom/ops/grafana/inventory"
)

// gRPC status codes по классам ошибки (rpc_response_status_code — имя кода):
// transport/availability — сеть, deadline, перегрузка, недоступность зависимости;
// application — дефект или неожиданный сбой; business rejection — отказ по правилам
// домена/контракта. Бизнес-результаты внутри OK-ответа (PaymentDeclined) ошибками не считаются.
const (
	transport   = "CANCELLED|DEADLINE_EXCEEDED|RESOURCE_EXHAUSTED|UNAVAILABLE"
	application = "UNKNOWN|UNIMPLEMENTED|INTERNAL|DATA_LOSS"
	business    = "INVALID_ARGUMENT|NOT_FOUND|ALREADY_EXISTS|PERMISSION_DENIED|FAILED_PRECONDITION|ABORTED|OUT_OF_RANGE|UNAUTHENTICATED"

	codesTransport   = `rpc_response_status_code=~"` + transport + `"`
	codesApplication = `rpc_response_status_code=~"` + application + `"`
	codesBusiness    = `rpc_response_status_code=~"` + business + `"`
	codesTechnical   = `rpc_response_status_code=~"` + transport + "|" + application + `"`
)

// TraceQL-условия spans: те же классы кодов в span attribute rpc.response.status_code.
const (
	grpcServerSpan    = `kind=server && span.rpc.method != nil`
	spansTransport    = `kind=server && span.rpc.response.status_code=~"` + transport + `"`
	spansApplication  = `kind=server && span.rpc.response.status_code=~"` + application + `"`
	spansBusiness     = `kind=server && span.rpc.response.status_code=~"` + business + `"`
	serverErrorSpan   = `kind=server && status=error`
	consumerErrorSpan = `kind=consumer && status=error`
)

const (
	rpcServer = "rpc_server_call_duration_seconds"
	rpcClient = "rpc_client_call_duration_seconds"
	router    = "router_http_request_duration_milliseconds"
	// Входящий GraphQL-запрос клиента; с wg_subgraph_name — вызов subgraph.
	clientRequest = `wg_subgraph_name=""`
	subgraphCall  = `wg_subgraph_name!=""`
)

func (g *generator) health(sc Scope) []Panel {
	o := g.opts
	var rate, errs, ratio []Query
	var latency []Query
	var errTraces, latencyTraces []string
	if o.GRPC {
		rate = append(rate, Query{Sum("", sc.Rate(rpcServer+"_count")), "gRPC"})
		errs = append(errs,
			Query{Sum("", sc.Rate(rpcServer+"_count", codesTransport)), "gRPC transport/availability"},
			Query{Sum("", sc.Rate(rpcServer+"_count", codesApplication)), "gRPC application"},
			Query{Sum("", sc.Rate(rpcServer+"_count", codesBusiness)), "gRPC business rejection"},
		)
		ratio = append(ratio, Query{Sum("", sc.Rate(rpcServer+"_count", codesTechnical)) + " / " + Sum("", sc.Rate(rpcServer+"_count")), "gRPC"})
		errTraces = append(errTraces, spansTransport, spansApplication, spansBusiness)
		grpc := sc.Percentiles("gRPC ", "", rpcServer)
		latency = append(latency, grpc...)
		latencyTraces = append(latencyTraces, traces(grpcServerSpan+" && "+slower("s"), len(grpc))...)
	}
	if o.GraphQL {
		rate = append(rate, Query{Sum("", sc.Rate("router_http_requests_total", clientRequest)), "GraphQL/HTTP"})
		errs = append(errs,
			Query{Sum("", sc.Rate("router_http_requests_total", clientRequest, `http_status_code=~"5.."`)), "HTTP 5xx"},
			Query{Sum("", sc.Rate("router_http_requests_error_total", clientRequest)), "GraphQL errors"},
		)
		ratio = append(ratio, Query{Sum("", sc.Rate("router_http_requests_error_total", clientRequest)) + " / " + Sum("", sc.Rate("router_http_requests_total", clientRequest)), "GraphQL"})
		errTraces = append(errTraces, serverErrorSpan, serverErrorSpan)
		graphql := sc.Percentiles("GraphQL ", "", router, clientRequest)
		latency = append(latency, graphql...)
		latencyTraces = append(latencyTraces, traces("kind=server && "+slower("ms"), len(graphql))...)
	}
	if o.Kafka != nil {
		if o.Kafka.Watermill {
			rate = append(rate, Query{Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count")), "Kafka processing"})
			errs = append(errs, Query{Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count", `success="false"`)), "Kafka nack (retries exhausted)"})
			errTraces = append(errTraces, consumerErrorSpan)
			ratio = append(ratio, Query{Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count", `success="false"`)) + " / " + Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count")), "Kafka"})
		} else {
			rate = append(rate, Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count")), "Kafka processing"})
			errs = append(errs, Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `nomnom_error_class!~"|business"`)), "Kafka technical failure"})
			errTraces = append(errTraces, consumerErrorSpan)
			ratio = append(ratio, Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `nomnom_error_class!=""`)) + " / " + Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count")), "Kafka"})
		}
	}
	if o.Kafka != nil {
		errs = append(errs, Query{Sum("", sc.Rate("nomnom_messaging_poison_messages_total")), "Kafka dead letter"},
			Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `nomnom_error_class="business"`)), "Kafka business rejection"})
		// Dead letter и business rejection не помечают span ошибкой: ссылки нет.
		errTraces = append(errTraces, "", "")
	}
	if o.Temporal != nil {
		rate = append(rate, Query{Sum("", sc.Rate("temporal_activity_execution_latency_seconds_count")), "Temporal activities"})
		errs = append(errs, Query{Sum("", sc.Rate("temporal_activity_execution_failed_total")), "Temporal activity failure"})
		errTraces = append(errTraces, "status=error")
	}

	panels := []Panel{
		{Title: "Request rate by entry", Unit: "ops", Queries: rate,
			Description: "Входящая работа по типу входа: gRPC, GraphQL/HTTP, обработка Kafka, Temporal Activities."},
		{Title: "Errors by class", Unit: "ops", Queries: errs, Traces: errTraces,
			Description: "Transport/availability (CANCELLED, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED, UNAVAILABLE), application (UNKNOWN, UNIMPLEMENTED, INTERNAL, DATA_LOSS) и business rejection (INVALID_ARGUMENT, NOT_FOUND, ALREADY_EXISTS, PERMISSION_DENIED, FAILED_PRECONDITION, ABORTED, OUT_OF_RANGE, UNAUTHENTICATED). Бизнес-результаты в OK-ответе (PaymentDeclined) ошибками не являются."},
		{Title: "Technical error ratio", Unit: "percentunit", Queries: ratio,
			Description: "Доля transport + application ошибок; business rejections исключены."},
		{Title: "Latency p50 / p95 / p99", Unit: latencyUnit(o), Queries: latency, Traces: latencyTraces,
			Description: "Server-side latency из histograms входа."},
	}
	if g.opts.Language == Cosmo {
		return panels
	}
	cpu, memory := g.saturation(sc)
	return append(panels, cpu, memory)
}

// latencyUnit — gRPC histograms в секундах, GraphQL Cosmo Router — в миллисекундах.
func latencyUnit(o Options) string {
	if o.GRPC {
		return "s"
	}
	return "ms"
}

func (g *generator) saturation(sc Scope) (Panel, Panel) {
	cpu := Panel{Title: "CPU", Unit: "short", Description: "Используемые CPU-ядра процесса (rate process.cpu.time).",
		Queries: []Query{{Sum("job", sc.Rate("process_cpu_time_seconds_total")), "{{job}}"}}}
	memory := Panel{Title: "Memory", Unit: "bytes", Description: "Память процесса.",
		Queries: []Query{{Sum("job", sc.Gauge("process_memory_usage_bytes")), "{{job}}"}}}
	if g.opts.Language == Go {
		memory.Description = "Память Go runtime (go.memory.used): process.memory.usage для Go не экспортируется."
		memory.Queries = []Query{{Sum("job", sc.Gauge("go_memory_used_bytes")), "{{job}}"}}
	}
	return cpu, memory
}

func (g *generator) runtime(sc Scope) []Panel {
	// Runtime-метрики Cosmo Router не инвентаризированы: row не строится.
	switch g.opts.Language {
	case Go:
		return []Panel{
			{Title: "Heap and runtime memory", Unit: "bytes", Queries: []Query{{Sum("job, go_memory_type", sc.Gauge("go_memory_used_bytes")), "{{job}} {{go_memory_type}}"}, {Max("job", sc.Gauge("go_memory_gc_goal_bytes")), "{{job}} GC goal"}, {Max("job", sc.Gauge("go_memory_limit_bytes")), "{{job}} GOMEMLIMIT"}}},
			{Title: "Allocation rate", Unit: "Bps", Queries: []Query{{Sum("job", sc.Rate("go_memory_allocated_bytes_total")), "{{job}}"}}},
			{Title: "Goroutines", Unit: "short", Queries: []Query{{Sum("job", sc.Gauge("go_goroutine_count")), "{{job}}"}}},
			{Title: "GOMAXPROCS", Unit: "short", Queries: []Query{{Max("job", sc.Gauge("go_processor_limit")), "{{job}}"}}},
		}
	case Node:
		return []Panel{
			{Title: "V8 heap used / limit", Unit: "bytes", Queries: []Query{{Sum("", sc.Gauge("v8js_memory_heap_used_bytes")), "used"}, {Sum("", sc.Gauge("v8js_memory_heap_limit_bytes")), "limit"}}},
			{Title: "Event loop delay p99", Unit: "s", Queries: []Query{{Max("instance", sc.Gauge("nodejs_eventloop_delay_p99_seconds")), "{{instance}}"}}},
			{Title: "GC duration p95", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "v8js_gc_type", "v8js_gc_duration_seconds"), "{{v8js_gc_type}}"}}},
		}
	case Python:
		return []Panel{
			{Title: "GC collections", Unit: "ops", Queries: []Query{{Sum("cpython_gc_generation", sc.Rate("cpython_gc_collections_total")), "gen {{cpython_gc_generation}}"}}},
			{Title: "Threads (gRPC worker pool)", Unit: "short", Queries: []Query{{Sum("instance", sc.Gauge("process_thread_count")), "{{instance}}"}}},
			{Title: "CPU by mode", Unit: "short", Queries: []Query{{Sum("cpu_mode", sc.Rate("process_cpu_time_seconds_total")), "{{cpu_mode}}"}}},
		}
	case Rust:
		return []Panel{
			{Title: "Threads", Unit: "short", Queries: []Query{{Sum("instance", sc.Gauge("process_thread_count")), "{{instance}}"}}},
			{Title: "CPU by mode", Unit: "short", Queries: []Query{{Sum("cpu_mode", sc.Rate("process_cpu_time_seconds_total")), "{{cpu_mode}}"}}},
		}
	}
	return nil
}

func (g *generator) dependencies(sc Scope) []Panel {
	o := g.opts
	var panels []Panel
	if o.GRPCClients {
		gateway := `rpc_method!~"authzed.*"`
		panels = append(panels,
			Panel{Title: "gRPC via internal gateway: rate", Unit: "ops", Queries: []Query{{Sum("rpc_method", sc.Rate(rpcClient+"_count", gateway)), "{{rpc_method}}"}}},
			Panel{Title: "gRPC via internal gateway: technical errors", Unit: "ops", Traces: []string{"kind=client && " + spanLabel("rpc_method") + " && " + spanLabel("rpc_response_status_code")}, Queries: []Query{{Sum("rpc_method, rpc_response_status_code", sc.Rate(rpcClient+"_count", gateway, codesTechnical)), "{{rpc_method}} {{rpc_response_status_code}}"}}},
			Panel{Title: "gRPC via internal gateway: p95", Unit: "s", Traces: []string{"kind=client && " + spanLabel("rpc_method") + " && " + slower("s")}, Queries: []Query{{sc.Quantile("0.95", "rpc_method", rpcClient, gateway), "{{rpc_method}}"}}},
		)
	}
	if o.GraphQL {
		panels = append(panels,
			Panel{Title: "Subgraph calls: rate", Unit: "ops", Queries: []Query{{Sum("wg_subgraph_name", sc.Rate("router_http_requests_total", subgraphCall)), "{{wg_subgraph_name}}"}}},
			Panel{Title: "Subgraph calls: errors", Unit: "ops", Traces: []string{"kind=client && status=error"}, Queries: []Query{{Sum("wg_subgraph_name", sc.Rate("router_http_requests_error_total", subgraphCall)), "{{wg_subgraph_name}}"}}},
			Panel{Title: "Subgraph calls: p95", Unit: "ms", Queries: []Query{{sc.Quantile("0.95", "wg_subgraph_name", router, subgraphCall), "{{wg_subgraph_name}}"}}},
		)
	}
	if o.Database != "" {
		db := `db_system_name="` + string(o.Database) + `"`
		dbSpan := `kind=client && span.db.system.name="` + string(o.Database) + `"`
		panels = append(panels,
			Panel{Title: string(o.Database) + ": operations", Unit: "ops", Traces: []string{"", dbSpan + " && " + spanLabel("error_type")}, Queries: []Query{
				{Sum("db_operation_name", sc.Rate("db_client_operation_duration_seconds_count", db)), "{{db_operation_name}}"},
				{Sum("error_type", sc.Rate("db_client_operation_duration_seconds_count", db, `error_type!=""`)), "error {{error_type}}"},
			}},
			Panel{Title: string(o.Database) + ": p95 by operation", Unit: "s", Traces: []string{dbSpan + " && " + spanLabel("db_operation_name") + " && " + slower("s")}, Queries: []Query{{sc.Quantile("0.95", "db_operation_name", "db_client_operation_duration_seconds", db), "{{db_operation_name}}"}}},
		)
		if o.DatabasePool {
			panels = append(panels, poolPanels(sc, o.Database)...)
		} else {
			panels = append(panels,
				Panel{Title: string(o.Database) + ": pool", Unit: "short", Queries: []Query{
					{Sum("db_client_connection_state", sc.Gauge("db_client_connection_count")), "{{db_client_connection_state}}"},
					{Sum("", sc.Gauge("db_client_connection_max")), "max"},
				}},
				Panel{Title: string(o.Database) + ": pool wait p95", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "", "db_client_connection_wait_time_seconds"), "wait p95"}}},
			)
		}
	}
	if o.PaymentProvider {
		provider := "nomnom_payments_provider_request_duration_seconds"
		panels = append(panels,
			Panel{Title: "Payment provider: requests", Unit: "ops", Queries: []Query{{Sum("nomnom_payment_operation, nomnom_outcome", sc.Rate(provider+"_count")), "{{nomnom_payment_operation}} {{nomnom_outcome}}"}},
				Description: "`declined` — бизнес-результат провайдера, не технический сбой."},
			Panel{Title: "Payment provider: errors and timeouts", Unit: "ops", Traces: []string{spanLabel("nomnom_payment_operation") + " && " + spanLabel("nomnom_outcome")}, Queries: []Query{{Sum("nomnom_payment_operation, nomnom_outcome", sc.Rate(provider+"_count", `nomnom_outcome=~"error|timeout"`)), "{{nomnom_payment_operation}} {{nomnom_outcome}}"}}},
			Panel{Title: "Payment provider: p95", Unit: "s", Traces: []string{spanLabel("nomnom_payment_operation") + " && " + slower("s")}, Queries: []Query{{sc.Quantile("0.95", "nomnom_payment_operation", provider), "{{nomnom_payment_operation}}"}}},
		)
	}
	return panels
}

func (g *generator) kafka(sc Scope) []Panel {
	k := g.opts.Kafka
	lag := Sum("topic", `kafka_consumergroup_lag{consumergroup="`+k.ConsumerGroup+`"}`)
	// Outbox публикует Debezium (ADR 37): метрики коннектора контекста из Kafka Connect.
	cdc := Scope{Services: []string{inventory.KafkaConnect}, Job: jobKafkaConnect}
	connector := `name="` + k.Connector + `"`
	produced := []Query{{Sum("", cdc.Rate("debezium_metrics_TotalNumberOfEventsSeen", connector)), "events"}}
	cdcLag := []Query{{Max("", cdc.Gauge("debezium_metrics_MilliSecondsBehindSource", connector)) + " / 1000", "behind source"}}
	cdcTasks := []Query{{Sum("status", cdc.Gauge("kafka_connect_connector_task_status", `connector="`+k.Connector+`"`)), "{{status}}"}}
	var consumed, failures []Query
	var processed Query
	if k.Watermill {
		consumed = []Query{{Sum("handler_name, acked", sc.Rate("watermill_consumer_subscriber_messages_received_total")), "{{handler_name}} acked={{acked}}"}}
		failures = []Query{{Sum("handler_name", sc.Rate("watermill_consumer_handler_execution_time_seconds_count", `success="false"`)), "{{handler_name}}"}}
		processed = Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `nomnom_error_class=~"|business"`)), "processed"}
	} else {
		consumed = []Query{{Sum("messaging_destination_name", sc.Rate("messaging_client_consumed_messages_total")), "{{messaging_destination_name}}"}}
		processed = Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `nomnom_error_class=~"|business"`)), "processed"}
	}
	failures = append(failures, Query{Sum("nomnom_error_class", sc.Rate("nomnom_messaging_process_duration_seconds_count", `nomnom_error_class!~"|business"`)), "{{nomnom_error_class}}"})
	return []Panel{
		{Title: "Consumed", Unit: "ops", Queries: consumed},
		{Title: "Produced (Outbox, Debezium)", Unit: "ops", Queries: produced,
			Description: "События Outbox, которые коннектор `" + k.Connector + "` прочитал из журнала базы и отправил в Kafka (ADR 37)."},
		{Title: "Processing failures", Unit: "ops", Queries: failures, Traces: traces(consumerErrorSpan, len(failures)),
			Description: "Технические сбои обработки по `error_class` (permanent, deserialization, transient); business rejections подтверждаются (ack) и показаны в Service health отдельно."},
		{Title: "Consumer lag", Unit: "short", Queries: []Query{{lag, "{{topic}}"}},
			Description: "Lag consumer group `" + k.ConsumerGroup + "` из Alloy Kafka exporter; не путать с очередями предметной области."},
		{Title: "Retries and poison (DLQ)", Unit: "ops", Queries: []Query{
			{Sum("nomnom_event_type", sc.Rate("nomnom_messaging_retry_attempts_total")), "retry {{nomnom_event_type}}"},
			{Sum("messaging_destination_name, nomnom_error_class", sc.Rate("nomnom_messaging_poison_messages_total")), "{{messaging_destination_name}}.dlq {{nomnom_error_class}}"},
		}},
		{Title: "Processing duration p95 by event_type", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "nomnom_event_type", "nomnom_messaging_process_duration_seconds"), "{{nomnom_event_type}}"}}},
		{Title: "Message age at processing p95", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "nomnom_event_type", "nomnom_messaging_message_age_seconds"), "{{nomnom_event_type}}"}},
			Description: "`now − occurred_at` в момент обработки."},
		{Title: "Inbox: processed / duplicates", Unit: "ops", Queries: []Query{processed, {Sum("", sc.Rate("nomnom_inbox_duplicates_total")), "duplicates"}},
			Description: "Duplicate = конфликт уникальности `(consumer, event_id)` в Inbox; failures — панель Processing failures."},
		{Title: "Outbox CDC lag", Unit: "s", Queries: cdcLag,
			Description: "Отставание коннектора `" + k.Connector + "` от журнала базы: время от commit записи Outbox до её чтения."},
		{Title: "Outbox connector tasks", Unit: "short", Queries: cdcTasks,
			Description: "Состояние задачи коннектора `" + k.Connector + "`: `failed` — Outbox не доставляется."},
	}
}

// useCases — row декоратора application handlers: rejected — бизнес-отказ по
// правилам use case, error — технический сбой; это разные сигналы.
func useCases(sc Scope) []Panel {
	uc := "nomnom_usecase_duration_seconds"
	return []Panel{
		{Title: "Calls by outcome", Unit: "ops", Queries: []Query{{Sum("nomnom_outcome", sc.Rate(uc+"_count")), "{{nomnom_outcome}}"}},
			Description: "success, rejected (отказ по правилам use case) и error (технический сбой)."},
		{Title: "Rejections by use case", Unit: "ops", Traces: []string{spanLabel("nomnom_usecase_name") + ` && span.nomnom.outcome="rejected" && ` + spanLabel("error_type")}, Queries: []Query{{Sum("nomnom_usecase_name, error_type", sc.Rate(uc+"_count", `nomnom_outcome="rejected"`)), "{{nomnom_usecase_name}} {{error_type}}"}}},
		{Title: "Errors by use case", Unit: "ops", Traces: []string{spanLabel("nomnom_usecase_name") + ` && span.nomnom.outcome="error" && ` + spanLabel("error_type")}, Queries: []Query{{Sum("nomnom_usecase_name, error_type", sc.Rate(uc+"_count", `nomnom_outcome="error"`)), "{{nomnom_usecase_name}} {{error_type}}"}}},
		{Title: "Duration p95 by use case", Unit: "s", Traces: []string{spanLabel("nomnom_usecase_name") + " && " + slower("s")}, Queries: []Query{{sc.Quantile("0.95", "nomnom_usecase_name", uc), "{{nomnom_usecase_name}}"}}},
	}
}

func transactions(sc Scope) []Panel {
	return []Panel{
		{Title: "Transactions by outcome", Unit: "ops", Queries: []Query{{Sum("nomnom_outcome", sc.Rate("nomnom_uow_transactions_total")), "{{nomnom_outcome}}"}},
			Description: "committed, rolled_back и commit_failed Unit of Work."},
		{Title: "Transaction duration", Unit: "s", Queries: sc.Percentiles("", "", "nomnom_uow_duration_seconds")},
		{Title: "Retries by conflict", Unit: "ops", Queries: []Query{{Sum("nomnom_reason", sc.Rate("nomnom_uow_retries_total")), "{{nomnom_reason}}"}},
			Description: "serialization, deadlock, write_conflict — классы retry Unit of Work."},
	}
}

func lifecycle(sc Scope) []Panel {
	fsm := "nomnom_fsm_transitions_total"
	return []Panel{
		{Title: "Transitions", Unit: "ops", Queries: []Query{{Sum("nomnom_fsm_aggregate_type, nomnom_fsm_to_state", sc.Rate(fsm, `nomnom_fsm_result="applied"`)), "{{nomnom_fsm_aggregate_type}} → {{nomnom_fsm_to_state}}"}}},
		{Title: "Invalid transitions", Unit: "ops", Queries: []Query{{Sum("nomnom_fsm_aggregate_type, nomnom_fsm_from_state, nomnom_fsm_trigger", sc.Rate(fsm, `nomnom_fsm_result="rejected"`)), "{{nomnom_fsm_aggregate_type}} {{nomnom_fsm_from_state}} × {{nomnom_fsm_trigger}}"}}},
		{Title: "Terminal transitions", Unit: "ops", Queries: []Query{{Sum("nomnom_fsm_aggregate_type, nomnom_fsm_to_state", sc.Rate(fsm, `nomnom_fsm_result="applied"`, `nomnom_fsm_terminal="true"`)), "{{nomnom_fsm_aggregate_type}} {{nomnom_fsm_to_state}}"}}},
	}
}

// temporal — row с section variable worker; запуск workflow измеряется клиентом API.
func temporal(worker, main Scope) []Panel {
	return []Panel{
		{Title: "Workflows started", Unit: "ops", Queries: []Query{{Sum("", main.Rate("temporal_request_total", `operation="StartWorkflowExecution"`)), "started"}},
			Description: "Клиентский StartWorkflowExecution (`$service`)."},
		{Title: "Workflows completed / failed", Unit: "ops", Queries: []Query{
			{Sum("workflow_type", worker.Rate("temporal_workflow_completed_total")), "completed {{workflow_type}}"},
			{Sum("workflow_type", worker.Rate("temporal_workflow_failed_total")), "failed {{workflow_type}}"},
		}},
		{Title: "Workflow end-to-end latency p95", Unit: "s", Queries: []Query{{worker.Quantile("0.95", "workflow_type", "temporal_workflow_endtoend_latency_seconds"), "{{workflow_type}}"}}},
		{Title: "Oldest running fulfillment", Unit: "s", Queries: []Query{{Max("", worker.Gauge("nomnom_orders_fulfillment_oldest_running_age_seconds")), "age"}},
			Description: "Возраст старейшего выполняющегося workflow: polling Visibility, SDK метрики не даёт."},
		{Title: "Activities executed / failed attempts", Unit: "ops", Traces: []string{"", "status=error"}, Queries: []Query{
			{Sum("activity_type", worker.Rate("temporal_activity_execution_latency_seconds_count")), "executed {{activity_type}}"},
			{Sum("activity_type", worker.Rate("temporal_activity_execution_failed_total")), "failed {{activity_type}}"},
		}, Description: "Каждая неуспешная попытка, кроме последней, ведёт к retry по Retry Policy."},
		{Title: "Activity latency p95", Unit: "s", Queries: []Query{{worker.Quantile("0.95", "activity_type", "temporal_activity_execution_latency_seconds"), "{{activity_type}}"}}},
		{Title: "Worker task slots", Unit: "short", Queries: []Query{
			{Sum("worker_type", worker.Gauge("temporal_worker_task_slots_used")), "used {{worker_type}}"},
			{Sum("worker_type", worker.Gauge("temporal_worker_task_slots_available")), "available {{worker_type}}"},
		}},
		{Title: "Compensations", Unit: "ops", Queries: []Query{{Sum("nomnom_compensation_step, nomnom_outcome", worker.Rate("nomnom_orders_compensations_total")), "{{nomnom_compensation_step}} {{nomnom_outcome}}"}}},
	}
}

func kitchen(sc Scope) []Panel {
	return []Panel{
		{Title: "Kitchen queue size", Unit: "short", Queries: []Query{{Sum("", sc.Gauge("nomnom_kitchen_queue_size")), "queued"}},
			Description: "Kitchen Queue Size из Kitchen Board projection; это не Kafka consumer lag."},
		{Title: "Oldest queued order age", Unit: "s", Queries: []Query{{Max("", sc.Gauge("nomnom_kitchen_queue_oldest_age_seconds")), "oldest"}}},
		{Title: "Active preparations / ready for pickup", Unit: "short", Queries: []Query{
			{Sum("", sc.Gauge("nomnom_kitchen_preparations_active")), "preparing"},
			{Sum("", sc.Gauge("nomnom_kitchen_ready_for_pickup")), "ready for pickup"},
		}},
		{Title: "Preparation duration", Unit: "s", Queries: sc.Percentiles("", "", "nomnom_kitchen_preparation_duration_seconds")},
		{Title: "Delays", Unit: "ops", Queries: []Query{{Sum("nomnom_reason", sc.Rate("nomnom_kitchen_delays_total")), "{{nomnom_reason}}"}}},
	}
}

// servicePods — pods ролей сервиса: неймспейс контекста и имя Deployment как префикс
// pod (<deployment>-<replicaset hash>-<suffix>), чтобы orders не захватывал orders-worker.
func servicePods(o Options) Scope {
	return Scope{Var: "service", Services: o.Services, Job: jobKubernetes, Matchers: []string{
		fmt.Sprintf(`namespace="nomnom-%s"`, o.Context),
		`pod=~"(${service:pipe})-[a-z0-9]+-[a-z0-9]+"`,
	}}
}

const jobKubernetes = "infra/kubernetes"

func pods(sc Scope) []Panel {
	return []Panel{
		{Title: "Ready pods", Unit: "short", Queries: []Query{
			{Sum("", sc.Gauge("kube_pod_status_ready", `condition="true"`)), "ready"},
			{"count(" + sc.Gauge("kube_pod_status_ready", `condition="true"`) + ")", "pods"},
		}, Description: "Разрыв ready и pods — pod не проходит readinessProbe и выведен из Service."},
		{Title: "Restarts", Unit: "short", Queries: []Query{{Sum("pod", sc.Increase("kube_pod_container_status_restarts_total")), "{{pod}}"}}},
		{Title: "Waiting containers by reason", Unit: "short", Queries: []Query{{Sum("reason", sc.Gauge("kube_pod_container_status_waiting_reason")), "{{reason}}"}},
			Description: "CrashLoopBackOff, ImagePullBackOff и т. п.: контейнер не запущен."},
		{Title: "CPU by pod", Unit: "short", Queries: []Query{{Sum("pod", sc.Rate("container_cpu_usage_seconds_total")), "{{pod}}"}},
			Description: "Ядра CPU (cAdvisor), включая то, что runtime не видит (GC, sidecar-процессы контейнера)."},
		{Title: "Memory working set by pod", Unit: "bytes", Queries: []Query{{Sum("pod", sc.Gauge("container_memory_working_set_bytes")), "{{pod}}"}}},
		{Title: "Network by pod", Unit: "Bps", Queries: []Query{
			{Sum("pod", sc.Rate("container_network_receive_bytes_total")), "{{pod}} rx"},
			{Sum("pod", sc.Rate("container_network_transmit_bytes_total")), "{{pod}} tx"},
		}},
	}
}

func authorization(sc Scope) []Panel {
	checks := "nomnom_authz_checks_total"
	return []Panel{
		{Title: "Checks by result", Unit: "ops", Queries: []Query{{Sum("nomnom_authz_result", sc.Rate(checks)), "{{nomnom_authz_result}}"}}},
		{Title: "Denied by permission", Unit: "ops", Queries: []Query{{Sum("nomnom_authz_resource_type, nomnom_authz_permission", sc.Rate(checks, `nomnom_authz_result="denied"`)), "{{nomnom_authz_resource_type}}#{{nomnom_authz_permission}}"}},
			Description: "DENIED — решение политики, не сбой SpiceDB."},
		{Title: "Errors and timeouts", Unit: "ops", Traces: []string{`kind=client && span.rpc.method=~"authzed.*" && status=error`}, Queries: []Query{{Sum("nomnom_authz_result", sc.Rate(checks, `nomnom_authz_result=~"error|timeout"`)), "{{nomnom_authz_result}}"}},
			Description: "Технические сбои проверки; fail-closed превращает их в отказ клиенту."},
		{Title: "Check latency", Unit: "s", Queries: sc.Percentiles("", "", "nomnom_authz_check_duration_seconds")},
	}
}
