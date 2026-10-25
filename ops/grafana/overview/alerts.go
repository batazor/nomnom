package overview

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/grafana/grafana-foundation-sdk/go/alerting"
	"github.com/grafana/grafana-foundation-sdk/go/cog"
	"github.com/grafana/grafana-foundation-sdk/go/expr"
	"github.com/grafana/grafana-foundation-sdk/go/prometheus"
)

// MetricsDatasource — placeholder datasource UID в артефактах alert rules: UID
// окружения в репозитории не хранится, push-скрипт подставляет UID Prometheus/Mimir.
const MetricsDatasource = "${metrics}"

// AlertFolder — folder alert rules и dashboards в Grafana.
const AlertFolder = "nomnom"

type Severity string

const (
	// Critical — пользовательский путь сломан, нужна реакция сейчас.
	Critical Severity = "critical"
	// Warning — деградация или риск, разбирается в рабочее время.
	Warning Severity = "warning"
)

// Alert — правило на значении PromQL: срабатывает, когда Value Op Threshold
// держится For. Пороги — стартовые значения тестового окружения; burn rate
// alerts по SLO — slo.go (ADR 40).
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
		slices.Sort(missing)
		missing = slices.Compact(missing)
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
