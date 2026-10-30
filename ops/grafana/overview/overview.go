// Package overview строит "<service> / Overview" dashboard одной функцией
// ServiceOverview: состав и порядок rows задаются кодом, а не конвенцией;
// Options только включают rows по реальным зависимостям сервиса.
package overview

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	dashboard "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
	"github.com/grafana/grafana-foundation-sdk/go/prometheus"
	"github.com/grafana/grafana-foundation-sdk/go/resource"
	"github.com/grafana/grafana-foundation-sdk/go/text"
	"github.com/grafana/grafana-foundation-sdk/go/timeseries"

	"nomnom/ops/grafana/inventory"
)

type Language string

const (
	Go     Language = "go"
	Node   Language = "nodejs"
	Python Language = "python"
	Rust   Language = "rust"
	// Cosmo — Go-бинарь Cosmo Router; runtime-метрики не инвентаризированы.
	Cosmo Language = "cosmo"
)

type Database string

const (
	PostgreSQL Database = "postgresql"
	MongoDB    Database = "mongodb"
)

type Options struct {
	// Context — каталог boundaries/<Context>; uid nomnom-<Context>-overview.
	Context string
	Title   string
	// Services — service.name всех deployable roles; первая — API.
	Services []string
	Language Language
	GRPC     bool
	GraphQL  bool
	// Dependencies — только client-side метрики самого сервиса.
	GRPCClients bool
	Database    Database
	// DatabasePool — пул клиента БД экспортирует полный набор db.client.connection.*
	// (pool.go): saturation, pending, timeouts, wait/use/create time и alert rules пула.
	DatabasePool bool
	// Cache — кеш чтения в Valkey (ADR 43, cache.go): row Cache и alert на долю bypass.
	Cache           bool
	PaymentProvider bool
	Kafka           *Kafka
	// UseCases — декоратор application handlers (раздел usecase инвентаря).
	UseCases   bool
	UnitOfWork bool
	Lifecycle  bool
	Temporal   *Temporal
	// Process — панели процесса, идущего через все роли сервиса: row без фильтра $service.
	Process       func(Scope) []Panel
	Kitchen       bool
	Authorization bool
	// Certificates — у контекста есть свои Certificate cert-manager в nomnom-<context>.
	Certificates bool
	Domain       func(Scope) []Panel
	// DomainAlerts — alert rules бизнес-метрик сервиса (scope без variables).
	DomainAlerts func(Scope) []Alert
	// SLO — цели сервиса (ADR 40): row SLO и burn rate alerts.
	SLO *SLO
}

type Kafka struct {
	// Watermill — встроенные метрики Watermill как базовый слой (Go).
	Watermill bool
	// ConsumerGroup и Consumers пусты, когда контекст только публикует Outbox через CDC.
	ConsumerGroup string
	Consumers     []string
	// DeadLetters — <stream>.dlq каждого стрима, который читает ConsumerGroup (ADR 28):
	// неразобранные записи группы на них считает alert kafka-dlq-unresolved.
	DeadLetters []string
	// Connector — коннектор Debezium, который публикует Outbox контекста (ADR 37).
	Connector string
}

// consumes — контекст читает Kafka сам, а не только публикует Outbox.
func (k *Kafka) consumes() bool { return k != nil && len(k.Consumers) > 0 }

type Temporal struct {
	// Worker — service.name worker; section variable row Temporal.
	Worker string
}

type Query struct {
	Expr   string
	Legend string
}

type Panel struct {
	Title       string
	Description string
	Unit        string
	Queries     []Query
	// Sankey — потоки между состояниями (sankey.go): запрос даёт по строке на пару
	// узлов, два label и значение. Требует плагин Business Charts в Grafana.
	Sankey bool
	// Traces — TraceQL-условия spans серий по индексу Queries: data link точки
	// ведёт в Tempo на spans этой серии. "" — у запроса нет ссылки.
	Traces []string
}

// Dashboard — готовый ресурс и сведения о панелях для README.
type Dashboard struct {
	UID      string
	Manifest resource.Manifest
	Rows     []RowSummary
}

