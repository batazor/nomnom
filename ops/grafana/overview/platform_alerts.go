package overview

import "nomnom/ops/grafana/inventory"

// PlatformAlertsPath — rule group Platform / Overview.
const PlatformAlertsPath = "ops/grafana/alerts/platform.json"

// PlatformAlerts — SpiceDB, PostgreSQL, CDC Outbox, pods и сертификаты (Platform / Overview).
func PlatformAlerts() (AlertGroup, error) {
	spicedb := Scope{Services: []string{inventory.SpiceDB}, Job: "infra/spicedb"}
	postgres := Scope{Services: []string{inventory.PostgreSQLServer}, Job: "infra/postgresql"}
	pods := Scope{Services: inventory.AllServices, Job: jobKubernetes}
	certs := Scope{Services: []string{inventory.CertManager}, Job: jobCertManager}
	cdc := Scope{Services: []string{inventory.KafkaConnect}, Job: jobKafkaConnect}
	alerts := []Alert{
		{Slug: "spicedb-errors", Runbook: "spicedb", Title: "SpiceDB: errors", Severity: Critical, For: "10m", Op: ">", Threshold: 0.05, DashboardID: PlatformUID,
			Summary: "Больше 5% запросов к SpiceDB не OK: клиенты fail-closed, пользователи получают отказ в доступе.",
			Value:   Sum("", spicedb.Rate("grpc_server_handled_total", spicedbAPI, `grpc_code!="OK"`)) + " / " + Sum("", spicedb.Rate("grpc_server_handled_total", spicedbAPI))},
		{Slug: "spicedb-latency", Runbook: "spicedb", Title: "SpiceDB: p95 latency", Severity: Warning, For: "10m", Op: ">", Threshold: 0.25, DashboardID: PlatformUID,
			Summary: "p95 запросов к SpiceDB дольше 250 ms: проверки доступа тормозят все use cases.",
			Value:   spicedb.Quantile("0.95", "", "grpc_server_handling_seconds", spicedbAPI)},
		{Slug: "postgres-down", Runbook: "postgres", Title: "PostgreSQL: down", Severity: Critical, For: "5m", Op: "<", Threshold: 1, DashboardID: PlatformUID,
			Summary: "Экспортер не подключается к базе владельца: база недоступна или нет прав.",
			Value:   Max("owner", postgres.Gauge("pg_up"))},
		{Slug: "postgres-deadlocks", Runbook: "postgres", Title: "PostgreSQL: deadlocks", Severity: Warning, For: "0s", Op: ">", Threshold: 0, DashboardID: PlatformUID,
			Summary: "В базе владельца случились deadlocks.",
			Value:   Sum("owner", postgres.Increase("pg_stat_database_deadlocks"))},
		{Slug: "cdc-task-failed", Runbook: "cdc", Title: "CDC: connector task failed", Severity: Critical, For: "5m", Op: ">", Threshold: 0, DashboardID: PlatformUID,
			Summary: "Задача коннектора Debezium в FAILED: Outbox контекста не доставляется в Kafka (ADR 37). GET /connectors/<name>/status, после исправления — restart задачи.",
			Value:   Sum("connector", cdc.Gauge("kafka_connect_connector_task_status", `status="failed"`))},
		{Slug: "cdc-task-not-running", Runbook: "cdc", Title: "CDC: connector task not running", Severity: Warning, For: "15m", Op: ">", Threshold: 0, DashboardID: PlatformUID,
			Summary: "Задача коннектора Debezium дольше 15 минут не RUNNING (paused, unassigned, restarting): события Outbox ждут.",
			Value:   Sum("connector", cdc.Gauge("kafka_connect_connector_task_status", `status!="running"`))},
		{Slug: "cdc-lag", Runbook: "cdc", Title: "CDC: lag behind source", Severity: Warning, For: "10m", Op: ">", Threshold: 60000, DashboardID: PlatformUID,
			Summary: "Коннектор обрабатывает записи Outbox больше чем через минуту после commit: подписки и интеграция видят изменения с задержкой.",
			Value:   Max("name", cdc.Gauge("debezium_metrics_MilliSecondsBehindSource"))},
		{Slug: "replication-slot-inactive", Runbook: "cdc", Title: "PostgreSQL: replication slot inactive", Severity: Critical, For: "10m", Op: "<", Threshold: 1, DashboardID: PlatformUID,
			Summary: "Слот Debezium не читается: Outbox владельца не доставляется, WAL копится на диске.",
			Value:   Min("owner, slot_name", postgres.Gauge("pg_replication_slot_slot_is_active"))},
		{Slug: "replication-slot-lag", Runbook: "cdc", Title: "PostgreSQL: replication slot retains WAL", Severity: Warning, For: "15m", Op: ">", Threshold: 1 << 30, DashboardID: PlatformUID,
			Summary: "Слот Debezium держит больше 1 GiB WAL: коннектор отстаёт или остановлен, диск базы заполняется.",
			Value:   slotLag(postgres)},
		{Slug: "pod-crashloop", Runbook: "pods", Title: "Pods: crash looping", Severity: Critical, For: "5m", Op: ">", Threshold: 0, DashboardID: PlatformUID,
			Summary: "Контейнер в CrashLoopBackOff или ImagePullBackOff.",
			Value:   Sum("namespace, pod", pods.Gauge("kube_pod_container_status_waiting_reason", `reason=~"CrashLoopBackOff|ImagePullBackOff|ErrImagePull"`))},
		{Slug: "pod-restarts", Runbook: "pods", Title: "Pods: restarting", Severity: Warning, For: "0s", Op: ">", Threshold: 2, DashboardID: PlatformUID,
			Summary: "Контейнер перезапускался больше 2 раз за окно оценки.",
			Value:   Sum("namespace, pod", pods.Increase("kube_pod_container_status_restarts_total"))},
		{Slug: "pod-not-ready", Runbook: "pods", Title: "Pods: not ready", Severity: Warning, For: "10m", Op: "<", Threshold: 1, DashboardID: PlatformUID,
			Summary: "Pod не проходит readinessProbe и выведен из Service.",
			Value:   Max("namespace, pod", pods.Gauge("kube_pod_status_ready", `condition="true"`))},
		{Slug: "certificate-expiring", Runbook: "certificates", Title: "Certificates: expiring", Severity: Warning, For: "1h", Op: "<", Threshold: 14 * 24 * 3600, DashboardID: PlatformUID,
			Summary: "До NotAfter сертификата меньше 14 дней: cert-manager его не продлил.",
			Value:   Min("namespace, name", certs.Gauge("certmanager_certificate_not_after_timestamp_seconds")) + " - time()"},
		{Slug: "certificate-not-ready", Runbook: "certificates", Title: "Certificates: not ready", Severity: Critical, For: "15m", Op: "<", Threshold: 1, DashboardID: PlatformUID,
			Summary: "Certificate не Ready: сертификат не выпущен, просрочен или issuer его не выпускает.",
			Value:   Min("namespace, name", certs.Gauge("certmanager_certificate_ready_status", certReady))},
	}
	return buildGroup("platform", Scope{Services: append([]string{inventory.SpiceDB, inventory.PostgreSQLServer, inventory.CertManager, inventory.KafkaConnect}, inventory.AllServices...)}, alerts)
}
