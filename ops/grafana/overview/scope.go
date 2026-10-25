package overview

import (
	"fmt"
	"strings"
)

// Scope — набор service.name, который запрашивает row: variable фильтра job и
// сервисы, по статусу которых в инвентаре решается, жива ли панель.
type Scope struct {
	Var      string
	Services []string
	// Instance включает фильтр dashboard variable service_instance; он относится к
	// $service и не применяется к rows с section variable.
	Instance bool
	// Job — job инфраструктурного Prometheus scrape (infra/kubernetes и т. п.) вместо
	// nomnom/<service.name>; у таких метрик нет target_info, фильтр environment не применяется.
	Job string
	// Matchers добавляются к каждому selector row (namespace и pod сервиса).
	Matchers []string
	// Literal — job из списка Services без dashboard variables и без join с
	// target_info: так строятся alert rules, у которых variables нет.
	Literal bool
}

func (s Scope) job() string {
	if s.Job != "" {
		return fmt.Sprintf(`job=%q`, s.Job)
	}
	if s.Literal {
		return fmt.Sprintf(`job=~"nomnom/(%s)"`, strings.Join(s.Services, "|"))
	}
	return fmt.Sprintf(`job=~"nomnom/(${%s:pipe})"`, s.Var)
}

// Sel строит selector с фильтром service и instance.
func (s Scope) Sel(metric string, matchers ...string) string {
	ms := []string{s.job()}
	if s.Instance {
		ms = append(ms, `instance=~"$service_instance"`)
	}
	ms = append(ms, s.Matchers...)
	ms = append(ms, matchers...)
	return metric + "{" + strings.Join(ms, ", ") + "}"
}

// Increase — increase() серии за $__rate_interval (рестарты и т. п.).
func (s Scope) Increase(metric string, matchers ...string) string {
	return s.filter("increase(" + s.Sel(metric, matchers...) + "[$__rate_interval])")
}

// Env — фильтр environment и version через target_info: resource attributes не
// promoted в labels метрик (docs/observability.md).
func (s Scope) Env() string {
	return fmt.Sprintf(`target_info{%s, deployment_environment_name=~"$environment", service_version=~"$version"}`, s.job())
}

func (s Scope) filter(inner string) string {
	if s.Job != "" || s.Literal {
		return inner
	}
	return inner + " and on (job, instance) " + s.Env()
}

// Rate — rate() серии с фильтром environment/version.
func (s Scope) Rate(metric string, matchers ...string) string {
	return s.filter("rate(" + s.Sel(metric, matchers...) + "[$__rate_interval])")
}

// Gauge — значение gauge с фильтром environment/version.
func (s Scope) Gauge(metric string, matchers ...string) string {
	return s.filter(s.Sel(metric, matchers...))
}

// Quantile — histogram_quantile по базовому имени histogram.
func (s Scope) Quantile(q, by, histogram string, matchers ...string) string {
	group := "le"
	if by != "" {
		group = by + ", le"
	}
	return fmt.Sprintf("histogram_quantile(%s, %s)", q, Sum(group, s.Rate(histogram+"_bucket", matchers...)))
}

// Sum агрегирует выражение; пустой by — sum без группировки.
func Sum(by, inner string) string {
	if by == "" {
		return "sum(" + inner + ")"
	}
	return "sum by (" + by + ") (" + inner + ")"
}

func Max(by, inner string) string {
	if by == "" {
		return "max(" + inner + ")"
	}
	return "max by (" + by + ") (" + inner + ")"
}

func Min(by, inner string) string {
	if by == "" {
		return "min(" + inner + ")"
	}
	return "min by (" + by + ") (" + inner + ")"
}

// Percentiles — p50/p95/p99 одной histogram.
func (s Scope) Percentiles(prefix, by, histogram string, matchers ...string) []Query {
	var qs []Query
	for _, p := range []struct{ q, name string }{{"0.5", "p50"}, {"0.95", "p95"}, {"0.99", "p99"}} {
		legend := prefix + p.name
		if by != "" {
			legend += " {{" + strings.Split(by, ",")[0] + "}}"
		}
		qs = append(qs, Query{Expr: s.Quantile(p.q, by, histogram, matchers...), Legend: legend})
	}
	return qs
}
