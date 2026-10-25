package inventory

import (
	"fmt"
	"slices"
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
| есть | Метрика экспортируется сервисом по OTLP, а имя и labels подтверждены приёмом через Alloy в Grafana Cloud. |
| ожидается | Метрика принята архитектурой (semconv, ADR, решение по Watermill), но не экспортируется или не доставляется. |

Имена приведены в виде Prometheus после OTLP-трансляции (точки → подчёркивания, unit suffix, ` + "`_total`" + ` для
монотонных counters); Grafana Cloud добавляет suffixes при OTLP ingestion. Метрики Prometheus bridge (Watermill) не меняются. Для histogram указано базовое имя; запросы используют ` + "`_bucket`/`_count`/`_sum`" + `.

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
	b.WriteString("Подтверждение статуса «есть»: " + VerifiedPipeline + "\n\n")
	b.WriteString("Метрики подключены в процессах `orders`: `orders` (gRPC server), `orders-worker` (Temporal, gRPC client\n" +
		"к gateway) и `orders-consumer` (Watermill, OTel middleware), во всех трёх — Go runtime и process CPU через\n" +
		"`orders/pkg/telemetry`. Остальные контексты OpenTelemetry SDK пока не подключают, поэтому их панели — TODO.\n\n")
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
	for _, s := range append(slices.Clone(AllServices), InfraTargets...) {
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
