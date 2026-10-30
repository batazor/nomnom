package overview

import (
	"fmt"
	"strings"

	dashboard "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
	"github.com/grafana/grafana-foundation-sdk/go/text"

	"nomnom/ops/grafana/inventory"
)

// aboutHeight — высота панели вкладки About в пикселях: справка не умещается в обычной.
const aboutHeight = 700

// aboutTab — вкладка со сводкой о сервисе и ссылками на соседние инструменты.
func (g *generator) aboutTab(content string) *dashboard.TabBuilder {
	g.n++
	key := fmt.Sprintf("panel-%d", g.n)
	g.dash.Panel(key, dashboard.NewPanelBuilder().Title("About").Description("Сводка о сервисе и ссылки на соседние инструменты.").Id(float64(g.n)).
		Visualization(text.NewVisualizationV2Builder().Mode(text.TextModeMarkdown).Content(content)))
	return dashboard.Tab("About").AutoGridLayout(dashboard.AutoGrid().
		MaxColumnCount(1).
		ColumnWidthMode(dashboard.AutoGridLayoutSpecColumnWidthModeStandard).
		RowHeightMode(dashboard.AutoGridLayoutSpecRowHeightModeCustom).
		RowHeight(aboutHeight).
		Item(dashboard.AutoGridItem(key)))
}

// markdownLinks — раздел ссылок; пары title и URL идут подряд.
func markdownLinks(heading string, pairs ...string) string {
	var b strings.Builder
	b.WriteString("## " + heading + "\n\n")
	for i := 0; i < len(pairs); i += 2 {
		fmt.Fprintf(&b, "- [%s](%s)\n", pairs[i], pairs[i+1])
	}
	return b.String() + "\n"
}

func codeList(values []string) string { return "`" + strings.Join(values, "`, `") + "`" }

// serviceAbout — состав сервиса из Options и ссылки: логи, трейсы и профили с текущими
// переменными, документация и серверные dashboards зависимостей.
func (g *generator) serviceAbout() string {
	o := g.opts
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n## Сервис\n\n| | |\n| --- | --- |\n", o.Title)
	fmt.Fprintf(&b, "| Процессы | %s |\n", codeList(o.Services))
	fmt.Fprintf(&b, "| Язык | `%s` |\n", o.Language)
	if o.Database != "" {
		fmt.Fprintf(&b, "| База данных | `%s` |\n", o.Database)
	}
	if o.Kafka != nil && o.Kafka.ConsumerGroup != "" {
		fmt.Fprintf(&b, "| Consumer group | `%s` |\n", o.Kafka.ConsumerGroup)
	}
	if o.Kafka != nil && len(o.Kafka.DeadLetters) > 0 {
		fmt.Fprintf(&b, "| DLQ | %s |\n", codeList(o.Kafka.DeadLetters))
	}
	if o.Temporal != nil {
		fmt.Fprintf(&b, "| Temporal worker | `%s` |\n", o.Temporal.Worker)
	}
	b.WriteString("\n")

	main := Scope{Var: "service"}
	b.WriteString(markdownLinks("Сигналы",
		"Logs (Loki)", logsURL(),
		"Traces (Tempo)", tracesURL(main, ""),
		"Traces: errors", tracesURL(main, " && status=error"),
		"Profiles (Pyroscope)", profilesURL(),
	))
	b.WriteString(markdownLinks("Документация",
		"Dashboard README", inventory.RepoURL+"boundaries/"+o.Context+"/ops/grafana/README.md",
		"Metric inventory", inventory.RepoURL+inventory.DocPath,
	))

	related := []string{"Platform / Overview", "/d/" + PlatformUID}
	var deps []string
	if o.Database != "" {
		deps = append(deps, string(o.Database))
	}
	if o.Kafka != nil {
		deps = append(deps, "kafka")
		if o.Kafka.Watermill {
			deps = append(deps, "watermill")
		}
	}
	for _, d := range deps {
		related = append(related, community[d].title, community[d].url)
	}
	b.WriteString(markdownLinks("Соседние dashboards", related...))
	return b.String()
}

func platformAbout() string {
	return "# Platform\n\nСерверная сторона общей инфраструктуры: SpiceDB, PostgreSQL, CDC, Valkey, pods и сертификаты.\n\n" +
		markdownLinks("Документация",
			"Dashboard README", inventory.RepoURL+"ops/grafana/dashboards/README.md",
			"Metric inventory", inventory.RepoURL+inventory.DocPath,
		)
}
