package overview

import "nomnom/ops/grafana/inventory"

// Services — overview каждого deployable с собственной telemetry. Опции отражают
// реальные зависимости из кода и ADR, а не желаемый одинаковый набор rows.
func Services() []Options {
	return []Options{
		{
			Context: "orders", Title: "Orders", Language: Go,
			Services: []string{inventory.Orders, inventory.OrdersWorker, inventory.OrdersConsumer},
			GRPC:     true, GRPCClients: true, Database: PostgreSQL,
			Kafka:      &Kafka{Watermill: true, ConsumerGroup: "orders", Consumers: []string{inventory.OrdersConsumer}},
			UnitOfWork: true, Lifecycle: true, Authorization: true,
			Temporal: &Temporal{Worker: inventory.OrdersWorker},
			Domain:   ordersDomain,
		},
		{
			Context: "restaurants", Title: "Restaurants", Language: Node,
			Services: []string{inventory.Restaurants},
			GRPC:     true, Database: MongoDB,
			Kafka:      &Kafka{ConsumerGroup: "restaurants", Consumers: []string{inventory.Restaurants}},
			UnitOfWork: true, Lifecycle: true, Kitchen: true, Authorization: true,
			Domain: restaurantsDomain,
		},
		{
			Context: "delivery", Title: "Delivery", Language: Rust,
			Services:   []string{inventory.Delivery},
			GRPC:       true,
			Kafka:      &Kafka{ConsumerGroup: "delivery", Consumers: []string{inventory.Delivery}},
			UnitOfWork: true, Lifecycle: true, Authorization: true,
			Domain: deliveryDomain,
		},
		{
			Context: "payments", Title: "Payments", Language: Python,
			Services: []string{inventory.Payments},
			GRPC:     true, PaymentProvider: true,
			Kafka:      &Kafka{ConsumerGroup: "payments", Consumers: []string{inventory.Payments}},
			UnitOfWork: true, Lifecycle: true, Authorization: true,
			Domain: paymentsDomain,
		},
		{
			Context: "bff", Title: "BFF", Language: Cosmo,
			Services: []string{inventory.BFF},
			GraphQL:  true,
		},
	}
}

func counter(sc Scope, metric, legend string, matchers ...string) Query {
	return Query{Sum("", sc.Rate(metric, matchers...)), legend}
}

func ordersDomain(sc Scope) []Panel {
	return []Panel{
		{Title: "Orders: placed / completed / cancelled / failed", Unit: "ops", Queries: []Query{
			counter(sc, "nomnom_orders_placed_total", "placed"),
			counter(sc, "nomnom_orders_completed_total", "completed"),
			counter(sc, "nomnom_orders_cancelled_total", "cancelled"),
			counter(sc, "nomnom_orders_failed_total", "failed"),
		}},
		{Title: "Cancellation requests", Unit: "ops", Queries: []Query{{Sum("outcome", sc.Rate("nomnom_orders_cancellation_requests_total")), "{{outcome}}"}}},
		{Title: "Fulfillment duration", Unit: "s", Queries: sc.Percentiles("", "", "nomnom_orders_fulfillment_duration_seconds")},
		{Title: "Orders currently in state", Unit: "short", Queries: []Query{{Sum("state", sc.Gauge("nomnom_orders_in_state")), "{{state}}"}},
			Description: "Требует projection/polling; из counters не выводится."},
	}
}

func restaurantsDomain(sc Scope) []Panel {
	return []Panel{
		{Title: "Restaurant orders: received / accepted / rejected", Unit: "ops", Queries: []Query{
			counter(sc, "nomnom_restaurants_orders_received_total", "received"),
			counter(sc, "nomnom_restaurants_orders_accepted_total", "accepted"),
			counter(sc, "nomnom_restaurants_orders_rejected_total", "rejected"),
		}, Description: "Queued / preparing / ready и preparation duration — row Kitchen."},
		{Title: "Rejections by reason", Unit: "ops", Queries: []Query{{Sum("reason", sc.Rate("nomnom_restaurants_orders_rejected_total")), "{{reason}}"}}},
	}
}

func deliveryDomain(sc Scope) []Panel {
	return []Panel{
		{Title: "Delivery flow", Unit: "ops", Queries: []Query{
			counter(sc, "nomnom_delivery_created_total", "created"),
			counter(sc, "nomnom_delivery_assignments_total", "assigned"),
			counter(sc, "nomnom_delivery_reassignments_total", "reassigned"),
			counter(sc, "nomnom_delivery_pickups_total", "picked up"),
			counter(sc, "nomnom_delivery_completed_total", "completed"),
			counter(sc, "nomnom_delivery_failed_total", "failed"),
		}},
		{Title: "Delivery offers", Unit: "ops", Queries: []Query{{Sum("outcome", sc.Rate("nomnom_delivery_offers_total")), "{{outcome}}"}}},
		{Title: "Failures and reassignments by reason", Unit: "ops", Queries: []Query{
			{Sum("reason", sc.Rate("nomnom_delivery_failed_total")), "failed {{reason}}"},
			{Sum("reason", sc.Rate("nomnom_delivery_reassignments_total")), "reassigned {{reason}}"},
		}},
		{Title: "Assignment latency", Unit: "s", Queries: sc.Percentiles("", "", "nomnom_delivery_assignment_latency_seconds")},
		{Title: "Delivery duration", Unit: "s", Queries: sc.Percentiles("", "", "nomnom_delivery_duration_seconds")},
	}
}

func paymentsDomain(sc Scope) []Panel {
	return []Panel{
		{Title: "Authorizations", Unit: "ops", Queries: []Query{{Sum("outcome", sc.Rate("nomnom_payments_authorizations_total")), "{{outcome}}"}},
			Description: "Attempts = сумма outcomes. `declined` (PaymentDeclined) — бизнес-результат, не техническая ошибка."},
		{Title: "Captures and refunds", Unit: "ops", Queries: []Query{
			{Sum("outcome", sc.Rate("nomnom_payments_captures_total")), "capture {{outcome}}"},
			{Sum("outcome", sc.Rate("nomnom_payments_refunds_total")), "refund {{outcome}}"},
		}},
		{Title: "Failed payment operations", Unit: "ops", Traces: true, Queries: []Query{
			counter(sc, "nomnom_payments_authorizations_total", "authorization", `outcome="failed"`),
			counter(sc, "nomnom_payments_captures_total", "capture", `outcome="failed"`),
			counter(sc, "nomnom_payments_refunds_total", "refund", `outcome="failed"`),
		}, Description: "Технические сбои операций; declined сюда не входит. Provider latency — row Dependencies."},
	}
}
