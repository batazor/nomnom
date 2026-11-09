package overview

import (
	"fmt"
	"slices"
	"strings"

	"nomnom/ops/grafana/inventory"
)

// rowsTable — rows и статусы панелей; root — путь от README до корня репозитория.
func rowsTable(b *strings.Builder, d Dashboard, root string) {
	b.WriteString("## Rows\n\n")
	live, total := 0, 0
	for _, r := range d.Rows {
		fmt.Fprintf(b, "### %s\n\n| Панель | Статус | Недостающие метрики |\n| --- | --- | --- |\n", r.Title)
		for _, p := range r.Panels {
			total++
			status := "TODO"
			if p.Live {
				status = "live"
				live++
			}
			missing := "—"
			if len(p.Missing) > 0 {
				var links []string
				for _, m := range p.Missing {
					metric, _ := inventory.Lookup(m)
					links = append(links, fmt.Sprintf("[`%s`](%s%s#%s)", m, root, inventory.DocPath, metric.Section))
				}
				missing = strings.Join(links, ", ")
			}
			fmt.Fprintf(b, "| %s | %s | %s |\n", p.Title, status, missing)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(b, "Живых панелей: %d из %d. TODO-панели нет в dashboard: она появляется после смены статуса метрик в инвентаре и регенерации.\n\n", live, total)
}

// alertsTable — alert rules dashboard: какие активны, какие ждут метрик.
func alertsTable(b *strings.Builder, a AlertGroup, file, root string) {
	fmt.Fprintf(b, "## Alerts\n\nRule group `%s` в folder `%s` ([`%s`](%s)), push — `make grafana-cloud-push`. Правило создаётся, только когда его метрики «есть»: alert без данных ничего не защищает. Пороги — стартовые для тестового окружения; SLO burn rate — по целям [ADR 40](%sdocs/adr/0040-service-slo-and-burn-rate-alerts.md).\n\n", a.Name, AlertFolder, file, file, root)
	if len(a.Report) == 0 {
		b.WriteString("Правил нет.\n\n")
		return
	}
	b.WriteString("| Alert | Severity | Статус | Недостающие метрики |\n| --- | --- | --- | --- |\n")
	for _, r := range a.Report {
		status := "ждёт метрик"
		if r.Live {
			status = "активно"
		}
		missing := "—"
		if len(r.Missing) > 0 {
			var links []string
			for _, m := range r.Missing {
				metric, _ := inventory.Lookup(m)
				links = append(links, fmt.Sprintf("[`%s`](%s%s#%s)", m, root, inventory.DocPath, metric.Section))
			}
			missing = strings.Join(links, ", ")
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s |\n", r.Title, r.Severity, status, missing)
	}
	b.WriteString("\n")
}

// PlatformREADME — ops/grafana/dashboards/README.md рядом с platform.json.
func PlatformREADME(d Dashboard, alerts AlertGroup) string {
	var b strings.Builder
	b.WriteString("<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную. -->\n\n# Platform / Overview\n\n")
	b.WriteString("Серверная сторона общей инфраструктуры и pods всех прикладных неймспейсов. Клиентская сторона (Authorization, Dependencies) и Pods конкретного сервиса — в overview сервиса; оттуда ведёт ссылка сюда.\n\n")
	fmt.Fprintf(&b, "| | |\n| --- | --- |\n| UID | `%s` |\n| Файл | [`platform.json`](platform.json) |\n| Схема | `dashboard.grafana.app/v2` (Grafana 13) |\n| Доставка | Git Sync, repository [`ops/grafana/git-sync/platform.yaml`](../git-sync/platform.yaml) |\n| Генератор | [`ops/grafana`](../README.md), функция `overview.PlatformOverview` |\n| Источники | Alloy Prometheus scrape: `infra/spicedb`, `integrations/db-o11y` (PostgreSQL), `infra/kubernetes` ([`infra.alloy`](../../kind/manifests/observability/alloy/infra.alloy)) |\n\n", d.UID)
	b.WriteString("## Variables\n\n| Variable | Источник | Назначение |\n| --- | --- | --- |\n")
	b.WriteString("| `metrics` (общая, global) | data source Prometheus, [`ops/grafana/variables`](../variables) | Все запросы. |\n")
	fmt.Fprintf(&b, "| `owner` (section, row PostgreSQL) | владельцы PostgreSQL (`%s`) | label `owner` метрик баз и слотов (Alloy берёт его из `datname` и `slot_name`); Up — сервер, без фильтра. |\n", strings.Join(postgresOwners, "`, `"))
	if liveRow(d, "Valkey") {
		fmt.Fprintf(&b, "| `valkey_owner` (section, row Valkey) | владельцы Valkey (`%s`) | label `owner` метрик Valkey. |\n", strings.Join(valkeyOwners, "`, `"))
	}
	fmt.Fprintf(&b, "| `namespace` (section, row Pods) | прикладные неймспейсы (`%s`) | Неймспейс pods. |\n", strings.Join(appNamespaces, "`, `"))
	b.WriteString(filterRow(d, "Pods", "pod"))
	b.WriteString("\n")
	b.WriteString("Environment и version не фильтруются: у Prometheus-метрик инфраструктуры нет `target_info`.\n\n")
	rowsTable(&b, d, "../../../")
	alertsTable(&b, alerts, "../alerts/platform.json", "../../../")
	return b.String()
}

// README — boundaries/<context>/ops/grafana/README.md, генерируется вместе с dashboard,
// чтобы описание rows и статусы панелей не расходились с JSON.
func README(o Options, d Dashboard, alerts AlertGroup) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную. -->\n\n# %s / Overview\n\n", o.Title)
	fmt.Fprintf(&b, "Overview-dashboard bounded context `%s`: один на сервис, отвечает на вопросы incident — работает ли сервис, выросли ли ошибки и latency, какая зависимость деградировала, есть ли backlog, что с Inbox/Outbox и бизнес-потоком, где trace/log/profile.\n\n", o.Context)
	fmt.Fprintf(&b, "| | |\n| --- | --- |\n| UID | `%s` |\n| Файл | [`dashboards/overview.json`](dashboards/overview.json) |\n| Схема | `dashboard.grafana.app/v2` (Grafana 13) |\n| Доставка | Git Sync, repository [`ops/grafana/git-sync/%s.yaml`](../../../../ops/grafana/git-sync/%s.yaml) |\n| Генератор | [`ops/grafana`](../../../../ops/grafana/README.md), функция `overview.ServiceOverview` |\n| service.name | `%s` |\n\n", d.UID, o.Context, o.Context, strings.Join(o.Services, "`, `"))
	b.WriteString("JSON — артефакт генератора: правки вносятся в `ops/grafana/overview/services.go` или в инвентарь, затем `make grafana-generate`. CI (`make grafana-check`) падает при расхождении.\n\n")

	sloTable(&b, o)
	b.WriteString("## Data sources\n\nВыбираются общими (cross-dashboard, global) variables по типу plugin, без UID окружения: `metrics` (Prometheus API Grafana Cloud), `logs` (Loki), `traces` (Tempo), `profiles` (Pyroscope). Определения — [`ops/grafana/variables`](../../../../ops/grafana/variables), dashboard подключает их annotation `grafana.app/useCrossDashboardVariables`. В шапке только `metrics`, остальные — в меню Controls: они нужны лишь ссылкам Explore. Нужен feature toggle `grafana.dashboardGlobalVariables` ([ADR 29](../../../../docs/adr/0029-grafana-dashboards-as-code.md)).\n\n")

	b.WriteString("## Variables\n\n| Variable | Источник | Назначение |\n| --- | --- | --- |\n")
	b.WriteString("| `environment` | `target_info` → `deployment_environment_name` | Фильтр окружения через join с `target_info`. |\n")
	fmt.Fprintf(&b, "| `service` | список service.name из генератора (`%s`) | `job=~\"nomnom/(${service:pipe})\"`. |\n", strings.Join(o.Services, "`, `"))
	b.WriteString("| `version` | `target_info` → `service_version` | Сравнение версий во время выкатки. |\n")
	b.WriteString("| `service_instance` | `target_info` → `instance` (`service.instance.id`) | Диагностика экземпляра. |\n")
	if o.Temporal != nil {
		fmt.Fprintf(&b, "| `worker` (section, row Temporal) | `%s` | Temporal worker фильтруется отдельно от API. |\n", o.Temporal.Worker)
	}
	b.WriteString(filterRow(d, "Pods", "pod"))
	b.WriteString("\n`namespace` не добавлен: `k8s.namespace.name` не входит в принятые resource attributes. High-cardinality variables (`order_id`, `user_id`, `payment_id`, `event_id`, `workflow_id`) запрещены — для них traces и logs.\n\n")

	rowsTable(&b, d, "../../../../")
	alertsTable(&b, alerts, "alerts/rules.json", "../../../../")

	b.WriteString("## Переходы\n\n")
	b.WriteString("- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.\n")
	b.WriteString("- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); у серий панелей ошибок и latency — data link «Traces этой серии»: spans с теми же attributes, что labels серии (`rpc.method`, `rpc.response.status_code`, `db.operation.name`, `nomnom.usecase.name`, `error.type`), у latency — не быстрее значения точки.\n")
	b.WriteString("- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу.")
	if o.Language == Go || o.Language == Python {
		b.WriteString(" Trace → profile (Span Profiles, только CPU) поддерживается runtime и включается в data source Tempo после проверки end-to-end.\n")
	} else {
		b.WriteString(" Span Profiles для этого runtime не поддерживаются: только корреляция service/version/time.\n")
	}
	b.WriteString(exemplarsLine(o))
	b.WriteString("- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».\n")
	if o.Database != "" || o.Kafka != nil {
		b.WriteString("- **Серверные метрики зависимостей** — ссылки на community dashboards в шапке; панели не дублируются.\n")
	}
	return b.String()
}

// exemplarsLine — где latency-панели показывают exemplars (inventory.Exemplars) и почему
// их нет у остальных ролей.
func exemplarsLine(o Options) string {
	var with, without []string
	for _, service := range o.Services {
		if slices.Contains(inventory.Exemplars, service) {
			with = append(with, "`"+service+"`")
		} else {
			without = append(without, "`"+service+"`")
		}
	}
	if len(with) == 0 {
		var reason string
		switch o.Language {
		case Node:
			reason = "JS SDK (`@opentelemetry/sdk-metrics`) exemplars не реализует"
		case Rust:
			reason = "Rust SDK (`opentelemetry_sdk`) пишет точки без exemplars"
		case Cosmo:
			reason = "запись exemplars Cosmo Router не проверена"
		default:
			panic(fmt.Sprintf("overview %s: %s SDK пишет exemplars, но ни одна роль не подтверждена в inventory.Exemplars", o.Context, o.Language))
		}
		return "- **Exemplars** — нет: " + reason + ". Переход к trace — через `trace_id` логов.\n"
	}
	line := "- **Exemplars** — в latency-панелях (`histogram_quantile`) " + strings.Join(with, ", ") +
		": точка exemplar несёт `trace_id` и `span_id` измерения внутри sampled span. " +
		"Кнопки перехода в Tempo нет: data source `grafanacloud-prom` (read-only, Grafana Cloud) связывает с Tempo label `traceID`, " +
		"а OTLP-ingestion пишет `trace_id`; trace открывается по значению из tooltip."
	if len(without) > 0 {
		line += " Без exemplars: " + strings.Join(without, ", ") + " (причина — `inventory.Exemplars`)."
	}
	if o.Kafka != nil && o.Kafka.Watermill {
		line += " Метрики Watermill (Prometheus bridge) exemplars не несут."
	}
	return line + "\n"
}

// sloTable — цели сервиса (ADR 40); без SLO раздел отсутствует.
func sloTable(b *strings.Builder, o Options) {
	slis := o.slis()
	if len(slis) == 0 {
		return
	}
	fmt.Fprintf(b, "## SLO\n\nЦели за скользящие %s, решение — [ADR 40](../../../../docs/adr/0040-service-slo-and-burn-rate-alerts.md). Business rejection бюджет не тратит.\n\n| SLI | Цель | Error budget |\n| --- | --- | --- |\n", sloWindow)
	for _, s := range slis {
		fmt.Fprintf(b, "| %s | %s | %s |\n", s.name, percent(s.target), percent(1-s.target))
	}
	b.WriteString("\n")
}

// filterRow — строка таблицы Variables для Filter row (ad hoc filter).
func filterRow(d Dashboard, title string, keys ...string) string {
	if !liveRow(d, title) {
		return ""
	}
	return fmt.Sprintf("| `%s` (section, row %s) | ad hoc Filter, ключи `%s` | Фильтр панелей row; ключ есть у всех метрик row. |\n",
		sectionFilterName(title), title, strings.Join(keys, "`, `"))
}

// liveRow — row выводится: в нём есть живая панель. У невыведенного row нет и его section variables.
func liveRow(d Dashboard, title string) bool {
	for _, r := range d.Rows {
		for _, p := range r.Panels {
			if r.Title == title && p.Live {
				return true
			}
		}
	}
	return false
}
