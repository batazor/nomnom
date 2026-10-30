package overview

import (
	"fmt"
	"strings"

	dashboard "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"

	"nomnom/ops/grafana/inventory"
)

// PlatformUID — Platform / Overview: серверная сторона общей инфраструктуры (SpiceDB,
// PostgreSQL) и pods всех контекстов. Не overview сервиса: владелец — ops, артефакт —
// ops/grafana/dashboards/platform.json.
const PlatformUID = "nomnom-platform-overview"

const PlatformPath = "ops/grafana/dashboards/platform.json"

// postgresOwners — владельцы PostgreSQL (ADR 8): label owner экспортера Alloy.
var postgresOwners = []string{"orders", "payments", "delivery"}

var appNamespaces = []string{"nomnom-orders", "nomnom-payments", "nomnom-delivery", "nomnom-restaurants", "nomnom-bff"}

func PlatformOverview() (Dashboard, error) {
	g := &generator{opts: Options{Context: "platform", Title: "Platform"}}
	g.dash = dashboard.NewDashboardBuilder("Platform / Overview").
		Description("Серверная сторона SpiceDB, PostgreSQL, CDC Outbox (Kafka Connect, Debezium) и Valkey, pods прикладных неймспейсов и сертификаты cert-manager: Prometheus scrape Alloy (job infra/*). Клиентская сторона — overview сервисов. Сгенерировано ops/grafana; правки — через генератор.").
		Tags([]string{"nomnom", "platform", "overview", "generated"}).
		Editable(false).
		CursorSync(dashboard.DashboardCursorSyncCrosshair).
		TimeSettings(dashboard.NewTimeSettingsBuilder().From("now-6h").To("now").AutoRefresh("1m").Timezone("browser")).
		Variables(g.platformVariables())

	spicedb := Scope{Services: []string{inventory.SpiceDB}, Job: "infra/spicedb"}
	postgres := Scope{Services: []string{inventory.PostgreSQLServer}, Job: "infra/postgresql", Matchers: []string{`owner=~"$owner"`}}
	pods := Scope{Services: inventory.AllServices, Job: jobKubernetes, Matchers: []string{`namespace=~"$namespace"`}}
	certs := Scope{Services: []string{inventory.CertManager}, Job: jobCertManager}
	cdc := Scope{Services: []string{inventory.KafkaConnect}, Job: jobKafkaConnect}
	valkey := Scope{Services: []string{inventory.Valkey}, Job: "infra/valkey", Matchers: []string{`owner=~"$valkey_owner"`}}

	layout := dashboard.Rows()
	for _, r := range []row{
		{title: "SpiceDB", scope: spicedb, panels: spicedbPanels(spicedb)},
		{title: "PostgreSQL", scope: postgres, panels: append(postgresPanels(postgres), slotPanels(postgres)...)},
		{title: "CDC (Debezium)", scope: cdc, panels: cdcPanels(cdc)},
		{title: "Valkey", scope: valkey, panels: valkeyPanels(valkey)},
		{title: "Pods", scope: pods, panels: platformPods(pods)},
		{title: "Certificates", scope: certs, panels: certificates(certs)},
	} {
		if b, ok := g.row(r); ok {
			layout.Row(b)
		}
	}
	g.dash.TabsLayout(dashboard.Tabs().
		Tab(dashboard.Tab("Technical").RowsLayout(layout)).
		Tab(g.aboutTab(platformAbout())))
	if len(g.errs) > 0 {
		return Dashboard{}, fmt.Errorf("%s: %s", PlatformUID, strings.Join(g.errs, "; "))
	}
	manifest, err := dashboard.Manifest(PlatformUID, g.dash).Build()
	if err != nil {
		return Dashboard{}, err
	}
	return Dashboard{UID: PlatformUID, Manifest: manifest, Rows: g.report}, nil
}

func (g *generator) platformVariables() []dashboard.VariableKind {
	v, err := dashboard.NewDatasourceVariableBuilder("metrics").Label("Metrics (Prometheus)").PluginId("prometheus").
		Refresh(dashboard.VariableRefreshOnDashboardLoad).Build()
	g.record(err)
	return []dashboard.VariableKind{
		{DatasourceVariableKind: &v},
		customVariable("owner", "PostgreSQL owner", postgresOwners, true),
		customVariable("valkey_owner", "Valkey owner", valkeyOwners, true),
		customVariable("namespace", "Namespace", appNamespaces, true),
	}
}

const spicedbAPI = `grpc_service=~"authzed\\.api\\.v1\\..+"`