type RowSummary struct {
	Title  string
	Panels []PanelSummary
}

type PanelSummary struct {
	Title   string
	Live    bool
	Missing []string
}

type row struct {
	title     string
	scope     Scope
	collapsed bool
	vars      []dashboard.VariableKind
	panels    []Panel
}

type generator struct {
	opts   Options
	dash   *dashboard.DashboardBuilder
	n      int
	errs   []string
	report []RowSummary
}

func UID(context string) string { return "nomnom-" + context + "-overview" }

// ServiceOverview — общая функция всех overview-dashboards.
func ServiceOverview(opts Options) (Dashboard, error) {
	g := &generator{opts: opts}
	uid := UID(opts.Context)
	main := Scope{Var: "service", Services: opts.Services, Instance: true}

	g.dash = dashboard.NewDashboardBuilder(opts.Title + " / Overview").
		Description(fmt.Sprintf("Overview bounded context %s: health, runtime, зависимости и бизнес-поток. Сгенерировано ops/grafana; правки — через генератор.", opts.Context)).
		Tags([]string{"nomnom", opts.Context, "overview", "generated"}).
		Editable(false).
		CursorSync(dashboard.DashboardCursorSyncCrosshair).
		TimeSettings(dashboard.NewTimeSettingsBuilder().From("now-6h").To("now").AutoRefresh("1m").Timezone("browser")).
		Variables(g.variables()).
		Links(g.links()).
		Annotations(g.annotations(main))

	rows := []row{
		{title: "Service health", scope: main, panels: g.health(main)},
		// SLO относится к сервису целиком: без фильтра instance и version.
		{title: "SLO", scope: Scope{Var: "service", Services: opts.Services}, panels: g.sloPanels(Scope{Var: "service", Services: opts.Services})},
		{title: "Runtime", scope: main, collapsed: true, panels: g.runtime(main)},
		{title: "Pods", scope: servicePods(opts), collapsed: true, panels: pods(servicePods(opts))},
		{title: "Dependencies", scope: main, collapsed: true, panels: g.dependencies(main)},
	}
	if opts.Cache {
		rows = append(rows, row{title: "Cache", scope: main, collapsed: true, panels: cachePanels(main)})
	}
	if opts.Kafka != nil {
		rows = append(rows, row{title: "Kafka", scope: main, collapsed: true, panels: g.kafka(main)})
	}
	if opts.UseCases {
		rows = append(rows, row{title: "Use cases", scope: main, collapsed: true, panels: useCases(main)})
	}
	if opts.UnitOfWork {
		rows = append(rows, row{title: "Transactions", scope: main, collapsed: true, panels: transactions(main)})
	}
	if opts.Lifecycle {
		rows = append(rows, row{title: "Domain Lifecycle", scope: main, collapsed: true, panels: lifecycle(main)})
	}
	if opts.Domain != nil {
		rows = append(rows, row{title: "Domain metrics", scope: main, panels: opts.Domain(main)})
	}
	if opts.Process != nil {
		process := Scope{Services: opts.Services, AllServices: true}
		rows = append(rows, row{title: "Flow", scope: process, panels: opts.Process(process)})
	}
	if opts.Temporal != nil {
		worker := Scope{Var: "worker", Services: []string{opts.Temporal.Worker}}
		rows = append(rows, row{
			title: "Temporal", scope: worker, collapsed: true,
			vars:   []dashboard.VariableKind{customVariable("worker", "Temporal worker", []string{opts.Temporal.Worker}, false)},
			panels: temporal(worker, main),
		})
	}
	if opts.Kitchen {
		rows = append(rows, row{title: "Kitchen", scope: main, collapsed: true, panels: kitchen(main)})
	}
	if opts.Authorization {
		rows = append(rows, row{title: "Authorization", scope: main, collapsed: true, panels: authorization(main)})
	}
	if opts.Certificates {
		certs := contextCertificates(opts)
		rows = append(rows, row{title: "Certificates", scope: certs, collapsed: true, panels: certificates(certs)})
	}

	layout := dashboard.Rows()
	for _, r := range rows {
		if len(r.panels) > 0 {
			layout.Row(g.row(r))
		}
	}
	g.dash.RowsLayout(layout)
	if len(g.errs) > 0 {
		return Dashboard{}, fmt.Errorf("%s: %s", uid, strings.Join(g.errs, "; "))
	}

	manifest, err := dashboard.Manifest(uid, g.dash).Build()
	if err != nil {
		return Dashboard{}, err
	}
	return Dashboard{UID: uid, Manifest: manifest, Rows: g.report}, nil
}

