package di

import (
	"nomnom/orders/pkg/telemetry"
	"nomnom/orders/pkg/unitofwork"
	"nomnom/orders/slices/order/applications/delivery/recordcourierassignment"
	"nomnom/orders/slices/order/applications/delivery/recordcourierrelease"
	"nomnom/orders/slices/order/applications/delivery/recorddeliverycompletion"
	"nomnom/orders/slices/order/applications/delivery/recorddeliveryfailure"
	"nomnom/orders/slices/order/applications/delivery/recordpickup"
	"nomnom/orders/slices/order/applications/fulfillment"
	"nomnom/orders/slices/order/applications/kitchen/confirmorder"
	"nomnom/orders/slices/order/applications/kitchen/recordorderready"
	"nomnom/orders/slices/order/applications/kitchen/recordpreparationdelay"
	"nomnom/orders/slices/order/applications/kitchen/recordpreparationstarted"
	"nomnom/orders/slices/order/applications/kitchen/rejectorder"
	"nomnom/orders/slices/order/applications/kitchen/relaypreparationissue"
	"nomnom/orders/slices/order/applications/placement/startfulfillment"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
	ordermessaging "nomnom/orders/slices/order/infrastructure/messaging"
	"nomnom/orders/slices/order/infrastructure/observability"
)

// InboxStore — роль хранилища handlers orders-consumer: Unit of Work,
// Transactional Inbox, repository, Courier Assignment, RelationshipsChanged и
// чтение субъектов и revision для Sync Order Relationships.
type InboxStore interface {
	unitofwork.UnitOfWork
	unitofwork.Inbox
	order.Repository
	order.CourierAssignments
	syncorderrelationships.Changes
	syncorderrelationships.Reader
	syncorderrelationships.Revisions
}

// ConsumerWorkflow — Order Fulfillment Workflow для handlers orders-consumer:
// Start Fulfillment запускает его по OrderPlaced, остальные передают факты.
type ConsumerWorkflow interface {
	fulfillment.Starter
	fulfillment.Notifier
}

// Consumer — порты handlers orders-consumer; Wire заполняет все поля
// (wire.Struct). Notifier workflow сюда не входит: его Temporal client с
// metrics handler процесса открывает KafkaConsumer.Start, поэтому handlers
// собирает Handlers уже после подключения.
type Consumer struct {
	Store         InboxStore
	Relationships RelationshipSync
	Telemetry     *telemetry.UseCases
}

// Handlers собирает application handlers, которые читают Kafka, под
// декоратором телеметрии use cases. Record Courier Assignment и Record Courier
// Release записывают RelationshipsChanged в своём Inbox unit of work, Sync Order
// Relationships по нему пишет связи в SpiceDB.
func (c Consumer) Handlers(workflow ConsumerWorkflow) ordermessaging.Handlers {
	store, relationships, uc := c.Store, c.Relationships, c.Telemetry
	return ordermessaging.Handlers{
		ConfirmOrder:             telemetry.Instrument(uc, observability.ConfirmOrder, confirmorder.NewHandler(store, store, store, workflow).Handle),
		RejectOrder:              telemetry.Instrument(uc, observability.RejectOrder, rejectorder.NewHandler(store, store, store, workflow).Handle),
		RecordPreparationStarted: telemetry.Instrument(uc, observability.RecordPreparationStarted, recordpreparationstarted.NewHandler(store, store, store, workflow).Handle),
		RecordOrderReady:         telemetry.Instrument(uc, observability.RecordOrderReady, recordorderready.NewHandler(store, store, store, workflow).Handle),
		RecordPreparationDelay:   telemetry.Instrument(uc, observability.RecordPreparationDelay, recordpreparationdelay.NewHandler(store, store, store, workflow).Handle),
		RelayPreparationIssue:    telemetry.Instrument(uc, observability.RelayPreparationIssue, relaypreparationissue.NewHandler(workflow).Handle),
		RecordCourierAssignment:  telemetry.Instrument(uc, observability.RecordCourierAssignment, recordcourierassignment.NewHandler(store, store, store, workflow, store, store).Handle),
		RecordCourierRelease:     telemetry.Instrument(uc, observability.RecordCourierRelease, recordcourierrelease.NewHandler(store, store, store, store).Handle),
		RecordPickup:             telemetry.Instrument(uc, observability.RecordPickup, recordpickup.NewHandler(store, store, store, workflow).Handle),
		RecordDeliveryCompletion: telemetry.Instrument(uc, observability.RecordDeliveryCompletion, recorddeliverycompletion.NewHandler(store, store, store, workflow).Handle),
		RecordDeliveryFailure:    telemetry.Instrument(uc, observability.RecordDeliveryFailure, recorddeliveryfailure.NewHandler(store, store, store, workflow).Handle),
		SyncOrderRelationships:   telemetry.Instrument(uc, observability.SyncOrderRelationships, syncorderrelationships.NewHandler(store, store, relationships, store).Handle),
		StartFulfillment:         telemetry.Instrument(uc, observability.StartFulfillment, startfulfillment.NewHandler(store, store, store, workflow).Handle),
	}
}
