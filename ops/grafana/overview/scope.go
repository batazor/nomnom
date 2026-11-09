package overview

import (
	"fmt"
	"slices"
	"strings"

	"nomnom/ops/grafana/inventory"
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
	// AllServices — job из всего списка Services, без фильтра $service, но с
	// фильтром environment: панель описывает процесс, идущий через все роли.
	AllServices bool
	// Literal — job из списка Services без dashboard variables и без join с
	// target_info: так строятся alert rules, у которых variables нет.
	Literal bool
}

func (s Scope) job() string {
	if s.Job != "" {
		return fmt.Sprintf(`job=%q`, s.Job)
	}
	if s.Literal || s.AllServices {
		return fmt.Sprintf(`job=~"nomnom/(%s)"`, strings.Join(s.Services, "|"))
	}
	return fmt.Sprintf(`job=~"nomnom/(${%s:pipe})"`, s.Var)
}

// exemplars — row запрашивает хотя бы один сервис с подтверждёнными exemplars;
// у инфраструктурного scrape exemplars нет.
func (s Scope) exemplars() bool {
	return s.Job == "" && slices.ContainsFunc(s.Services, func(service string) bool {
		return slices.Contains(inventory.Exemplars, service)
	})
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

// Over — increase() за окно SLO. Environment фильтруется по target_info за то же
// окно (max_over_time), чтобы instance, завершившиеся внутри окна, оставались в
// расчёте; version и instance не фильтруются: SLO относится к сервису, а не к выкатке.
func (s Scope) Over(window, metric string, matchers ...string) string {
	return s.overFilter(window, "increase("+s.Sel(metric, matchers...)+"["+window+"])")
}

func (s Scope) overFilter(window, inner string) string {
	if s.Job != "" || s.Literal {
		return inner
	}
	return inner + fmt.Sprintf(` and on (job, instance) max_over_time(target_info{%s, deployment_environment_name=~"$environment"}[%s])`, s.job(), window)
}

// native — histogram процесса или OBI: native histogram без series _bucket/_count/_sum.
func native(histogram string) bool {
	m, ok := inventory.Lookup(histogram)
	return ok && m.Type == inventory.Histogram
}

// Count — частота наблюдений histogram: histogram_count у native, _count у классической.
func (s Scope) Count(histogram string, matchers ...string) string {
	if native(histogram) {
		return s.filter("histogram_count(rate(" + s.Sel(histogram, matchers...) + "[$__rate_interval]))")
	}
	return s.Rate(histogram+"_count", matchers...)
}

// SumOf — скорость роста суммы наблюдений: histogram_sum у native, _sum у классической.
func (s Scope) SumOf(histogram string, matchers ...string) string {
	if native(histogram) {
		return s.filter("histogram_sum(rate(" + s.Sel(histogram, matchers...) + "[$__rate_interval]))")
	}
	return s.Rate(histogram+"_sum", matchers...)
}

// CountOver — число наблюдений histogram за окно SLO с фильтром environment, как Over.
func (s Scope) CountOver(window, histogram string, matchers ...string) string {
	if native(histogram) {
		return s.overFilter(window, "histogram_count(increase("+s.Sel(histogram, matchers...)+"["+window+"]))")
	}
	return s.Over(window, histogram+"_count", matchers...)
}

// Fraction — доля наблюдений native histogram не больше upper за окно SLO. upper —
// степень двойки: при scale ≥ 0 это граница бакета, доля точна без интерполяции (ADR 40).
func (s Scope) Fraction(window, by, upper, histogram string, matchers ...string) string {
	return "histogram_fraction(0, " + upper + ", " + Sum(by, s.Over(window, histogram, matchers...)) + ")"
}

// Grown — точный прирост counter за окно без экстраполяции increase(): последнее
// значение минус значение на начало окна, а у series, появившейся внутри окна, — минус
// ноль. Рестарт даёт новую series (instance — service.instance.id), сбросов внутри
// series нет. Для точных потоков, где экстраполяция даёт этапы больше начала.
func (s Scope) Grown(window, metric string, matchers ...string) string {
	series := s.Sel(metric, matchers...)
	last := fmt.Sprintf("max_over_time(%s[%s])", series, window)
	inner := fmt.Sprintf("(%s - (%s offset %s or 0 * %s))", last, series, window, last)
	if s.Job != "" || s.Literal {
		return inner
	}
	return inner + fmt.Sprintf(` and on (job, instance) max_over_time(target_info{%s, deployment_environment_name=~"$environment"}[%s])`, s.job(), window)
}

// Gauge — значение gauge с фильтром environment/version.
func (s Scope) Gauge(metric string, matchers ...string) string {
	return s.filter(s.Sel(metric, matchers...))
}

// Quantile — histogram_quantile по базовому имени histogram: у native — по самой
// series, у классической — по _bucket с группировкой le.
func (s Scope) Quantile(q, by, histogram string, matchers ...string) string {
	if native(histogram) {
		return fmt.Sprintf("histogram_quantile(%s, %s)", q, Sum(by, s.Rate(histogram, matchers...)))
	}
	group := "le"
	if by != "" {
		group = by + ", le"
	}
	return fmt.Sprintf("histogram_quantile(%s, %s)", q, Sum(group, s.Rate(histogram+"_bucket", matchers...)))
}

// rateBy — запрос rate() по одному label с этим же label в легенде.
func rateBy(s Scope, label, metric string, matchers ...string) Query {
	return Query{Sum(label, s.Rate(metric, matchers...)), "{{" + label + "}}"}
}

// countBy — частота наблюдений histogram по одному label с этим же label в легенде.
func countBy(s Scope, label, histogram string, matchers ...string) Query {
	return Query{Sum(label, s.Count(histogram, matchers...)), "{{" + label + "}}"}
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
