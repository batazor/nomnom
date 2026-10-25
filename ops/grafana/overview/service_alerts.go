package overview

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
