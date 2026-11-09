package overview

import (
	"encoding/json/v2"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/cog"
	dashboard "github.com/grafana/grafana-foundation-sdk/go/dashboardv2"
	"github.com/grafana/grafana-foundation-sdk/go/resource"

	"nomnom/ops/grafana/inventory"
)

// Data sources выбираются variables по типу plugin: dashboard не содержит UID
// конкретного окружения и работает с любым provisioning. Это общие (cross-dashboard)
// variables уровня организации: определение одно на все dashboards nomnom, dashboard
// подключает их annotation useCrossDashboardVariables.
// Панели запрашивают только metrics; logs, traces и profiles нужны лишь ссылкам Explore
// (About, data links серий), поэтому они в меню Controls, а не в шапке: Grafana сортирует
// общие variables по имени, и иначе metrics стоял бы между logs и profiles.
var datasources = []struct {
	name, label, plugin string
	hide                dashboard.VariableHide
}{
	{"metrics", "Metrics (Prometheus)", "prometheus", dashboard.VariableHideDontHide},
	{"logs", "Logs (Loki)", "loki", dashboard.VariableHideInControlsMenu},
	{"traces", "Traces (Tempo)", "tempo", dashboard.VariableHideInControlsMenu},
	{"profiles", "Profiles (Pyroscope)", "grafana-pyroscope-datasource", dashboard.VariableHideInControlsMenu},
}

// appDatasources исключает служебные data sources Grafana Cloud того же типа: первым по
// алфавиту среди Loki идёт alert-state-history, и логи сервиса уходили бы в него.
const appDatasources = "/^(?!.*(alert-state-history|usage)).*$/"

// GlobalVariablesDir — артефакты общих variables: вне каталогов Git Sync, применяются до
// dashboards (make grafana-cloud-push).
const GlobalVariablesDir = "ops/grafana/variables"

// GlobalVariable — ресурс Variable (dashboard.grafana.app, public preview Grafana 13.3,
// feature toggle grafana.dashboardGlobalVariables) в global scope: без annotation folder.
// metadata.name равен имени variable: другое имя Grafana отклоняет.
// Folder scope не подходит: Git Sync раскладывает dashboards по folder сервиса.
type GlobalVariable struct {
	Name     string
	Manifest resource.Manifest
}

const variableAPIVersion = "dashboard.grafana.app/v2beta1"

func GlobalVariables() ([]GlobalVariable, error) {
	var out []GlobalVariable
	for _, ds := range datasources {
		v, err := dashboard.NewDatasourceVariableBuilder(ds.name).Label(ds.label).PluginId(ds.plugin).
			Regex(appDatasources).Refresh(dashboard.VariableRefreshOnDashboardLoad).Hide(ds.hide).Build()
		if err != nil {
			return nil, fmt.Errorf("variable %s: %w", ds.name, err)
		}
		out = append(out, GlobalVariable{Name: ds.name, Manifest: resource.Manifest{
			ApiVersion: variableAPIVersion, Kind: "Variable",
			Metadata: resource.Metadata{Name: ds.name},
			Spec:     dashboard.VariableKind{DatasourceVariableKind: &v},
		}})
	}
	return out, nil
}

// crossDashboard — annotation dashboard со списком общих variables: без неё dashboard
// не видит ни одной, перечисленные подключаются явно (новые сами не появляются).
func crossDashboard(names ...string) map[string]string {
	raw, _ := json.Marshal(map[string][]string{"global": names})
	return map[string]string{"grafana.app/useCrossDashboardVariables": string(raw)}
}

// datasourceNames — все общие data source variables: их подключает overview сервиса.
func datasourceNames() []string {
	var names []string
	for _, ds := range datasources {
		names = append(names, ds.name)
	}
	return names
}

// sectionFilter — Filter row (section variable): ad hoc filter Prometheus. Grafana добавляет
// фильтр в каждый selector запросов row, поэтому ключ должен быть у всех метрик row (по
// инвентарю), иначе генерация падает. Поэтому фильтра нет в rows с join target_info или с
// разными семействами метрик (Kafka): он опустошил бы соседние панели. Group by выключен:
// Grafana 13.3 показывает его, но в запросы Prometheus не подставляет (sum by не меняется).
func (g *generator) sectionFilter(r row) dashboard.VariableKind {
	for _, p := range r.panels {
		for _, q := range p.Queries {
			a, err := Analyze(q.Expr)
			if err != nil {
				g.errs = append(g.errs, fmt.Sprintf("%s: %v", p.Title, err))
				continue
			}
			for _, s := range a.Selectors {
				m, ok := inventory.Lookup(s.Series)
				for _, k := range r.filter {
					if ok && !slices.Contains(m.Labels, k) {
						g.errs = append(g.errs, fmt.Sprintf("row %s: filter %s, у метрики %s нет label %s", r.title, k, m.Name, k))
					}
				}
			}
		}
	}
	var keys []cog.Builder[dashboard.MetricFindValue]
	for _, k := range r.filter {
		keys = append(keys, dashboard.NewMetricFindValueBuilder().Text(k))
	}
	v, err := dashboard.NewAdhocVariableBuilder(sectionFilterName(r.title)).Label("Filter").Group("prometheus").
		Datasource(dashboard.NewDashboardv2AdhocVariableKindDatasourceBuilder().Name("${metrics}")).
		DefaultKeys(keys).AllowCustomValue(false).EnableGroupBy(false).Build()
	g.record(err)
	return dashboard.VariableKind{AdhocVariableKind: &v}
}

// sectionFilterName — имя variable из заголовка row: Pods → pods_filter.
func sectionFilterName(title string) string {
	return strings.ToLower(strings.ReplaceAll(title, " ", "_")) + "_filter"
}

func (g *generator) variables() []dashboard.VariableKind {
	jobs := `job=~"nomnom/(` + strings.Join(g.opts.Services, "|") + `)"`
	return []dashboard.VariableKind{
		g.queryVariable("environment", "Environment", `label_values(target_info{`+jobs+`}, deployment_environment_name)`, false),
		customVariable("service", "Service", g.opts.Services, true),
		g.queryVariable("version", "Version", `label_values(target_info{job=~"nomnom/(${service:pipe})", deployment_environment_name=~"$environment"}, service_version)`, true),
		g.queryVariable("service_instance", "Instance", `label_values(target_info{job=~"nomnom/(${service:pipe})", deployment_environment_name=~"$environment", service_version=~"$version"}, instance)`, true),
	}
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
	raw, _ := json.Marshal(panes, json.Deterministic(true))
	tokens := map[string]string{}
	escaped := grafanaVariable.ReplaceAllStringFunc(string(raw), func(v string) string {
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