func (g *generator) row(r row) *dashboard.RowBuilder {
	grid := dashboard.Grid()
	summary := RowSummary{Title: r.title}
	for i, p := range r.panels {
		g.n++
		key := fmt.Sprintf("panel-%d", g.n)
		panel, ps := g.panel(p, r.scope)
		g.dash.Panel(key, panel.Id(float64(g.n)))
		grid.Item(dashboard.GridItem(key).X(int64(i%3) * 8).Y(int64(i/3) * 8).Width(8).Height(8))
		summary.Panels = append(summary.Panels, ps)
	}
	g.report = append(g.report, summary)
	b := dashboard.Row(r.title).Collapse(r.collapsed).GridLayout(grid)
	if len(r.vars) > 0 {
		b.Variables(r.vars)
	}
	return b
}

// panel рендерит живую панель на готовых запросах или TODO-панель.
func (g *generator) panel(p Panel, sc Scope) (*dashboard.PanelBuilder, PanelSummary) {
	var ready, pending []Query
	var traces []string
	if len(p.Traces) > 0 && len(p.Traces) != len(p.Queries) {
		g.errs = append(g.errs, fmt.Sprintf("%s: %d trace-условий на %d запросов", p.Title, len(p.Traces), len(p.Queries)))
	}
	missing := map[string]bool{}
	for i, q := range p.Queries {
		ok, miss := g.check(p.Title, q, sc)
		if ok {
			ready = append(ready, q)
			if i < len(p.Traces) {
				traces = append(traces, p.Traces[i])
			} else {
				traces = append(traces, "")
			}
		} else {
			pending = append(pending, q)
		}
		for _, m := range miss {
			missing[m] = true
		}
	}
	names := make([]string, 0, len(missing))
	for m := range missing {
		names = append(names, m)
	}
	sort.Strings(names)
	// Константа (цель SLO) без метрик рядом не делает панель живой.
	if !slices.ContainsFunc(ready, hasMetrics) {
		pending = append(pending, ready...)
		ready, traces = nil, nil
	}
	summary := PanelSummary{Title: p.Title, Live: len(ready) > 0, Missing: names}

	if p.Description == "" {
		p.Description = "Метрики: " + strings.Join(metricsOf(p), ", ") + "."
	}
	b := dashboard.NewPanelBuilder().Title(p.Title)
	if len(ready) == 0 {
		return b.Description(p.Description).
			Visualization(text.NewVisualizationV2Builder().Mode(text.TextModeMarkdown).Content(todo(p, sc, names))), summary
	}

	description := p.Description
	if len(pending) > 0 {
		description += "\n\nЧасть запросов ждёт метрик: " + strings.Join(names, ", ") + " (см. " + inventory.DocPath + ")."
	}
	group := dashboard.NewQueryGroupBuilder()
	viz := timeseries.NewVisualizationV2Builder().Unit(p.Unit)
	for i, q := range ready {
		ref := string(rune('A' + i))
		query := prometheus.NewQueryV2Builder().
			Datasource(dashboard.NewDashboardv2DataQueryKindDatasourceBuilder().Name("${metrics}")).
			Expr(q.Expr).LegendFormat(q.Legend).Range(!p.Sankey)
		if p.Sankey {
			query.Instant(true)
		}
		if inventory.ExemplarsEnabled && strings.Contains(q.Expr, "histogram_quantile") {
			query.Exemplar(true)
		}
		group.Target(dashboard.NewTargetBuilder().RefId(ref).Query(query))
		if traces[i] != "" {
			g.checkTrace(p.Title, q, traces[i])
			viz.OverrideByQuery(ref, []dashboard.DynamicConfigValue{{Id: "links", Value: seriesLinks(sc, traces[i])}})
		}
	}
	if p.Sankey {
		return b.Description(description).Data(group).Visualization(sankeyVisualization{}), summary
	}
	return b.Description(description).Data(group).Visualization(viz), summary
}

