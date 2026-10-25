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
		Description("Серверная сторона SpiceDB и PostgreSQL и pods прикладных неймспейсов: Prometheus scrape Alloy (job infra/*). Клиентская сторона — overview сервисов. Сгенерировано ops/grafana; правки — через генератор.").
		Tags([]string{"nomnom", "platform", "overview", "generated"}).
		Editable(false).
		CursorSync(dashboard.DashboardCursorSyncCrosshair).
		TimeSettings(dashboard.NewTimeSettingsBuilder().From("now-6h").To("now").AutoRefresh("1m").Timezone("browser")).
		Variables(g.platformVariables())

	spicedb := Scope{Services: []string{inventory.SpiceDB}, Job: "infra/spicedb"}
	postgres := Scope{Services: []string{inventory.PostgreSQLServer}, Job: "infra/postgresql", Matchers: []string{`owner=~"$owner"`}}
	pods := Scope{Services: inventory.AllServices, Job: jobKubernetes, Matchers: []string{`namespace=~"$namespace"`}}

	layout := dashboard.Rows()
	for _, r := range []row{
		{title: "SpiceDB", scope: spicedb, panels: spicedbPanels(spicedb)},
		{title: "PostgreSQL", scope: postgres, panels: postgresPanels(postgres)},
		{title: "Pods", scope: pods, panels: platformPods(pods)},
	} {
		layout.Row(g.row(r))
	}
	g.dash.RowsLayout(layout)
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
	v, err := dashboard.NewDatasourceVariableBuilder("metrics").Label("Metrics (Mimir)").PluginId("prometheus").
		Refresh(dashboard.VariableRefreshOnDashboardLoad).Build()
	g.record(err)
	return []dashboard.VariableKind{
		{DatasourceVariableKind: &v},
		customVariable("owner", "PostgreSQL owner", postgresOwners, true),
		customVariable("namespace", "Namespace", appNamespaces, true),
	}
}

const spicedbAPI = `grpc_service=~"authzed\\.api\\.v1\\..+"`

func spicedbPanels(sc Scope) []Panel {
	return []Panel{
		{Title: "Requests by method", Unit: "ops", Queries: []Query{{Sum("grpc_method", sc.Rate("grpc_server_handled_total", spicedbAPI)), "{{grpc_method}}"}}},
		{Title: "Errors by method and code", Unit: "ops", Queries: []Query{{Sum("grpc_method, grpc_code", sc.Rate("grpc_server_handled_total", spicedbAPI, `grpc_code!="OK"`)), "{{grpc_method}} {{grpc_code}}"}},
			Description: "Ответы SpiceDB не OK. Клиенты fail-closed: такие ответы становятся отказом в доступе."},
		{Title: "Latency p95 by method", Unit: "s", Queries: []Query{{Expr: sc.Quantile("0.95", "grpc_method", "grpc_server_handling_seconds", spicedbAPI), Legend: "{{grpc_method}}"}}},
		{Title: "CheckPermission results", Unit: "ops", Queries: []Query{{Sum("permissionship", sc.Rate("spicedb_v1_check_permissionship_total", `method="CheckPermission"`)), "{{permissionship}}"}},
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
		{Title: "CPU by namespace", Unit: "short", Queries: []Query{{Sum("namespace", sc.Rate("container_cpu_usage_seconds_total")), "{{namespace}}"}}},
		{Title: "Memory working set by namespace", Unit: "bytes", Queries: []Query{{Sum("namespace", sc.Gauge("container_memory_working_set_bytes")), "{{namespace}}"}}},
		{Title: "Network by namespace", Unit: "Bps", Queries: []Query{
			{Sum("namespace", sc.Rate("container_network_receive_bytes_total")), "{{namespace}} rx"},
			{Sum("namespace", sc.Rate("container_network_transmit_bytes_total")), "{{namespace}} tx"},
		}},
	}
}