func spicedbPanels(sc Scope) []Panel {
	return []Panel{
		{Title: "Requests by method", Unit: "ops", Queries: []Query{rateBy(sc, "grpc_method", "grpc_server_handled_total", spicedbAPI)}},
		{Title: "Errors by method and code", Unit: "ops", Queries: []Query{{Sum("grpc_method, grpc_code", sc.Rate("grpc_server_handled_total", spicedbAPI, `grpc_code!="OK"`)), "{{grpc_method}} {{grpc_code}}"}},
			Description: "Ответы SpiceDB не OK. Клиенты fail-closed: такие ответы становятся отказом в доступе."},
		{Title: "Latency p95 by method", Unit: "s", Queries: []Query{{Expr: sc.Quantile("0.95", "grpc_method", "grpc_server_handling_seconds", spicedbAPI), Legend: "{{grpc_method}}"}}},
		{Title: "CheckPermission results", Unit: "ops", Queries: []Query{rateBy(sc, "permissionship", "spicedb_v1_check_permissionship_total", `method="CheckPermission"`)},
			Description: "NO_PERMISSION — решение политики, не сбой."},
		{Title: "Dispatch cache hit ratio", Unit: "percentunit", Queries: []Query{{
			Sum("", sc.Rate("spicedb_dispatch_client_check_from_cache_total")) + " / " + Sum("", sc.Rate("spicedb_dispatch_client_check_total")), "hit ratio"}}},
		{Title: "Datastore latency (avg) by operation", Unit: "s", Queries: []Query{{
			Sum("operation", sc.Rate("spicedb_datastore_query_latency_sum")) + " / " + Sum("operation", sc.Rate("spicedb_datastore_query_latency_count")), "{{operation}}"}},
			Description: "Средняя: buckets datastore не доставляются (квота series)."},
		{Title: "CPU", Unit: "short", Queries: []Query{{Sum("", sc.Rate("process_cpu_seconds_total")), "cores"}}},
		{Title: "Resident memory", Unit: "bytes", Queries: []Query{{Sum("", sc.Gauge("process_resident_memory_bytes")), "RSS"}}},
	}
}

func postgresPanels(sc Scope) []Panel {
	rate := func(metric string) string { return Sum("owner", sc.Rate(metric)) }
	return []Panel{
		{Title: "Up", Unit: "short", Queries: []Query{{Max("owner", sc.Gauge("pg_up")), "{{owner}}"}},
			Description: "0 — экспортер Alloy не подключился к базе владельца."},
		{Title: "Connections", Unit: "short", Queries: []Query{{Sum("owner", sc.Gauge("pg_stat_database_numbackends")), "{{owner}}"}}},
		{Title: "Transactions", Unit: "ops", Queries: []Query{
			{rate("pg_stat_database_xact_commit"), "{{owner}} commit"},
			{rate("pg_stat_database_xact_rollback"), "{{owner}} rollback"},
		}},
		{Title: "Buffer cache hit ratio", Unit: "percentunit", Queries: []Query{{
			rate("pg_stat_database_blks_hit") + " / (" + rate("pg_stat_database_blks_hit") + " + " + rate("pg_stat_database_blks_read") + ")", "{{owner}}"}}},
		{Title: "Deadlocks and conflicts", Unit: "ops", Queries: []Query{
			{rate("pg_stat_database_deadlocks"), "{{owner}} deadlocks"},
			{rate("pg_stat_database_conflicts"), "{{owner}} conflicts"},
		}},
		{Title: "Temp files written", Unit: "Bps", Queries: []Query{{rate("pg_stat_database_temp_bytes"), "{{owner}}"}},
			Description: "Сортировки и hash, не уместившиеся в work_mem."},
		{Title: "Database size", Unit: "bytes", Queries: []Query{{Sum("owner", sc.Gauge("pg_database_size_bytes")), "{{owner}}"}}},
	}
}

// platformPods — те же панели, что row Pods сервиса, с разбивкой по неймспейсу.
func platformPods(sc Scope) []Panel {
	return []Panel{
		{Title: "Ready / total pods by namespace", Unit: "short", Queries: []Query{
			{Sum("namespace", sc.Gauge("kube_pod_status_ready", `condition="true"`)), "{{namespace}} ready"},
			{"count by (namespace) (" + sc.Gauge("kube_pod_status_ready", `condition="true"`) + ")", "{{namespace}} pods"},
		}},
		{Title: "Restarts by pod", Unit: "short", Queries: []Query{{Sum("namespace, pod", sc.Increase("kube_pod_container_status_restarts_total")), "{{namespace}}/{{pod}}"}}},
		{Title: "Waiting containers by reason", Unit: "short", Queries: []Query{{Sum("namespace, reason", sc.Gauge("kube_pod_container_status_waiting_reason")), "{{namespace}} {{reason}}"}}},
		{Title: "CPU by namespace", Unit: "short", Queries: []Query{rateBy(sc, "namespace", "container_cpu_usage_seconds_total")}},
		{Title: "Memory working set by namespace", Unit: "bytes", Queries: []Query{{Sum("namespace", sc.Gauge("container_memory_working_set_bytes")), "{{namespace}}"}}},
		{Title: "Network by namespace", Unit: "Bps", Queries: []Query{
			{Sum("namespace", sc.Rate("container_network_receive_bytes_total")), "{{namespace}} rx"},
			{Sum("namespace", sc.Rate("container_network_transmit_bytes_total")), "{{namespace}} tx"},
		}},
	}
}