func hasMetrics(q Query) bool {
	a, err := Analyze(q.Expr)
	return err == nil && len(a.Selectors) > 0
}

// check проверяет запрос против инвентаря: метрики инвентаризированы, labels
// разрешены, high-cardinality labels отсутствуют. Возвращает готовность и
// метрики без статуса «есть» в scope.
func (g *generator) check(panel string, q Query, sc Scope) (bool, []string) {
	a, err := Analyze(q.Expr)
	if err != nil {
		g.errs = append(g.errs, fmt.Sprintf("%s: %v", panel, err))
		return false, nil
	}
	ready := true
	var missing []string
	allowed := slices.Clone(inventory.ResourceLabels)
	for _, s := range a.Selectors {
		m, ok := inventory.Lookup(s.Series)
		if !ok {
			g.errs = append(g.errs, fmt.Sprintf("%s: метрика %s отсутствует в инвентаре", panel, s.Series))
			continue
		}
		permitted := append(slices.Clone(inventory.ResourceLabels), m.Labels...)
		allowed = append(allowed, m.Labels...)
		for _, l := range s.Labels {
			if !slices.Contains(permitted, l) {
				g.errs = append(g.errs, fmt.Sprintf("%s: label %s не заявлен для %s", panel, l, m.Name))
			}
		}
		services := sc.Services
		if m.Name == "kafka_consumergroup_lag" && g.opts.Kafka != nil {
			services = g.opts.Kafka.Consumers
		}
		// Outbox сервиса публикует Kafka Connect (ADR 37): метрики CDC — его, не сервиса.
		if m.Section == "cdc" {
			services = []string{inventory.KafkaConnect}
		}
		if !inventory.Ready(m.Name, services) {
			ready = false
			missing = append(missing, m.Name)
		}
	}
	for _, l := range append(a.Grouping, a.Labels()...) {
		if slices.Contains(inventory.ForbiddenLabels, l) {
			g.errs = append(g.errs, fmt.Sprintf("%s: high-cardinality label %s", panel, l))
		}
	}
	for _, l := range a.Grouping {
		if !slices.Contains(allowed, l) {
			g.errs = append(g.errs, fmt.Sprintf("%s: группировка по незаявленному label %s", panel, l))
		}
	}
	return ready, missing
}

func todo(p Panel, sc Scope, missing []string) string {
	var b strings.Builder
	b.WriteString("**TODO** — панель станет живой, когда метрики получат статус «есть» в [инвентаре](" + inventory.RepoURL + inventory.DocPath + ").\n\n")
	b.WriteString("| Метрика | Статус для `" + strings.Join(sc.Services, "`, `") + "` |\n| --- | --- |\n")
	for _, name := range missing {
		m, _ := inventory.Lookup(name)
		status := "не заявлена"
		for _, s := range sc.Services {
			if st, ok := m.Status[s]; ok {
				status = string(st)
				break
			}
		}
		fmt.Fprintf(&b, "| [`%s`](%s) | %s |\n", name, inventory.Anchor(m.Section), status)
	}
	b.WriteString("\nЦелевой PromQL:\n\n```promql\n")
	for _, q := range p.Queries {
		b.WriteString(q.Expr + "\n")
	}
	b.WriteString("```\n")
	return b.String()
}

// metricsOf — метрики панели без служебной target_info, для описания по умолчанию.
func metricsOf(p Panel) []string {
	var out []string
	for _, q := range p.Queries {
		a, _ := Analyze(q.Expr)
		for _, sel := range a.Selectors {
			name := "`" + inventory.BaseName(sel.Series) + "`"
			if sel.Series != "target_info" && !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	return out
}
