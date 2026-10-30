package overview

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/cog"
	dashboard "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
)

// Data sources выбираются variables по типу plugin: dashboard не содержит UID
// конкретного окружения и работает с любым provisioning.
var datasources = []struct{ name, label, plugin string }{
	{"metrics", "Metrics (Prometheus)", "prometheus"},
	{"logs", "Logs (Loki)", "loki"},
	{"traces", "Traces (Tempo)", "tempo"},
	{"profiles", "Profiles (Pyroscope)", "grafana-pyroscope-datasource"},
}

func (g *generator) variables() []dashboard.VariableKind {
	var vars []dashboard.VariableKind
	for _, ds := range datasources {
		v, err := dashboard.NewDatasourceVariableBuilder(ds.name).Label(ds.label).PluginId(ds.plugin).
			Refresh(dashboard.VariableRefreshOnDashboardLoad).Build()
		g.record(err)
		vars = append(vars, dashboard.VariableKind{DatasourceVariableKind: &v})
	}
	jobs := `job=~"nomnom/(` + strings.Join(g.opts.Services, "|") + `)"`
	vars = append(vars,
		g.queryVariable("environment", "Environment", `label_values(target_info{`+jobs+`}, deployment_environment_name)`, false),
		customVariable("service", "Service", g.opts.Services, true),
		g.queryVariable("version", "Version", `label_values(target_info{job=~"nomnom/(${service:pipe})", deployment_environment_name=~"$environment"}, service_version)`, true),
		g.queryVariable("service_instance", "Instance", `label_values(target_info{job=~"nomnom/(${service:pipe})", deployment_environment_name=~"$environment", service_version=~"$version"}, instance)`, true),
	)
	return vars
}

func (g *generator) queryVariable(name, label, query string, multi bool) dashboard.VariableKind {
	b := dashboard.NewQueryVariableBuilder(name).Label(label).
		Query(rawQuery{group: "prometheus", datasource: "${metrics}", spec: map[string]any{
			"qryType": 1, "query": query, "refId": "PrometheusVariableQueryEditor-VariableQuery",
		}}).
		Definition(query).Refresh(dashboard.VariableRefreshOnTimeRangeChanged).Sort(dashboard.VariableSortAlphabeticalAsc)
	if multi {
		b.Multi(true).IncludeAll(true).AllValue(".*")
	}
	v, err := b.Build()
	g.record(err)
	return dashboard.VariableKind{QueryVariableKind: &v}
}

// customVariable — список service.name из кода; разрешается без запроса к backend.
func customVariable(name, label string, values []string, includeAll bool) dashboard.VariableKind {
	b := dashboard.NewCustomVariableBuilder(name).Label(label).Query(strings.Join(values, ",")).Multi(includeAll).IncludeAll(includeAll)
	var options []dashboard.VariableOption
	for _, v := range values {
		options = append(options, dashboard.VariableOption{Text: dashboard.StringOrArrayOfString{String: cog.ToPtr(v)}, Value: dashboard.StringOrArrayOfString{String: cog.ToPtr(v)}})
	}
	current := options[0]
	if includeAll {
		current = dashboard.VariableOption{Text: dashboard.StringOrArrayOfString{String: cog.ToPtr("All")}, Value: dashboard.StringOrArrayOfString{String: cog.ToPtr("$__all")}}
	}
	v, _ := b.Options(options).Current(current).Build()
	return dashboard.VariableKind{CustomVariableKind: &v}
}

func (g *generator) record(err error) {
	if err != nil {
		g.errs = append(g.errs, err.Error())
	}
}

// rawQuery — DataQuery произвольного plugin (variable query Prometheus, Explore).
type rawQuery struct {
	group, datasource string
	spec              map[string]any
}

func (q rawQuery) Build() (dashboard.DataQueryKind, error) {
	name := q.datasource
	return dashboard.DataQueryKind{Kind: "DataQuery", Group: q.group, Version: "v0",
		Datasource: &dashboard.Dashboardv2DataQueryKindDatasource{Name: &name}, Spec: q.spec}, nil
}

// Explore URL с подставляемыми Grafana variables: JSON кодируется целиком, а
// токены ${...} возвращаются в URL некодированными, чтобы Grafana их интерполировала.
func explore(datasourceVar, plugin string, query map[string]any) string {
	query["refId"] = "A"
	query["datasource"] = map[string]string{"type": plugin, "uid": "${" + datasourceVar + "}"}
	panes := map[string]any{"nomnom": map[string]any{
		"datasource": "${" + datasourceVar + "}",
		"queries":    []any{query},
		"range":      map[string]string{"from": "${__from}", "to": "${__to}"},
	}}
	var raw strings.Builder
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(panes)
	tokens := map[string]string{}
	escaped := grafanaVariable.ReplaceAllStringFunc(strings.TrimSpace(raw.String()), func(v string) string {
		key := fmt.Sprintf("GRAFANAVAR%dX", len(tokens))
		tokens[key] = v
		return key
	})
	u := "/explore?schemaVersion=1&panes=" + url.QueryEscape(escaped)
	for k, v := range tokens {
		u = strings.ReplaceAll(u, k, v)
	}
	return u
}

const serviceSelector = `service_name=~"${service:pipe}", deployment_environment_name=~"$environment"`

