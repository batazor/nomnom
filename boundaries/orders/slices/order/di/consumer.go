package di

import (
	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/slices/order/applications/confirmorder"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/recordcourierassignment"
	"nomnom/orders/slices/order/applications/recordcourierrelease"
	"nomnom/orders/slices/order/applications/recorddeliverycompletion"
	"nomnom/orders/slices/order/applications/recorddeliveryfailure"
	"nomnom/orders/slices/order/applications/recordorderready"
	"nomnom/orders/slices/order/applications/recordpickup"
	"nomnom/orders/slices/order/applications/recordpreparationdelay"
	"nomnom/orders/slices/order/applications/recordpreparationstarted"
	"nomnom/orders/slices/order/applications/rejectorder"
	"nomnom/orders/slices/order/applications/relaypreparationissue"
	"nomnom/orders/slices/order/domains/order"
	ordermessaging "nomnom/orders/slices/order/infrastructure/messaging"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// InboxStore — роль хранилища handlers orders-consumer: Unit of Work,
// Transactional Inbox, repository и Courier Assignment.
type InboxStore interface {
	application.UnitOfWork
	application.Inbox
	order.Repository
	order.CourierAssignments
}

// Consumer — порты handlers orders-consumer; Wire заполняет все поля
// (wire.Struct). Notifier workflow сюда не входит: его Temporal client с
// metrics handler процесса открывает KafkaConsumer.Start, поэтому handlers
// собирает Handlers уже после подключения.
type Consumer struct {
	Store         InboxStore
	Relationships CourierRelationships
	Telemetry     *telemetry.UseCases
}

// Handlers собирает application handlers, которые читают Kafka, под
// декоратором телеметрии use cases. Record Courier Assignment и Record Courier
// Release пишут order#delivery в SpiceDB внутри своего Inbox unit of work.
func (c Consumer) Handlers(workflow fulfillment.Notifier) ordermessaging.Handlers {
	store, relationships, uc := c.Store, c.Relationships, c.Telemetry
	return ordermessaging.Handlers{
		ConfirmOrder:             telemetry.Instrument(uc, observability.ConfirmOrder, confirmorder.NewHandler(store, store, store, workflow).Handle),
		RejectOrder:              telemetry.Instrument(uc, observability.RejectOrder, rejectorder.NewHandler(store, store, store, workflow).Handle),
		RecordPreparationStarted: telemetry.Instrument(uc, observability.RecordPreparationStarted, recordpreparationstarted.NewHandler(store, store, store, workflow).Handle),
		RecordOrderReady:         telemetry.Instrument(uc, observability.RecordOrderReady, recordorderready.NewHandler(store, store, store, workflow).Handle),
		RecordPreparationDelay:   telemetry.Instrument(uc, observability.RecordPreparationDelay, recordpreparationdelay.NewHandler(store, store, store, workflow).Handle),
		RelayPreparationIssue:    telemetry.Instrument(uc, observability.RelayPreparationIssue, relaypreparationissue.NewHandler(workflow).Handle),
		RecordCourierAssignment:  telemetry.Instrument(uc, observability.RecordCourierAssignment, recordcourierassignment.NewHandler(store, store, store, workflow, store, relationships).Handle),
		RecordCourierRelease:     telemetry.Instrument(uc, observability.RecordCourierRelease, recordcourierrelease.NewHandler(store, store, store, relationships).Handle),
		RecordPickup:             telemetry.Instrument(uc, observability.RecordPickup, recordpickup.NewHandler(store, store, store, workflow).Handle),
		RecordDeliveryCompletion: telemetry.Instrument(uc, observability.RecordDeliveryCompletion, recorddeliverycompletion.NewHandler(store, store, store, workflow).Handle),
		RecordDeliveryFailure:    telemetry.Instrument(uc, observability.RecordDeliveryFailure, recorddeliveryfailure.NewHandler(store, store, store, workflow).Handle),
	}
}
