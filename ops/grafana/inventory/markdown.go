package inventory

import (
	"fmt"
	"strings"
)

// Markdown рендерит docs/observability/metric-inventory.md.
func Markdown() string {
	var b strings.Builder
	b.WriteString(`<!-- Сгенерировано ops/grafana (make grafana-generate). Не редактировать вручную: источник — ops/grafana/inventory/inventory.go. -->

# Инвентарь метрик

Инвентарь фиксирует метрики, на которых строятся overview-dashboards сервисов
([docs/observability.md](../observability.md#dashboards)). Панель строится только на метриках
со статусом «есть»; для остальных генератор рисует TODO-панель со ссылкой на раздел этого документа.

| Статус | Значение |
| --- | --- |
| есть | Метрика экспортируется сервисом **и** доставляется в Mimir существующим pipeline. |
| ожидается | Метрика принята архитектурой (semconv, ADR, решение по Watermill), но не экспортируется или не доставляется. |

Имена приведены в виде Mimir после OTLP-трансляции (точки → подчёркивания, unit suffix, ` + "`_total`" + ` для
монотонных counters). Для histogram указано базовое имя; запросы используют ` + "`_bucket`/`_count`/`_sum`" + `.

## Состояние на дату генерации

`)
	present := 0
	for _, m := range Metrics {
		for _, st := range m.Status {
			if st == Present {
				present++
			}
		}
	}
	fmt.Fprintf(&b, "Метрик в инвентаре: %d; пар «метрика × сервис» со статусом «есть»: %d.\n\n", len(Metrics), present)
	b.WriteString(`Ни один backend-сервис пока не подключает OpenTelemetry SDK: OTel-пакеты в ` + "`orders`" + ` и BFF — только
транзитивные зависимости, технический порт ` + "`metrics`" + ` отдаёт health, Helm values содержат ` + "`OTEL_*`" + ` лишь в
комментариях, а Alloy, Mimir и Grafana в репозитории не развёрнуты. Поэтому все панели overview сейчас — TODO.
Встроенные метрики Watermill приняты как базовый слой, но ` + "`PrometheusMetricsBuilder`" + ` в ` + "`orders-consumer`" + ` не
подключён — статус «ожидается» до появления wiring.

`)
	for _, s := range Sections {
		fmt.Fprintf(&b, "<a id=%q></a>\n\n## %s\n\n%s\n\n", s.ID, s.Title, s.Intro)
		b.WriteString("| Метрика | Источник | Тип | Labels | Сервис → статус | Примечание |\n| --- | --- | --- | --- | --- | --- |\n")
		for _, m := range Metrics {
			if m.Section != s.ID {
				continue
			}
			labels := "—"
			if len(m.Labels) > 0 {
				labels = "`" + strings.Join(m.Labels, "`, `") + "`"
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s | %s |\n", m.Name, m.Source, m.Type, labels, statuses(m), orDash(m.Note))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func statuses(m Metric) string {
	var parts []string
	for _, s := range AllServices {
		if st, ok := m.Status[s]; ok {
			parts = append(parts, fmt.Sprintf("`%s`: %s", s, st))
		}
	}
	return strings.Join(parts, "<br>")
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
