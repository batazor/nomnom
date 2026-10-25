package overview

import "nomnom/ops/grafana/inventory"

// PlatformAlertsPath — rule group Platform / Overview.
const PlatformAlertsPath = "ops/grafana/alerts/platform.json"

// PlatformAlerts — SpiceDB, PostgreSQL и pods (Platform / Overview).
func PlatformAlerts() (AlertGroup, error) {
	spicedb := Scope{Services: []string{inventory.SpiceDB}, Job: "infra/spicedb"}
	postgres := Scope{Services: []string{inventory.PostgreSQLServer}, Job: "infra/postgresql"}
	pods := Scope{Services: inventory.AllServices, Job: jobKubernetes}
	alerts := []Alert{
		{Slug: "spicedb-errors", Title: "SpiceDB: errors", Severity: Critical, For: "10m", Op: ">", Threshold: 0.05, DashboardID: PlatformUID,
			Summary: "Больше 5% запросов к SpiceDB не OK: клиенты fail-closed, пользователи получают отказ в доступе.",
			Value:   Sum("", spicedb.Rate("grpc_server_handled_total", spicedbAPI, `grpc_code!="OK"`)) + " / " + Sum("", spicedb.Rate("grpc_server_handled_total", spicedbAPI))},
		{Slug: "spicedb-latency", Title: "SpiceDB: p95 latency", Severity: Warning, For: "10m", Op: ">", Threshold: 0.25, DashboardID: PlatformUID,
			Summary: "p95 запросов к SpiceDB дольше 250 ms: проверки доступа тормозят все use cases.",
			Value:   spicedb.Quantile("0.95", "", "grpc_server_handling_seconds", spicedbAPI)},
		{Slug: "postgres-down", Title: "PostgreSQL: down", Severity: Critical, For: "5m", Op: "<", Threshold: 1, DashboardID: PlatformUID,
			Summary: "Экспортер не подключается к базе владельца: база недоступна или нет прав.",
			Value:   Max("owner", postgres.Gauge("pg_up"))},
		{Slug: "postgres-deadlocks", Title: "PostgreSQL: deadlocks", Severity: Warning, For: "0s", Op: ">", Threshold: 0, DashboardID: PlatformUID,
			Summary: "В базе владельца случились deadlocks.",
			Value:   Sum("owner", postgres.Increase("pg_stat_database_deadlocks"))},
		{Slug: "pod-crashloop", Title: "Pods: crash looping", Severity: Critical, For: "5m", Op: ">", Threshold: 0, DashboardID: PlatformUID,
			Summary: "Контейнер в CrashLoopBackOff или ImagePullBackOff.",
			Value:   Sum("namespace, pod", pods.Gauge("kube_pod_container_status_waiting_reason", `reason=~"CrashLoopBackOff|ImagePullBackOff|ErrImagePull"`))},
		{Slug: "pod-restarts", Title: "Pods: restarting", Severity: Warning, For: "0s", Op: ">", Threshold: 2, DashboardID: PlatformUID,
			Summary: "Контейнер перезапускался больше 2 раз за окно оценки.",
			Value:   Sum("namespace, pod", pods.Increase("kube_pod_container_status_restarts_total"))},
		{Slug: "pod-not-ready", Title: "Pods: not ready", Severity: Warning, For: "10m", Op: "<", Threshold: 1, DashboardID: PlatformUID,
			Summary: "Pod не проходит readinessProbe и выведен из Service.",
			Value:   Max("namespace, pod", pods.Gauge("kube_pod_status_ready", `condition="true"`))},
	}
	return buildGroup("platform", Scope{Services: append([]string{inventory.SpiceDB, inventory.PostgreSQLServer}, inventory.AllServices...)}, alerts)
}