const jobCertManager = "infra/cert-manager"

// contextCertificates — Certificate неймспейса контекста.
func contextCertificates(o Options) Scope {
	return Scope{Services: []string{inventory.CertManager}, Job: jobCertManager, Matchers: []string{fmt.Sprintf(`namespace="nomnom-%s"`, o.Context)}}
}

const certReady = `condition="True"`

// certificates — Certificate cert-manager: срок действия, плановое продление и Ready.
// Без matchers scope — все сертификаты кластера, с namespace — сертификаты контекста.
func certificates(sc Scope) []Panel {
	return []Panel{
		{Title: "Time to expiry", Unit: "s", Queries: []Query{{Min("namespace, name", sc.Gauge("certmanager_certificate_not_after_timestamp_seconds")) + " - time()", "{{namespace}}/{{name}}"}},
			Description: "До NotAfter. cert-manager продлевает заранее (renewBefore), срок не должен приближаться к нулю."},
		{Title: "Time to scheduled renewal", Unit: "s", Queries: []Query{{Min("namespace, name", sc.Gauge("certmanager_certificate_renewal_timestamp_seconds")) + " - time()", "{{namespace}}/{{name}}"}},
			Description: "Отрицательное значение — продление просрочено: issuer не выпускает сертификат."},
		{Title: "Ready", Unit: "short", Queries: []Query{{Min("namespace, name", sc.Gauge("certmanager_certificate_ready_status", certReady)), "{{namespace}}/{{name}}"}},
			Description: "0 — Certificate не Ready: сертификат не выпущен, просрочен или не совпадает со spec."},
	}
}

// jobKafkaConnect — Alloy scrape JMX exporter Kafka Connect (ADR 37).
const jobKafkaConnect = "infra/kafka-connect"

// slotLag — WAL, который слот Debezium держит на диске: от текущей позиции WAL до
// подтверждённой коннектором.
func slotLag(sc Scope) string {
	return "max by (owner, slot_name) (" + sc.Gauge("pg_replication_slot_slot_current_wal_lsn") + " - " +
		sc.Gauge("pg_replication_slot_slot_confirmed_flush_lsn") + ")"
}

// cdcPanels — доставка Outbox всех контекстов (ADR 37): коннекторы, поток и отставание
// Debezium; слоты репликации — row PostgreSQL (slotPanels).
func cdcPanels(sc Scope) []Panel {
	return []Panel{
		{Title: "Connector tasks by status", Unit: "short", Queries: []Query{{Sum("connector, status", sc.Gauge("kafka_connect_connector_task_status")), "{{connector}} {{status}}"}},
			Description: "Задача `failed` — Outbox контекста не доставляется: `GET /connectors/<name>/status`, после исправления — restart задачи."},
		{Title: "Events published", Unit: "ops", Queries: []Query{rateBy(sc, "name", "debezium_metrics_TotalNumberOfEventsSeen")},
			Description: "Записи Outbox, прочитанные из журнала базы и отправленные в Kafka."},
		{Title: "Lag behind source", Unit: "ms", Queries: []Query{{Max("name", sc.Gauge("debezium_metrics_MilliSecondsBehindSource")), "{{name}}"}},
			Description: "От commit записи Outbox до её обработки коннектором; −1 до первого события задачи."},
		{Title: "Queue fill", Unit: "percentunit", Queries: []Query{{
			"1 - " + Max("name", sc.Gauge("debezium_metrics_QueueRemainingCapacity")) + " / " + Max("name", sc.Gauge("debezium_metrics_QueueTotalCapacity")), "{{name}}"}},
			Description: "Заполнение очереди коннектора: близко к 100% — Kafka не успевает принимать события."},
	}
}

// slotPanels — слоты Debezium <owner>_outbox (ADR 37) на серверах владельцев.
func slotPanels(slots Scope) []Panel {
	return []Panel{
		{Title: "Replication slot lag", Unit: "bytes", Queries: []Query{{slotLag(slots), "{{owner}} {{slot_name}}"}},
			Description: "WAL, который слот держит на диске. Растёт, если коннектор остановлен или отстаёт."},
		{Title: "Replication slot active", Unit: "short", Queries: []Query{{Min("owner, slot_name", slots.Gauge("pg_replication_slot_slot_is_active")), "{{owner}} {{slot_name}}"}}},
	}
}
