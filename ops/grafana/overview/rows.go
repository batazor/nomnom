package overview

import "fmt"

// gRPC status codes по классам ошибки (rpc_response_status_code — имя кода):
// transport/availability — сеть, deadline, перегрузка, недоступность зависимости;
// application — дефект или неожиданный сбой; business rejection — отказ по правилам
// домена/контракта. Бизнес-результаты внутри OK-ответа (PaymentDeclined) ошибками не считаются.
const (
	codesTransport   = `rpc_response_status_code=~"CANCELLED|DEADLINE_EXCEEDED|RESOURCE_EXHAUSTED|UNAVAILABLE"`
	codesApplication = `rpc_response_status_code=~"UNKNOWN|UNIMPLEMENTED|INTERNAL|DATA_LOSS"`
	codesBusiness    = `rpc_response_status_code=~"INVALID_ARGUMENT|NOT_FOUND|ALREADY_EXISTS|PERMISSION_DENIED|FAILED_PRECONDITION|ABORTED|OUT_OF_RANGE|UNAUTHENTICATED"`
	codesTechnical   = `rpc_response_status_code=~"CANCELLED|DEADLINE_EXCEEDED|RESOURCE_EXHAUSTED|UNAVAILABLE|UNKNOWN|UNIMPLEMENTED|INTERNAL|DATA_LOSS"`
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
	if o.GRPC {
		rate = append(rate, Query{Sum("", sc.Rate(rpcServer+"_count")), "gRPC"})
		errs = append(errs,
			Query{Sum("", sc.Rate(rpcServer+"_count", codesTransport)), "gRPC transport/availability"},
			Query{Sum("", sc.Rate(rpcServer+"_count", codesApplication)), "gRPC application"},
			Query{Sum("", sc.Rate(rpcServer+"_count", codesBusiness)), "gRPC business rejection"},
		)
		ratio = append(ratio, Query{Sum("", sc.Rate(rpcServer+"_count", codesTechnical)) + " / " + Sum("", sc.Rate(rpcServer+"_count")), "gRPC"})
		latency = append(latency, sc.Percentiles("gRPC ", "", rpcServer)...)
	}
	if o.GraphQL {
		rate = append(rate, Query{Sum("", sc.Rate("router_http_requests_total", clientRequest)), "GraphQL/HTTP"})
		errs = append(errs,
			Query{Sum("", sc.Rate("router_http_requests_total", clientRequest, `http_status_code=~"5.."`)), "HTTP 5xx"},
			Query{Sum("", sc.Rate("router_http_requests_error_total", clientRequest)), "GraphQL errors"},
		)
		ratio = append(ratio, Query{Sum("", sc.Rate("router_http_requests_error_total", clientRequest)) + " / " + Sum("", sc.Rate("router_http_requests_total", clientRequest)), "GraphQL"})
		latency = append(latency, sc.Percentiles("GraphQL ", "", router, clientRequest)...)
	}
	if o.Kafka != nil {
		if o.Kafka.Watermill {
			rate = append(rate, Query{Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count")), "Kafka processing"})
			errs = append(errs, Query{Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count", `success="false"`)), "Kafka nack (retries exhausted)"})
			ratio = append(ratio, Query{Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count", `success="false"`)) + " / " + Sum("", sc.Rate("watermill_consumer_handler_execution_time_seconds_count")), "Kafka"})
		} else {
			rate = append(rate, Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count")), "Kafka processing"})
			errs = append(errs, Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `error_class!~"|business"`)), "Kafka technical failure"})
			ratio = append(ratio, Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `error_class!=""`)) + " / " + Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count")), "Kafka"})
		}
	}
	if o.Kafka != nil {
		errs = append(errs, Query{Sum("", sc.Rate("nomnom_messaging_poison_messages_total")), "Kafka dead letter"},
			Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `error_class="business"`)), "Kafka business rejection"})
	}
	if o.Temporal != nil {
		rate = append(rate, Query{Sum("", sc.Rate("temporal_activity_execution_latency_seconds_count")), "Temporal activities"})
		errs = append(errs, Query{Sum("", sc.Rate("temporal_activity_execution_failed_total")), "Temporal activity failure"})
	}

	panels := []Panel{
		{Title: "Request rate by entry", Unit: "ops", Queries: rate,
			Description: "Входящая работа по типу входа: gRPC, GraphQL/HTTP, обработка Kafka, Temporal Activities."},
		{Title: "Errors by class", Unit: "ops", Queries: errs, Traces: true,
			Description: "Transport/availability (CANCELLED, DEADLINE_EXCEEDED, RESOURCE_EXHAUSTED, UNAVAILABLE), application (UNKNOWN, UNIMPLEMENTED, INTERNAL, DATA_LOSS) и business rejection (INVALID_ARGUMENT, NOT_FOUND, ALREADY_EXISTS, PERMISSION_DENIED, FAILED_PRECONDITION, ABORTED, OUT_OF_RANGE, UNAUTHENTICATED). Бизнес-результаты в OK-ответе (PaymentDeclined) ошибками не являются."},
		{Title: "Technical error ratio", Unit: "percentunit", Queries: ratio,
			Description: "Доля transport + application ошибок; business rejections исключены."},
		{Title: "Latency p50 / p95 / p99", Unit: latencyUnit(o), Queries: latency, Traces: true,
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
			{Title: "Heap and runtime memory", Unit: "bytes", Queries: []Query{{Sum("job, go_memory_type", sc.Gauge("go_memory_used_bytes")), "{{job}} {{go_memory_type}}"}, {Max("job", sc.Gauge("go_memory_gc_goal_bytes")), "{{job}} GC goal"}}},
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
			Panel{Title: "gRPC via internal gateway: technical errors", Unit: "ops", Traces: true, Queries: []Query{{Sum("rpc_method, rpc_response_status_code", sc.Rate(rpcClient+"_count", gateway, codesTechnical)), "{{rpc_method}} {{rpc_response_status_code}}"}}},
			Panel{Title: "gRPC via internal gateway: p95", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "rpc_method", rpcClient, gateway), "{{rpc_method}}"}}},
		)
	}
	if o.GraphQL {
		panels = append(panels,
			Panel{Title: "Subgraph calls: rate", Unit: "ops", Queries: []Query{{Sum("wg_subgraph_name", sc.Rate("router_http_requests_total", subgraphCall)), "{{wg_subgraph_name}}"}}},
			Panel{Title: "Subgraph calls: errors", Unit: "ops", Traces: true, Queries: []Query{{Sum("wg_subgraph_name", sc.Rate("router_http_requests_error_total", subgraphCall)), "{{wg_subgraph_name}}"}}},
			Panel{Title: "Subgraph calls: p95", Unit: "ms", Queries: []Query{{sc.Quantile("0.95", "wg_subgraph_name", router, subgraphCall), "{{wg_subgraph_name}}"}}},
		)
	}
	if o.Database != "" {
		db := `db_system_name="` + string(o.Database) + `"`
		panels = append(panels,
			Panel{Title: string(o.Database) + ": operations", Unit: "ops", Queries: []Query{
				{Sum("db_operation_name", sc.Rate("db_client_operation_duration_seconds_count", db)), "{{db_operation_name}}"},
				{Sum("error_type", sc.Rate("db_client_operation_duration_seconds_count", db, `error_type!=""`)), "error {{error_type}}"},
			}},
			Panel{Title: string(o.Database) + ": p95 by operation", Unit: "s", Traces: true, Queries: []Query{{sc.Quantile("0.95", "db_operation_name", "db_client_operation_duration_seconds", db), "{{db_operation_name}}"}}},
			Panel{Title: string(o.Database) + ": pool", Unit: "short", Queries: []Query{
				{Sum("db_client_connection_state", sc.Gauge("db_client_connection_count")), "{{db_client_connection_state}}"},
				{Sum("", sc.Gauge("db_client_connection_max")), "max"},
			}},
			Panel{Title: string(o.Database) + ": pool wait p95", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "", "db_client_connection_wait_time_seconds"), "wait p95"}}},
		)
	}
	if o.PaymentProvider {
		provider := "nomnom_payments_provider_request_duration_seconds"
		panels = append(panels,
			Panel{Title: "Payment provider: requests", Unit: "ops", Queries: []Query{{Sum("operation, outcome", sc.Rate(provider+"_count")), "{{operation}} {{outcome}}"}},
				Description: "`declined` — бизнес-результат провайдера, не технический сбой."},
			Panel{Title: "Payment provider: errors and timeouts", Unit: "ops", Traces: true, Queries: []Query{{Sum("operation, outcome", sc.Rate(provider+"_count", `outcome=~"error|timeout"`)), "{{operation}} {{outcome}}"}}},
			Panel{Title: "Payment provider: p95", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "operation", provider), "{{operation}}"}}},
		)
	}
	return panels
}

