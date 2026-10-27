package overview

// Кеш чтения в Valkey (ADR 43, раздел cache инвентаря): клиентская сторона —
// lookups и ошибки API, обновления по событиям — consumer контекста. Серверные
// метрики Valkey — row Valkey в Platform / Overview.

const cacheBy = "nomnom_cache_name"

// cacheBypassShare — доля чтений мимо Valkey (0..1) по кешу.
func cacheBypassShare(sc Scope, by string) string {
	return Sum(by, sc.Rate("nomnom_cache_lookups_total", `nomnom_cache_result="bypass"`)) +
		" / " + Sum(by, sc.Rate("nomnom_cache_lookups_total"))
}

// cachePanels — row Cache сервиса с Options.Cache.
func cachePanels(sc Scope) []Panel {
	lookups := func(result string) string {
		return Sum(cacheBy, sc.Rate("nomnom_cache_lookups_total", `nomnom_cache_result=~"`+result+`"`))
	}
	return []Panel{
		{Title: "Hit ratio by cache", Unit: "percentunit", Queries: []Query{{lookups("hit") + " / " + lookups("hit|miss"), "{{nomnom_cache_name}}"}},
			Description: "hit / (hit + miss) по чтениям, которые дошли до Valkey; bypass не входит. Низкая доля при стабильной нагрузке — TTL короче интервала повторных чтений или ключи вытесняются (evictions в Platform / Overview)."},
		{Title: "Lookups by result", Unit: "ops", Queries: []Query{{Sum(cacheBy+", nomnom_cache_result", sc.Rate("nomnom_cache_lookups_total")), "{{nomnom_cache_name}} {{nomnom_cache_result}}"}}},
		{Title: "Bypass share", Unit: "percentunit", Queries: []Query{{cacheBypassShare(sc, cacheBy), "{{nomnom_cache_name}}"}},
			Description: "Чтения мимо кеша: Valkey недоступен или команда не уложилась в VALKEY__COMMAND_TIMEOUT. Ответы не ошибочные, но вся нагрузка идёт в базу."},
		{Title: "Refreshes by events", Unit: "ops", Queries: []Query{{Sum(cacheBy, sc.Rate("nomnom_cache_refreshes_total")), "{{nomnom_cache_name}}"}},
			Description: "Ключи, перезаписанные consumer'ом по Integration Events изменения (и новые поколения страниц списка)."},
		{Title: "Valkey command errors", Unit: "short", Queries: []Query{{Sum(cacheBy+", nomnom_cache_operation", sc.Increase("nomnom_cache_errors_total")), "{{nomnom_cache_name}} {{nomnom_cache_operation}}"}},
			Description: "get и set — чтение API (ответ всё равно из базы), refresh — обновление по событию (повторяется, offset не коммитится)."},
	}
}

// cacheAlerts — правило доли bypass; порог стартовый для тестового окружения.
func cacheAlerts(o Options, sc Scope, dash string) []Alert {
	return []Alert{{
		Slug: o.Context + "-cache-bypass", Title: o.Title + ": cache bypassed", Severity: Warning, For: "10m", Op: ">", Threshold: 0.5, DashboardID: dash,
		Summary: "Больше половины чтений кеша 10 минут идут мимо Valkey: он недоступен или медленнее VALKEY__COMMAND_TIMEOUT. Ответы корректны, но нагрузка на базу растёт; проверить row Valkey в Platform / Overview и pod Valkey.",
		Value:   Max("", cacheBypassShare(sc, "job")),
	}}
}

// valkeyOwners — владельцы Valkey (ADR 43): label owner экспортера Alloy.
var valkeyOwners = []string{"restaurants"}

// valkeyPanels — row Valkey в Platform / Overview.
func valkeyPanels(sc Scope) []Panel {
	rate := func(metric string) string { return Sum("owner", sc.Rate(metric)) }
	return []Panel{
		{Title: "Up", Unit: "short", Queries: []Query{{Max("owner", sc.Gauge("redis_up")), "{{owner}}"}},
			Description: "0 — экспортер Alloy не подключился к Valkey владельца."},
		{Title: "Memory used / max", Unit: "bytes", Queries: []Query{
			{Sum("owner", sc.Gauge("redis_memory_used_bytes")), "{{owner}} used"},
			{Sum("owner", sc.Gauge("redis_memory_max_bytes")), "{{owner}} maxmemory"},
		}, Description: "У maxmemory политика allkeys-lru: у предела ключи вытесняются, а не отклоняются записи."},
		{Title: "Evicted and expired keys", Unit: "ops", Queries: []Query{
			{rate("redis_evicted_keys_total"), "{{owner}} evicted"},
			{rate("redis_expired_keys_total"), "{{owner}} expired"},
		}, Description: "expired — штатное истечение TTL; evicted — памяти не хватает на рабочий набор кеша."},
		{Title: "Keyspace hit ratio", Unit: "percentunit", Queries: []Query{{
			rate("redis_keyspace_hits_total") + " / (" + rate("redis_keyspace_hits_total") + " + " + rate("redis_keyspace_misses_total") + ")", "{{owner}}"}},
			Description: "По всем GET сервера; hit ratio по чтениям сервиса — row Cache его overview."},
		{Title: "Commands", Unit: "ops", Queries: []Query{{rate("redis_commands_processed_total"), "{{owner}}"}}},
		{Title: "Clients and keys", Unit: "short", Queries: []Query{
			{Sum("owner", sc.Gauge("redis_connected_clients")), "{{owner}} clients"},
			{Sum("owner", sc.Gauge("redis_db_keys")), "{{owner}} keys"},
		}},
	}
}
