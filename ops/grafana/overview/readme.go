package overview

import (
	"fmt"
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
	fmt.Fprintf(b, "Живых панелей: %d из %d. TODO-панель показывает недостающие метрики и целевой PromQL; она становится живой после смены статуса в инвентаре и регенерации.\n\n", live, total)
}

// alertsTable — alert rules dashboard: какие активны, какие ждут метрик.
func alertsTable(b *strings.Builder, a AlertGroup, file, root string) {
	fmt.Fprintf(b, "## Alerts\n\nRule group `%s` в folder `%s` ([`%s`](%s)), push — `make grafana-cloud-push`. Правило создаётся, только когда его метрики «есть»: alert без данных ничего не защищает. Пороги — стартовые для тестового окружения, не SLO.\n\n", a.Name, AlertFolder, file, file)
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
	fmt.Fprintf(&b, "| | |\n| --- | --- |\n| UID | `%s` |\n| Файл | [`platform.json`](platform.json) |\n| Схема | `dashboard.grafana.app/v2` (Grafana 13) |\n| Доставка | Git Sync, repository [`ops/grafana/git-sync/platform.yaml`](../git-sync/platform.yaml) |\n| Генератор | [`ops/grafana`](../README.md), функция `overview.PlatformOverview` |\n| Источники | Alloy Prometheus scrape: `infra/spicedb`, `infra/postgresql`, `infra/kubernetes` ([`alloy.yaml`](../../kind/manifests/observability/alloy.yaml)) |\n\n", d.UID)
	b.WriteString("## Variables\n\n| Variable | Источник | Назначение |\n| --- | --- | --- |\n")
	fmt.Fprintf(&b, "| `owner` | владельцы PostgreSQL (`%s`) | label `owner` экспортера. |\n", strings.Join(postgresOwners, "`, `"))
	fmt.Fprintf(&b, "| `namespace` | прикладные неймспейсы (`%s`) | Row Pods. |\n\n", strings.Join(appNamespaces, "`, `"))
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

	b.WriteString("## Data sources\n\nВыбираются dashboard variables по типу plugin, без UID окружения: `metrics` (Prometheus API Mimir), `logs` (Loki), `traces` (Tempo), `profiles` (Pyroscope).\n\n")

	b.WriteString("## Variables\n\n| Variable | Источник | Назначение |\n| --- | --- | --- |\n")
	b.WriteString("| `environment` | `target_info` → `deployment_environment_name` | Фильтр окружения через join с `target_info`. |\n")
	fmt.Fprintf(&b, "| `service` | список service.name из генератора (`%s`) | `job=~\"nomnom/(${service:pipe})\"`. |\n", strings.Join(o.Services, "`, `"))
	b.WriteString("| `version` | `target_info` → `service_version` | Сравнение версий во время выкатки. |\n")
	b.WriteString("| `service_instance` | `target_info` → `instance` (`service.instance.id`) | Диагностика экземпляра. |\n")
	if o.Temporal != nil {
		fmt.Fprintf(&b, "| `worker` (section, row Temporal) | `%s` | Temporal worker фильтруется отдельно от API. |\n", o.Temporal.Worker)
	}
	b.WriteString("\n`namespace` не добавлен: `k8s.namespace.name` не входит в принятые resource attributes. High-cardinality variables (`order_id`, `user_id`, `payment_id`, `event_id`, `workflow_id`) запрещены — для них traces и logs.\n\n")

	rowsTable(&b, d, "../../../../")
	alertsTable(&b, alerts, "alerts/rules.json", "../../../../")

	b.WriteString("## Переходы\n\n")
	b.WriteString("- **Logs** — Explore Loki по `service_name`, `deployment_environment_name`, `service_version` и интервалу dashboard. Переход log → trace — derived field `trace_id` data source Loki.\n")
	b.WriteString("- **Traces** — Explore Tempo (TraceQL) по сервису и окружению: все, ошибки (`status=error`); панели ошибок и latency содержат data links на ошибочные и медленные traces.\n")
	b.WriteString("- **Profiles** — Explore Pyroscope (CPU) по service/environment/интервалу.")
	if o.Language == Go || o.Language == Python {
		b.WriteString(" Trace → profile (Span Profiles, только CPU) поддерживается runtime и включается в data source Tempo после проверки end-to-end.\n")
	} else {
		b.WriteString(" Span Profiles для этого runtime не поддерживаются: только корреляция service/version/time.\n")
	}
	b.WriteString("- **Exemplars** — выключены: не подтверждено, что SDK histograms, Alloy и Mimir сохраняют exemplars (`inventory.ExemplarsEnabled`).\n")
	b.WriteString("- **Deployments** — annotation по смене `service_version` в `target_info`; появляется, когда `target_info` получает статус «есть».\n")
	if o.Database != "" || o.Kafka != nil {
		b.WriteString("- **Серверные метрики зависимостей** — ссылки на community dashboards в шапке; панели не дублируются.\n")
	}
	return b.String()
}