func (g *generator) kafka(sc Scope) []Panel {
	k := g.opts.Kafka
	lag := Sum("topic", `kafka_consumergroup_lag{consumergroup="`+k.ConsumerGroup+`"}`)
	var consumed, produced, failures []Query
	var processed Query
	if k.Watermill {
		consumed = []Query{{Sum("handler_name, acked", sc.Rate("watermill_consumer_subscriber_messages_received_total")), "{{handler_name}} acked={{acked}}"}}
		produced = []Query{{Sum("publisher_name, success", sc.Rate("watermill_outbox_forwarder_publish_time_seconds_count")), "{{publisher_name}} success={{success}}"}}
		failures = []Query{{Sum("handler_name", sc.Rate("watermill_consumer_handler_execution_time_seconds_count", `success="false"`)), "{{handler_name}}"}}
		processed = Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `error_class=~"|business"`)), "processed"}
	} else {
		consumed = []Query{{Sum("messaging_destination_name", sc.Rate("messaging_client_consumed_messages_total")), "{{messaging_destination_name}}"}}
		produced = []Query{{Sum("messaging_destination_name", sc.Rate("messaging_client_sent_messages_total")), "{{messaging_destination_name}}"}}
		processed = Query{Sum("", sc.Rate("nomnom_messaging_process_duration_seconds_count", `error_class=~"|business"`)), "processed"}
	}
	failures = append(failures, Query{Sum("error_class", sc.Rate("nomnom_messaging_process_duration_seconds_count", `error_class!~"|business"`)), "{{error_class}}"})
	return []Panel{
		{Title: "Consumed", Unit: "ops", Queries: consumed},
		{Title: "Produced (Outbox Forwarder)", Unit: "ops", Queries: produced},
		{Title: "Processing failures", Unit: "ops", Queries: failures, Traces: true,
			Description: "Технические сбои обработки по `error_class` (permanent, deserialization, transient); business rejections подтверждаются (ack) и показаны в Service health отдельно."},
		{Title: "Consumer lag", Unit: "short", Queries: []Query{{lag, "{{topic}}"}},
			Description: "Lag consumer group `" + k.ConsumerGroup + "` из Alloy Kafka exporter; не путать с очередями предметной области."},
		{Title: "Retries and poison (DLQ)", Unit: "ops", Queries: []Query{
			{Sum("event_type", sc.Rate("nomnom_messaging_retry_attempts_total")), "retry {{event_type}}"},
			{Sum("stream, error_class", sc.Rate("nomnom_messaging_poison_messages_total")), "{{stream}}.dlq {{error_class}}"},
		}},
		{Title: "Processing duration p95 by event_type", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "event_type", "nomnom_messaging_process_duration_seconds"), "{{event_type}}"}}},
		{Title: "Message age at processing p95", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "event_type", "nomnom_messaging_message_age_seconds"), "{{event_type}}"}},
			Description: "`now − occurred_at` в момент обработки."},
		{Title: "Inbox: processed / duplicates", Unit: "ops", Queries: []Query{processed, {Sum("", sc.Rate("nomnom_inbox_duplicates_total")), "duplicates"}},
			Description: "Duplicate = конфликт уникальности `(consumer, event_id)` в Inbox; failures — панель Processing failures."},
		{Title: "Outbox pending", Unit: "short", Queries: []Query{{Sum("stream", sc.Gauge("nomnom_outbox_pending_messages")), "{{stream}}"}}},
		{Title: "Outbox oldest pending age", Unit: "s", Queries: []Query{{Max("stream", sc.Gauge("nomnom_outbox_oldest_pending_age_seconds")), "{{stream}}"}}},
		{Title: "Outbox publish retries", Unit: "ops", Queries: []Query{{Sum("stream, error_class", sc.Rate("nomnom_outbox_publish_retries_total")), "{{stream}} {{error_class}}"}}},
	}
}