func logsURL() string {
	return explore("logs", "loki", map[string]any{
		"expr": `{` + serviceSelector + `} | service_version=~"${version:regex}"`, "queryType": "range",
	})
}

func tracesURL(sc Scope, filter string) string {
	q := fmt.Sprintf(`{resource.service.name=~"${%s:pipe}" && resource.deployment.environment.name=~"$environment"%s}`, sc.Var, filter)
	return explore("traces", "tempo", map[string]any{"queryType": "traceql", "query": q, "limit": 20})
}

func profilesURL() string {
	return explore("profiles", "grafana-pyroscope-datasource", map[string]any{
		"queryType": "profile", "profileTypeId": "process_cpu:cpu:nanoseconds:cpu:nanoseconds",
		"labelSelector": `{` + serviceSelector + `}`,
	})
}

// seriesLinks — data link серии: spans сервиса по условию серии в интервале dashboard.
func seriesLinks(sc Scope, cond string) []dashboard.DataLink {
	return []dashboard.DataLink{{Title: "Traces этой серии", Url: tracesURL(sc, " && "+cond), TargetBlank: cog.ToPtr(true)}}
}

// spanAttributes — span attribute с тем же значением, что label серии: instrumentation
// пишет одни attributes в span операции и в её метрику (rpc, db, use case, provider).
var spanAttributes = map[string]string{
	"rpc_method":               "rpc.method",
	"rpc_response_status_code": "rpc.response.status_code",
	"db_operation_name":        "db.operation.name",
	"error_type":               "error.type",
	"nomnom_usecase_name":      "nomnom.usecase.name",
	"nomnom_outcome":           "nomnom.outcome",
	"nomnom_payment_operation": "nomnom.payment.operation",
}

var seriesLabel = regexp.MustCompile(`\$\{__field\.labels\.(\w+)\}`)

// spanLabel — условие на span attribute из label серии.
func spanLabel(label string) string {
	return fmt.Sprintf(`span.%s="${__field.labels.%s}"`, spanAttributes[label], label)
}

// slower — spans не быстрее значения точки; unit — единица histogram (s, ms).
func slower(unit string) string { return "duration >= ${__value.raw}" + unit }

// traces повторяет условие для каждого запроса панели.
func traces(cond string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = cond
	}
	return out
}

// checkTrace — labels серии в условии есть в группировке запроса и сопоставлены span attribute.
func (g *generator) checkTrace(panel string, q Query, cond string) {
	a, err := Analyze(q.Expr)
	if err != nil {
		g.errs = append(g.errs, fmt.Sprintf("%s: %v", panel, err))
		return
	}
	for _, m := range seriesLabel.FindAllStringSubmatch(cond, -1) {
		if _, ok := spanAttributes[m[1]]; !ok {
			g.errs = append(g.errs, fmt.Sprintf("%s: label %s без span attribute", panel, m[1]))
		}
		if !slices.Contains(a.Grouping, m[1]) {
			g.errs = append(g.errs, fmt.Sprintf("%s: label %s не входит в группировку %q", panel, m[1], q.Expr))
		}
	}
}

// community — серверные dashboards зависимостей: вкладка About ведёт на них, панели не дублируются.
var community = map[string]struct{ title, url string }{
	"postgresql": {"PostgreSQL server (community 9628)", "https://grafana.com/grafana/dashboards/9628"},
	"mongodb":    {"MongoDB server (community 7353)", "https://grafana.com/grafana/dashboards/7353"},
	"kafka":      {"Kafka cluster (community 7589)", "https://grafana.com/grafana/dashboards/7589"},
	"watermill":  {"Watermill panels reference (9777)", "https://grafana.com/grafana/dashboards/9777"},
}

// DeploymentAnnotation — смена service_version в target_info как отметка деплоя.
func DeploymentAnnotation(sc Scope) string {
	selector := fmt.Sprintf(`target_info{job=~"nomnom/(${%s:pipe})", deployment_environment_name=~"$environment"}`, sc.Var)
	return fmt.Sprintf(`count by (job, service_version) (%s) unless count by (job, service_version) (%s offset 5m)`, selector, selector)
}

func (g *generator) annotations(sc Scope) []cog.Builder[dashboard.AnnotationQueryKind] {
	annotations := []cog.Builder[dashboard.AnnotationQueryKind]{
		dashboard.NewAnnotationQueryBuilder().Name("Annotations & Alerts").BuiltIn(true).Enable(true).Hide(true).
			IconColor("rgba(0, 211, 255, 1)").Query(rawQuery{group: "grafana", datasource: "-- Grafana --", spec: map[string]any{}}),
	}
	expr := DeploymentAnnotation(sc)
	if ok, _ := g.check("deployment annotation", Query{Expr: expr}, sc); ok {
		annotations = append(annotations, dashboard.NewAnnotationQueryBuilder().Name("Deployments").Enable(true).IconColor("purple").
			Query(rawQuery{group: "prometheus", datasource: "${metrics}", spec: map[string]any{
				"expr": expr, "refId": "Anno", "step": "1m",
			}}).
			LegacyOptions(map[string]any{"titleFormat": "Deploy {{job}} {{service_version}}", "useValueForTime": false}))
	}
	return annotations
}
