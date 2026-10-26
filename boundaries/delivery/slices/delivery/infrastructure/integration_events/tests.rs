use super::*;
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::domains::delivery::{
    DeliveryCancellationReason, DeliveryFailureCode, DeliveryFailureReason, DeliveryId,
    DeliveryOfferId, OrderId,
};

fn ids() -> (DeliveryId, OrderId, CourierId) {
    (
        DeliveryId::new("delivery-1"),
        OrderId::new("order-1"),
        CourierId::new("courier-1"),
    )
}

fn map(event: DomainEvent) -> Option<OutboxRecord> {
    DeliveryIntegrationEvents.map(&OutboxMessage::delivery(event))
}

#[test]
fn maps_courier_assignment() {
    let (delivery_id, order_id, courier_id) = ids();
    let record = map(DomainEvent::CourierAssignedToDelivery {
        delivery_id,
        order_id,
        courier_id,
        created_at_epoch_seconds: Some(1_000),
        expected_pickup_at_epoch_seconds: None,
    })
    .expect("published");
    assert_eq!(record.event_type, COURIER_ASSIGNED_TO_DELIVERY);
    assert_eq!(
        v1::CourierAssignedToDelivery::decode(record.payload.as_slice()).expect("decode"),
        v1::CourierAssignedToDelivery {
            order_id: "order-1".to_owned(),
            delivery_id: "delivery-1".to_owned(),
            courier_id: "courier-1".to_owned(),
            expected_pickup_at: None,
        }
    );
}

/// FR-610, AC-770: expected times named by the courier reach the contract;
/// an unnamed one stays empty (see the tests above).
#[test]
fn maps_expected_times_named_by_the_courier() {
    let (delivery_id, order_id, courier_id) = ids();
    let assigned = map(DomainEvent::CourierAssignedToDelivery {
        delivery_id: delivery_id.clone(),
        order_id: order_id.clone(),
        courier_id: courier_id.clone(),
        created_at_epoch_seconds: Some(1_000),
        expected_pickup_at_epoch_seconds: Some(1_600),
    })
    .expect("published");
    assert_eq!(
        v1::CourierAssignedToDelivery::decode(assigned.payload.as_slice())
            .expect("decode")
            .expected_pickup_at,
        Some(prost_types::Timestamp {
            seconds: 1_600,
            nanos: 0
        })
    );

    let picked = map(DomainEvent::DeliveryPickedUp {
        delivery_id,
        order_id,
        courier_id,
        expected_delivered_at_epoch_seconds: Some(2_400),
    })
    .expect("published");
    assert_eq!(
        v1::DeliveryPickedUp::decode(picked.payload.as_slice())
            .expect("decode")
            .expected_delivered_at,
        Some(prost_types::Timestamp {
            seconds: 2_400,
            nanos: 0
        })
    );
}

#[test]
fn maps_pickup_and_completion() {
    let (delivery_id, order_id, courier_id) = ids();
    let picked = map(DomainEvent::DeliveryPickedUp {
        delivery_id: delivery_id.clone(),
        order_id: order_id.clone(),
        courier_id: courier_id.clone(),
        expected_delivered_at_epoch_seconds: None,
    })
    .expect("published");
    assert_eq!(picked.event_type, DELIVERY_PICKED_UP);
    assert_eq!(
        v1::DeliveryPickedUp::decode(picked.payload.as_slice()).expect("decode"),
        v1::DeliveryPickedUp {
            order_id: "order-1".to_owned(),
            delivery_id: "delivery-1".to_owned(),
            expected_delivered_at: None,
        }
    );

    let completed = map(DomainEvent::DeliveryCompleted {
        delivery_id,
        order_id,
        courier_id,
        created_at_epoch_seconds: Some(1_000),
    })
    .expect("published");
    assert_eq!(completed.event_type, DELIVERY_COMPLETED);
    assert_eq!(
        v1::DeliveryCompleted::decode(completed.payload.as_slice()).expect("decode"),
        v1::DeliveryCompleted {
            order_id: "order-1".to_owned(),
            delivery_id: "delivery-1".to_owned(),
        }
    );
}

#[test]
fn maps_failure_with_reason_code_and_detail() {
    let failed = |reason: DeliveryFailureReason| {
        let (delivery_id, order_id, courier_id) = ids();
        let record = map(DomainEvent::DeliveryFailed {
            delivery_id,
            order_id,
            courier_id,
            reason,
        })
        .expect("published");
        assert_eq!(record.event_type, DELIVERY_FAILED);
        v1::DeliveryFailed::decode(record.payload.as_slice()).expect("decode")
    };
    assert_eq!(
        failed(
            DeliveryFailureReason::new("customer_unreachable", Some("rang twice")).expect("reason")
        ),
        v1::DeliveryFailed {
            order_id: "order-1".to_owned(),
            delivery_id: "delivery-1".to_owned(),
            reason: "customer_unreachable".to_owned(),
            detail: "rang twice".to_owned(),
        }
    );
    // Without a detail the field is empty.
    let without_detail = failed(DeliveryFailureReason::of(DeliveryFailureCode::NoAccess));
    assert_eq!(without_detail.reason, "no_access");
    assert_eq!(without_detail.detail, "");
}