// useCases — row декоратора application handlers: rejected — бизнес-отказ по
// правилам use case, error — технический сбой; это разные сигналы.
func useCases(sc Scope) []Panel {
	uc := "nomnom_usecase_duration_seconds"
	return []Panel{
		{Title: "Calls by outcome", Unit: "ops", Queries: []Query{{Sum("outcome", sc.Rate(uc+"_count")), "{{outcome}}"}},
			Description: "success, rejected (отказ по правилам use case) и error (технический сбой)."},
		{Title: "Rejections by use case", Unit: "ops", Queries: []Query{{Sum("usecase, error_type", sc.Rate(uc+"_count", `outcome="rejected"`)), "{{usecase}} {{error_type}}"}}},
		{Title: "Errors by use case", Unit: "ops", Queries: []Query{{Sum("usecase, error_type", sc.Rate(uc+"_count", `outcome="error"`)), "{{usecase}} {{error_type}}"}}},
		{Title: "Duration p95 by use case", Unit: "s", Queries: []Query{{sc.Quantile("0.95", "usecase", uc), "{{usecase}}"}}},
	}
}

func transactions(sc Scope) []Panel {
	return []Panel{
		{Title: "Transactions by outcome", Unit: "ops", Queries: []Query{{Sum("outcome", sc.Rate("nomnom_uow_transactions_total")), "{{outcome}}"}},
			Description: "committed, rolled_back и commit_failed Unit of Work."},
		{Title: "Transaction duration", Unit: "s", Queries: sc.Percentiles("", "", "nomnom_uow_duration_seconds")},
		{Title: "Retries by conflict", Unit: "ops", Queries: []Query{{Sum("reason", sc.Rate("nomnom_uow_retries_total")), "{{reason}}"}},
			Description: "serialization, deadlock, write_conflict — классы retry Unit of Work."},
	}
}

