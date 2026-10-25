package overview

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/alerting"
	"github.com/grafana/grafana-foundation-sdk/go/cog"
	"github.com/grafana/grafana-foundation-sdk/go/expr"
	"github.com/grafana/grafana-foundation-sdk/go/prometheus"

	"nomnom/ops/grafana/inventory"
)

// MetricsDatasource — placeholder datasource UID в артефактах alert rules: UID
// окружения в репозитории не хранится, push-скрипт подставляет UID Prometheus/Mimir.
const MetricsDatasource = "${metrics}"

// AlertFolder — folder alert rules и dashboards в Grafana.
const AlertFolder = "nomnom"

// PlatformAlertsPath — rule group Platform / Overview.
const PlatformAlertsPath = "ops/grafana/alerts/platform.json"

type Severity string

const (
	// Critical — пользовательский путь сломан, нужна реакция сейчас.
	Critical Severity = "critical"
	// Warning — деградация или риск, разбирается в рабочее время.
	Warning Severity = "warning"
)

// Alert — правило на значении PromQL: срабатывает, когда Value Op Threshold
// держится For. Пороги — стартовые значения тестового окружения, не SLO
// (SLO и alerting policy — отдельное решение, docs/observability.md).
type Alert struct {
	Slug        string
	Title       string
	Summary     string
	Value       string
	Op          string
	Threshold   float64
	For         string
	Severity    Severity
	DashboardID string
}

// AlertGroup — rule group одного dashboard (Grafana evaluation group).
type AlertGroup struct {
	Name   string
	Rules  []alerting.Rule
	Report []AlertSummary
}

type AlertSummary struct {
	Title    string
	Severity Severity
	Live     bool
	Missing  []string
}

const alertInterval = 60 // секунды, как экспорт метрик в Grafana Cloud

// alertWindow — окно rate/increase: пять экспортов по 60 s.
const alertWindow = "5m"

var slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// buildGroup оставляет правила только на метриках «есть» (тот же критерий, что
// у живых панелей): правило без данных ничего не защищает.
func buildGroup(name string, sc Scope, alerts []Alert) (AlertGroup, error) {
	g := &generator{}
	group := AlertGroup{Name: name}
	for _, a := range alerts {
		// Окно rate/increase фиксировано: правило не зависит от подстановки variables Grafana.
		a.Value = strings.ReplaceAll(a.Value, "$__rate_interval", alertWindow)
		uid := "nomnom-" + a.Slug
		if !slugPattern.MatchString(a.Slug) || len(uid) > 40 {
			return AlertGroup{}, fmt.Errorf("alert %q: uid %q должен быть [a-z0-9-] и не длиннее 40", a.Title, uid)
		}
		ready, missing := g.check(a.Title, Query{Expr: a.Value}, sc)
		group.Report = append(group.Report, AlertSummary{Title: a.Title, Severity: a.Severity, Live: ready, Missing: missing})
		if !ready {
			continue
		}
		rule, err := alerting.NewRuleBuilder(a.Title).
			Uid(uid).
			FolderUID(AlertFolder).
			RuleGroup(name).
			Condition("C").
			For(a.For).
			NoDataState(alerting.RuleNoDataStateOK).
			ExecErrState(alerting.RuleExecErrStateError).
			Labels(map[string]string{"severity": string(a.Severity), "nomnom_dashboard": a.DashboardID}).
			Annotations(map[string]string{"summary": a.Summary, "dashboard_url": "/d/" + a.DashboardID}).
			Queries([]cog.Builder[alerting.Query]{
				alerting.NewQueryBuilder("A").DatasourceUid(MetricsDatasource).RelativeTimeRange(600, 0).
					Model(prometheus.NewDataqueryBuilder().RefId("A").Expr(a.Value).Instant()),
				alerting.NewQueryBuilder("C").DatasourceUid("__expr__").RelativeTimeRange(0, 0).
					Model(expr.NewExprBuilder().TypeMath(expr.NewTypeMathBuilder().RefId("C").Expression(fmt.Sprintf("$A %s %g", a.Op, a.Threshold)))),
			}).
			Build()
		if err != nil {
			return AlertGroup{}, fmt.Errorf("alert %q: %w", a.Title, err)
		}
		group.Rules = append(group.Rules, rule)
	}
	if len(g.errs) > 0 {
		return AlertGroup{}, fmt.Errorf("%s: %s", name, strings.Join(g.errs, "; "))
	}
	return group, nil
}

// RuleGroup — артефакт provisioning API (PUT /api/v1/provisioning/folder/{uid}/rule-groups/{group}).
func (a AlertGroup) RuleGroup() (alerting.RuleGroup, error) {
	rules := make([]cog.Builder[alerting.Rule], 0, len(a.Rules))
	for _, r := range a.Rules {
		rules = append(rules, ruleValue{r})
	}
	return alerting.NewRuleGroupBuilder(a.Name).FolderUid(AlertFolder).Interval(alertInterval).Rules(rules).Build()
}

type ruleValue struct{ rule alerting.Rule }

func (r ruleValue) Build() (alerting.Rule, error) { return r.rule, nil }

