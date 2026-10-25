package overview

// Connection pool клиента БД по semconv db.client.connection.* (раздел db инвентаря):
// одни имена для PostgreSQL (pgx, psycopg_pool) и MongoDB (CMAP events драйвера).
// Пул — серия db_client_connection_pool_name процесса (job, instance): у MongoDB пул
// на каждый сервер replica set, поэтому насыщение считается по пулу, а не суммой.

const poolBy = "job, instance, db_client_connection_pool_name"

// poolSaturation — used / max каждого пула (0..1).
func poolSaturation(sc Scope) string {
	return Sum(poolBy, sc.Gauge("db_client_connection_count", `db_client_connection_state="used"`)) +
		" / " + Sum(poolBy, sc.Gauge("db_client_connection_max"))
}

// poolPanels — row Dependencies сервиса с Options.DatabasePool.
func poolPanels(sc Scope, db Database) []Panel {
	name := string(db)
	pool := "db_client_connection_pool_name"
	limit := poolLimit(db)
	panels := []Panel{
		{Title: name + ": pool connections", Unit: "short", Queries: []Query{
			{Sum("db_client_connection_state", sc.Gauge("db_client_connection_count")), "{{db_client_connection_state}}"},
			{Sum("", sc.Gauge("db_client_connection_max")), "max"},
			{Sum("", sc.Gauge("db_client_connection_idle_min")), "idle min"},
		}, Description: "Сумма по процессам и пулам. Насыщение одного пула — панель «pool saturation»."},
		{Title: name + ": pool saturation", Unit: "percentunit", Queries: []Query{{Max("job, "+pool, poolSaturation(sc)), "{{job}} {{" + pool + "}}"}},
			Description: "used / max самого загруженного процесса по пулу. Долго около 1 — checkouts ждут в очереди: поднять " + limit + " процесса или найти долгие операции (use time)."},
		{Title: name + ": pending checkouts and timeouts", Unit: "short", Queries: []Query{
			{Sum("job", sc.Gauge("db_client_connection_pending_requests")), "pending {{job}}"},
			{Sum("job", sc.Increase("db_client_connection_timeouts_total")), "timeouts {{job}}"},
		}, Description: "Ожидающие checkout и checkouts, не получившие соединение за wait timeout (операция завершилась ошибкой)."},
		{Title: name + ": pool wait / use / create p95", Unit: "s", Queries: []Query{
			{sc.Quantile("0.95", "", "db_client_connection_wait_time_seconds"), "wait p95"},
			{sc.Quantile("0.95", "", "db_client_connection_use_time_seconds"), "use p95"},
			{sc.Quantile("0.95", "", "db_client_connection_create_time_seconds"), "create p95"},
		}, Description: "wait — ожидание соединения пула, use — от checkout до возврата, create — установка соединения с handshake и auth."},
	}
	if db == MongoDB {
		panels = append(panels, Panel{Title: name + ": pool cleared", Unit: "short", Queries: []Query{{Sum("job, "+pool, sc.Increase("nomnom_db_client_connection_pool_clears_total")), "{{job}} {{" + pool + "}}"}},
			Description: "Драйвер пометил сервер недоступным и закрыл соединения его пула: failover, сетевой сбой или рестарт узла replica set."})
	}
	return panels
}

// poolLimit — настройка размера пула процесса в Helm values. У PostgreSQL перед базой
// пулер в session mode: соединения всех реплик занимают его pool_size.
func poolLimit(db Database) string {
	if db == PostgreSQL {
		return "maxConns (Σ replicas × maxConns ≤ pool_size пулера)"
	}
	return "maxPoolSize"
}

// poolAlerts — правила пула; пороги — стартовые для тестового окружения.
func poolAlerts(o Options, sc Scope, dash string) []Alert {
	slug := func(s string) string { return o.Context + "-db-pool-" + s }
	title := o.Title + ": " + string(o.Database) + " pool "
	pool := "job, db_client_connection_pool_name"
	alerts := []Alert{
		{Slug: slug("saturation"), Title: title + "saturated", Severity: Warning, For: "10m", Op: ">", Threshold: 0.9, DashboardID: dash,
			Summary: "Пул процесса занят больше чем на 90% (used / max) 10 минут: новые операции ждут соединения. Проверить use time и долгие операции, затем " + poolLimit(o.Database) + " процесса в Helm values.",
			Value:   Max(pool, poolSaturation(sc))},
		{Slug: slug("queued"), Title: title + "checkouts queued", Severity: Warning, For: "10m", Op: ">", Threshold: 0, DashboardID: dash,
			Summary: "Checkouts ждут соединения пула 10 минут подряд: пул мал для нагрузки или соединения долго не возвращаются.",
			Value:   Max(pool, sc.Gauge("db_client_connection_pending_requests"))},
		{Slug: slug("wait"), Title: title + "wait p95", Severity: Warning, For: "10m", Op: ">", Threshold: 0.1, DashboardID: dash,
			Summary: "p95 ожидания соединения пула дольше 100 ms: латентность операций растёт из-за очереди пула, не из-за БД.",
			Value:   sc.Quantile("0.95", pool, "db_client_connection_wait_time_seconds")},
		{Slug: slug("timeouts"), Title: title + "checkout timeouts", Severity: Warning, For: "0s", Op: ">", Threshold: 0, DashboardID: dash,
			Summary: "Операции не получили соединение пула за wait timeout и завершились ошибкой (UNAVAILABLE клиентам).",
			Value:   Sum(pool, sc.Increase("db_client_connection_timeouts_total"))},
	}
	if o.Database == MongoDB {
		alerts = append(alerts, Alert{Slug: slug("cleared"), Title: title + "cleared", Severity: Warning, For: "0s", Op: ">", Threshold: 0, DashboardID: dash,
			Summary: "Драйвер MongoDB очистил пул сервера: failover, сетевой сбой или рестарт узла replica set. Операции на закрытых соединениях повторяются (retryable reads/writes); повторяющиеся очистки — проверить узлы replica set.",
			Value:   Sum(pool, sc.Increase("nomnom_db_client_connection_pool_clears_total"))})
	}
	return alerts
}
