package overview

import (
	"fmt"
	"strconv"
)

// SLO — цели сервиса за окно sloWindow (ADR 40). SLI считаются на входе сервиса:
// gRPC server у контекстов, клиентский GraphQL-запрос у BFF.
type SLO struct {
	// Availability — доля входящих вызовов без технической ошибки (transport и
	// application); business rejection — корректный ответ и бюджет не тратит.
	Availability float64
	// Latency — nil, когда цель latency не принята.
	Latency *LatencySLO
}

type LatencySLO struct {
	// Bucket — граница histogram (le) в секундах; совпадает с границей buckets сервиса.
	Bucket string
	// Target — доля вызовов не дольше Bucket.
	Target float64
}

// sloWindow — окно SLO и error budget.
const sloWindow = "30d"

// burn — окно burn rate и порог, при котором за окно тратится заданная доля
// 30-дневного бюджета: 2% за 1h, 5% за 6h, 10% за 3d. Short — подтверждающее окно
// alert: правило гаснет, как только сжигание прекратилось.
type burn struct {
	Long, Short string
	Threshold   float64
	Severity    Severity
	For         string
}

var (
	fastBurn = burn{Long: "1h", Short: "5m", Threshold: 14.4, Severity: Critical, For: "2m"}
	slowBurn = burn{Long: "6h", Short: "30m", Threshold: 6, Severity: Warning, For: "15m"}
	// ticketBurn — только на панели: 10% бюджета за 3d разбираются в рабочее время.
	ticketBurn = burn{Long: "3d", Threshold: 1}
)

// sli — события SLI для окна: доля плохих событий.
type sli struct {
	name   string
	target float64
	bad    func(sc Scope, window, by string) string
}

// slis — SLI сервиса по его входу; пусто, если SLO не задан.
func (o Options) slis() []sli {
	if o.SLO == nil {
		return nil
	}
	var out []sli
	if o.GRPC {
		out = append(out, sli{name: "Availability", target: o.SLO.Availability, bad: func(sc Scope, w, by string) string {
			return Sum(by, sc.Over(w, rpcServer+"_count", codesTechnical)) + " / " + Sum(by, sc.Over(w, rpcServer+"_count"))
		}})
		if l := o.SLO.Latency; l != nil {
			out = append(out, sli{name: "Latency ≤ " + l.Bucket + "s", target: l.Target, bad: func(sc Scope, w, by string) string {
				return "1 - " + Sum(by, sc.Over(w, rpcServer+"_bucket", `le="`+l.Bucket+`"`)) + " / " + Sum(by, sc.Over(w, rpcServer+"_count"))
			}})
		}
	}
	if o.GraphQL {
		out = append(out, sli{name: "Availability", target: o.SLO.Availability, bad: func(sc Scope, w, by string) string {
			return Sum(by, sc.Over(w, "router_http_requests_error_total", clientRequest)) + " / " + Sum(by, sc.Over(w, "router_http_requests_total", clientRequest))
		}})
	}
	return out
}

// number — без хвоста двоичного float (1 − 0.999 = 0.0010000000000000009).
func number(v float64) string { return strconv.FormatFloat(v, 'g', 6, 64) }

func percent(v float64) string { return number(v*100) + "%" }

func budget(target float64) string { return number(1 - target) }

// sloPanels — row SLO: SLI против цели, остаток error budget за sloWindow и burn rate.
func (g *generator) sloPanels(sc Scope) []Panel {
	var panels []Panel
	for _, s := range g.opts.slis() {
		target := number(s.target)
		panels = append(panels,
			Panel{Title: s.name + ": SLI / SLO " + percent(s.target), Unit: "percentunit", Queries: []Query{
				{"1 - (" + s.bad(sc, "$__rate_interval", "") + ")", "SLI"},
				{"vector(" + target + ")", "SLO"},
			}, Description: fmt.Sprintf("Доля хороших событий в шаге графика против цели %s за %s (ADR 40).", percent(s.target), sloWindow)},
			Panel{Title: s.name + ": error budget remaining (" + sloWindow + ")", Unit: "percentunit", Queries: []Query{
				{"1 - (" + s.bad(sc, sloWindow, "") + ") / " + budget(s.target), "remaining"},
			}, Description: fmt.Sprintf("Неизрасходованная доля бюджета %s за скользящие %s. Ниже нуля — SLO нарушен.", budget(s.target), sloWindow)},
			Panel{Title: s.name + ": burn rate", Unit: "short", Queries: []Query{
				{"(" + s.bad(sc, fastBurn.Long, "") + ") / " + budget(s.target), fastBurn.Long},
				{"(" + s.bad(sc, slowBurn.Long, "") + ") / " + budget(s.target), slowBurn.Long},
				{"(" + s.bad(sc, ticketBurn.Long, "") + ") / " + budget(s.target), ticketBurn.Long},
			}, Description: fmt.Sprintf("Скорость расхода бюджета: 1 — бюджет кончится ровно за %s. Пороги: %g за %s (critical), %g за %s (warning), %g за %s.",
				sloWindow, fastBurn.Threshold, fastBurn.Long, slowBurn.Threshold, slowBurn.Long, ticketBurn.Threshold, ticketBurn.Long)},
		)
	}
	return panels
}

// sloAlerts — multiwindow burn rate: длинное окно доказывает расход бюджета,
// короткое — что он продолжается.
func (o Options) sloAlerts(sc Scope, dash string) []Alert {
	var alerts []Alert
	for _, s := range o.slis() {
		slug := "availability"
		if s.name != "Availability" {
			slug = "latency"
		}
		for _, b := range []struct {
			burn
			kind string
		}{{fastBurn, "fast"}, {slowBurn, "slow"}} {
			rate := func(w string) string { return "(" + s.bad(sc, w, "job") + ") / " + budget(s.target) }
			alerts = append(alerts, Alert{
				Slug:  o.Context + "-slo-" + slug + "-" + b.kind,
				Title: fmt.Sprintf("%s: %s SLO burn rate %s", o.Title, s.name, b.Long),
				Summary: fmt.Sprintf("Error budget SLO %s %s расходуется в %g раз быстрее допустимого за %s и %s.",
					s.name, percent(s.target), b.Threshold, b.Long, b.Short),
				Value: rate(b.Long) + " and " + rate(b.Short) + fmt.Sprintf(" > %g", b.Threshold),
				Op:    ">", Threshold: b.Threshold, For: b.For, Severity: b.Severity, DashboardID: dash,
			})
		}
	}
	return alerts
}
