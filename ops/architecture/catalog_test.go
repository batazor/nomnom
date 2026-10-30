package main

import (
	"slices"
	"testing"
)

func TestParseEventsAssignsMessagesToStreams(t *testing.T) {
	src := `// Integration Events контекста restaurants.

// --- restaurants.v1.restaurant-events (aggregate_type Restaurant) ---

// Ресторан открыт.
message RestaurantOpened {
  string id = 1;
}

// --- restaurants.v1.menu-events ---

message MenuItemAdded {
}
`
	events := map[string][]Event{}
	if err := parseEvents(src, events); err != nil {
		t.Fatal(err)
	}
	if got := events["restaurants.v1.restaurant-events"]; len(got) != 1 || got[0].Name != "RestaurantOpened" || got[0].Comment != "Ресторан открыт." {
		t.Fatalf("restaurant-events: %+v", got)
	}
	if got := events["restaurants.v1.menu-events"]; len(got) != 1 || got[0].Name != "MenuItemAdded" {
		t.Fatalf("menu-events: %+v", got)
	}
}

func TestParseEventsRejectsMessageOutsideStream(t *testing.T) {
	if err := parseEvents("message Orphan {\n}\n", map[string][]Event{}); err == nil {
		t.Fatal("ожидалась ошибка")
	}
}

func TestParseConsumersReadsKafkaEdges(t *testing.T) {
	model := `
  nomnom.cell1.orders.consumer -[kafka]-> nomnom.cell1.messaging.integrationEvents 'order-events, delivery-events'
  nomnom.cell1.delivery.api -[kafka]-> nomnom.cell1.messaging.integrationEvents 'delivery-events'
  nomnom.cell1.router -[kafka]-> nomnom.cell1.messaging.changeNotifications 'order-changes'
  nomnom.cell1.messaging.debezium -[cdc]-> nomnom.cell1.orders.db 'читает Outbox'
`
	got := parseConsumers(model)
	if !slices.Equal(got["delivery-events"], []string{"delivery", "orders-consumer"}) {
		t.Fatalf("delivery-events: %v", got["delivery-events"])
	}
	if !slices.Equal(got["order-changes"], []string{"Cosmo Router"}) {
		t.Fatalf("order-changes: %v", got["order-changes"])
	}
	if _, ok := got["читает Outbox"]; ok {
		t.Fatal("связь cdc не является потреблением")
	}
}