// ServiceAlerts — правила overview сервиса по тем же Options, что и rows.
func ServiceAlerts(o Options) (AlertGroup, error) {
	sc := Scope{Services: o.Services, Literal: true}
	dash := UID(o.Context)
	slug := func(s string) string { return o.Context + "-" + s }
	ratio := func(metric string, matchers ...string) string {
		return Sum("job", sc.Rate(metric, matchers...)) + " / " + Sum("job", sc.Rate(metric))
	}
	var alerts []Alert
	if o.GRPC {
		alerts = append(alerts,
			Alert{Slug: slug("grpc-errors"), Title: o.Title + ": gRPC technical errors", Severity: Critical, For: "10m", Op: ">", Threshold: 0.05, DashboardID: dash,
				Summary: "Больше 5% входящих gRPC-вызовов завершаются техническими кодами (UNAVAILABLE, INTERNAL, DEADLINE_EXCEEDED…).",
				Value:   ratio(rpcServer+"_count", codesTechnical)},
			Alert{Slug: slug("grpc-latency"), Title: o.Title + ": gRPC p95 latency", Severity: Warning, For: "10m", Op: ">", Threshold: 1, DashboardID: dash,
				Summary: "p95 входящих gRPC-вызовов дольше 1 s.",
				Value:   sc.Quantile("0.95", "job", rpcServer)},
		)
	}
	if o.GRPCClients {
		alerts = append(alerts, Alert{Slug: slug("neighbour-errors"), Title: o.Title + ": neighbour calls failing", Severity: Warning, For: "10m", Op: ">", Threshold: 0.05, DashboardID: dash,
			Summary: "Больше 5% вызовов соседних контекстов через internal gateway — технические ошибки.",
			Value:   ratio(rpcClient+"_count", codesTechnical)})
	}
	if o.UseCases {
		alerts = append(alerts, Alert{Slug: slug("usecase-errors"), Title: o.Title + ": use case errors", Severity: Warning, For: "10m", Op: ">", Threshold: 0.05, DashboardID: dash,
			Summary: "Больше 5% вызовов use cases — технические сбои (outcome=error, не бизнес-отказы).",
			Value:   ratio("nomnom_usecase_duration_seconds_count", `nomnom_outcome="error"`)})
	}
	if o.Kafka != nil {
		alerts = append(alerts, Alert{Slug: slug("kafka-poison"), Title: o.Title + ": messages sent to DLQ", Severity: Warning, For: "0s", Op: ">", Threshold: 0, DashboardID: dash,
			Summary: "Integration Events ушли в <stream>.dlq: их обработка не повторяется сама.",
			Value:   Sum("job, messaging_destination_name", sc.Increase("nomnom_messaging_poison_messages_total"))})
	}
	return buildGroup(o.Context, sc, alerts)
}

// PlatformAlerts — SpiceDB, PostgreSQL и pods (Platform / Overview).
func PlatformAlerts() (AlertGroup, error) {
	spicedb := Scope{Services: []string{inventory.SpiceDB}, Job: "infra/spicedb"}
	postgres := Scope{Services: []string{inventory.PostgreSQLServer}, Job: "infra/postgresql"}
	pods := Scope{Services: inventory.AllServices, Job: jobKubernetes}
	alerts := []Alert{
		{Slug: "spicedb-errors", Title: "SpiceDB: errors", Severity: Critical, For: "10m", Op: ">", Threshold: 0.05, DashboardID: PlatformUID,
			Summary: "Больше 5% запросов к SpiceDB не OK: клиенты fail-closed, пользователи получают отказ в доступе.",
			Value:   Sum("", spicedb.Rate("grpc_server_handled_total", spicedbAPI, `grpc_code!="OK"`)) + " / " + Sum("", spicedb.Rate("grpc_server_handled_total", spicedbAPI))},
		{Slug: "spicedb-latency", Title: "SpiceDB: p95 latency", Severity: Warning, For: "10m", Op: ">", Threshold: 0.25, DashboardID: PlatformUID,
			Summary: "p95 запросов к SpiceDB дольше 250 ms: проверки доступа тормозят все use cases.",
			Value:   spicedb.Quantile("0.95", "", "grpc_server_handling_seconds", spicedbAPI)},
		{Slug: "postgres-down", Title: "PostgreSQL: down", Severity: Critical, For: "5m", Op: "<", Threshold: 1, DashboardID: PlatformUID,
			Summary: "Экспортер не подключается к базе владельца: база недоступна или нет прав.",
			Value:   Max("owner", postgres.Gauge("pg_up"))},
		{Slug: "postgres-deadlocks", Title: "PostgreSQL: deadlocks", Severity: Warning, For: "0s", Op: ">", Threshold: 0, DashboardID: PlatformUID,
			Summary: "В базе владельца случились deadlocks.",
			Value:   Sum("owner", postgres.Increase("pg_stat_database_deadlocks"))},
		{Slug: "pod-crashloop", Title: "Pods: crash looping", Severity: Critical, For: "5m", Op: ">", Threshold: 0, DashboardID: PlatformUID,
			Summary: "Контейнер в CrashLoopBackOff или ImagePullBackOff.",
			Value:   Sum("namespace, pod", pods.Gauge("kube_pod_container_status_waiting_reason", `reason=~"CrashLoopBackOff|ImagePullBackOff|ErrImagePull"`))},
		{Slug: "pod-restarts", Title: "Pods: restarting", Severity: Warning, For: "0s", Op: ">", Threshold: 2, DashboardID: PlatformUID,
			Summary: "Контейнер перезапускался больше 2 раз за окно оценки.",
			Value:   Sum("namespace, pod", pods.Increase("kube_pod_container_status_restarts_total"))},
		{Slug: "pod-not-ready", Title: "Pods: not ready", Severity: Warning, For: "10m", Op: "<", Threshold: 1, DashboardID: PlatformUID,
			Summary: "Pod не проходит readinessProbe и выведен из Service.",
			Value:   Max("namespace, pod", pods.Gauge("kube_pod_status_ready", `condition="true"`))},
	}
	return buildGroup("platform", Scope{Services: append([]string{inventory.SpiceDB, inventory.PostgreSQLServer}, inventory.AllServices...)}, alerts)
}