#[test]
fn offer_facts_are_not_published() {
    let (delivery_id, _, courier_id) = ids();
    let offer_id = DeliveryOfferId::new("offer-1");
    for event in [
        DomainEvent::DeliveryOffered {
            delivery_id: delivery_id.clone(),
            offer_id: offer_id.clone(),
            courier_id: courier_id.clone(),
            expires_at_epoch_seconds: 1_060,
        },
        DomainEvent::DeliveryOfferAccepted {
            delivery_id: delivery_id.clone(),
            offer_id: offer_id.clone(),
            courier_id: courier_id.clone(),
        },
        DomainEvent::DeliveryOfferRejected {
            delivery_id: delivery_id.clone(),
            offer_id: offer_id.clone(),
            courier_id: courier_id.clone(),
        },
        DomainEvent::DeliveryOfferExpired {
            delivery_id: delivery_id.clone(),
            offer_id: offer_id.clone(),
            courier_id: courier_id.clone(),
        },
    ] {
        assert!(map(event.clone()).is_none(), "{event:?}");
    }
}

#[test]
fn maps_courier_unassignment() {
    let (delivery_id, order_id, courier_id) = ids();
    let record = map(DomainEvent::CourierUnassignedFromDelivery {
        delivery_id,
        order_id,
        courier_id,
    })
    .expect("published");
    assert_eq!(record.event_type, COURIER_UNASSIGNED_FROM_DELIVERY);
    assert_eq!(
        v1::CourierUnassignedFromDelivery::decode(record.payload.as_slice()).expect("decode"),
        v1::CourierUnassignedFromDelivery {
            order_id: "order-1".to_owned(),
            delivery_id: "delivery-1".to_owned(),
            courier_id: "courier-1".to_owned(),
        }
    );
}

#[test]
fn maps_cancellation_with_and_without_reason() {
    let (delivery_id, order_id, courier_id) = ids();
    let record = map(DomainEvent::DeliveryCancelled {
        delivery_id: delivery_id.clone(),
        order_id: order_id.clone(),
        released_courier_id: Some(courier_id),
        reason: DeliveryCancellationReason::new("customer request"),
    })
    .expect("published");
    assert_eq!(record.event_type, DELIVERY_CANCELLED);
    assert_eq!(
        v1::DeliveryCancelled::decode(record.payload.as_slice()).expect("decode"),
        v1::DeliveryCancelled {
            order_id: "order-1".to_owned(),
            delivery_id: "delivery-1".to_owned(),
            reason: "customer request".to_owned(),
        }
    );

    let without_reason = map(DomainEvent::DeliveryCancelled {
        delivery_id,
        order_id,
        released_courier_id: None,
        reason: None,
    })
    .expect("published");
    assert_eq!(
        v1::DeliveryCancelled::decode(without_reason.payload.as_slice()).expect("decode"),
        v1::DeliveryCancelled {
            order_id: "order-1".to_owned(),
            delivery_id: "delivery-1".to_owned(),
            reason: String::new(),
        }
    );
}

/// Reference value computed independently (Python `uuid.uuid5`) with the
/// ADR 31 namespace `8f1d3c2a-5b7e-4a90-9c61-2e4f7b8d0a13`.
#[test]
fn event_id_is_uuid_v5_of_envelope_key() {
    let id = crate::infrastructure::repository::event_id(
        "delivery",
        "delivery-1",
        3,
        DELIVERY_PICKED_UP,
    );
    assert_eq!(id.to_string(), "7bab3765-b089-5ab8-8bcd-eaf2ea654b5a");
    assert_eq!(id.get_version_num(), 5);
    // Every envelope component changes the id.
    assert_ne!(
        id,
        crate::infrastructure::repository::event_id(
            "delivery",
            "delivery-1",
            4,
            DELIVERY_PICKED_UP
        )
    );
    assert_ne!(
        id,
        crate::infrastructure::repository::event_id(
            "delivery",
            "delivery-1",
            3,
            DELIVERY_COMPLETED
        )
    );
}

#[test]
fn event_types_are_full_protobuf_names() {
    fn full_name<T>() -> String {
        format!(
            "nomnom.delivery.v1.{}",
            std::any::type_name::<T>()
                .rsplit("::")
                .next()
                .expect("name")
        )
    }
    assert_eq!(
        COURIER_UNASSIGNED_FROM_DELIVERY,
        full_name::<v1::CourierUnassignedFromDelivery>()
    );
    assert_eq!(DELIVERY_CANCELLED, full_name::<v1::DeliveryCancelled>());
    assert_eq!(
        COURIER_ASSIGNED_TO_DELIVERY,
        format!(
            "nomnom.delivery.v1.{}",
            std::any::type_name::<v1::CourierAssignedToDelivery>()
                .rsplit("::")
                .next()
                .expect("name")
        )
    );
}