func lifecycle(sc Scope) []Panel {
	fsm := "nomnom_fsm_transitions_total"
	return []Panel{
		{Title: "Transitions", Unit: "ops", Queries: []Query{{Sum("aggregate_type, to_state", sc.Rate(fsm, `result="applied"`)), "{{aggregate_type}} → {{to_state}}"}}},
		{Title: "Invalid transitions", Unit: "ops", Queries: []Query{{Sum("aggregate_type, from_state, trigger", sc.Rate(fsm, `result="rejected"`)), "{{aggregate_type}} {{from_state}} × {{trigger}}"}}},
		{Title: "Terminal transitions", Unit: "ops", Queries: []Query{{Sum("aggregate_type, to_state", sc.Rate(fsm, `result="applied"`, `terminal="true"`)), "{{aggregate_type}} {{to_state}}"}}},
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
		{Title: "Activities executed / failed attempts", Unit: "ops", Traces: true, Queries: []Query{
			{Sum("activity_type", worker.Rate("temporal_activity_execution_latency_seconds_count")), "executed {{activity_type}}"},
			{Sum("activity_type", worker.Rate("temporal_activity_execution_failed_total")), "failed {{activity_type}}"},
		}, Description: "Каждая неуспешная попытка, кроме последней, ведёт к retry по Retry Policy."},
		{Title: "Activity latency p95", Unit: "s", Queries: []Query{{worker.Quantile("0.95", "activity_type", "temporal_activity_execution_latency_seconds"), "{{activity_type}}"}}},
		{Title: "Worker task slots", Unit: "short", Queries: []Query{
			{Sum("worker_type", worker.Gauge("temporal_worker_task_slots_used")), "used {{worker_type}}"},
			{Sum("worker_type", worker.Gauge("temporal_worker_task_slots_available")), "available {{worker_type}}"},
		}},
		{Title: "Compensations", Unit: "ops", Queries: []Query{{Sum("step, outcome", worker.Rate("nomnom_orders_compensations_total")), "{{step}} {{outcome}}"}}},
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
		{Title: "Delays", Unit: "ops", Queries: []Query{{Sum("reason", sc.Rate("nomnom_kitchen_delays_total")), "{{reason}}"}}},
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
		{Title: "Checks by result", Unit: "ops", Queries: []Query{{Sum("result", sc.Rate(checks)), "{{result}}"}}},
		{Title: "Denied by permission", Unit: "ops", Queries: []Query{{Sum("resource_type, permission", sc.Rate(checks, `result="denied"`)), "{{resource_type}}#{{permission}}"}},
			Description: "DENIED — решение политики, не сбой SpiceDB."},
		{Title: "Errors and timeouts", Unit: "ops", Traces: true, Queries: []Query{{Sum("result", sc.Rate(checks, `result=~"error|timeout"`)), "{{result}}"}},
			Description: "Технические сбои проверки; fail-closed превращает их в отказ клиенту."},
		{Title: "Check latency", Unit: "s", Queries: sc.Percentiles("", "", "nomnom_authz_check_duration_seconds")},
	}
}
