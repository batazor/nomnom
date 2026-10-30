//! Integration-тесты SpiceDB-адаптера (FR-435, AC-465, AC-466) против SpiceDB
//! из testcontainers, поднятого с `authorization/bootstrap.yaml`.

mod support;

use std::time::Duration;

use delivery::infrastructure::spicedb::SpiceDbAuthorization;
use delivery::pkg::authz::{AuthorizationError, Subject};
use delivery::pkg::config::SpiceDbConfig;
use delivery::slices::courier::applications::authorization::{
    CourierAuthorization, CourierPermission, CourierResource,
};
use delivery::slices::delivery::applications::authorization::{
    DeliveryAuthorization, DeliveryPermission, DeliveryResource,
};
use delivery::slices::delivery::domains::delivery::{DeliveryId, OrderId};

fn authorization(config: &SpiceDbConfig) -> SpiceDbAuthorization {
    SpiceDbAuthorization::from_config(config).expect("valid config")
}

fn live() -> SpiceDbAuthorization {
    authorization(&support::spicedb(Duration::from_secs(2)))
}

fn service(name: &str) -> Subject {
    Subject::Service(name.to_owned())
}

fn delivery(
    authorization: &SpiceDbAuthorization,
    subject: &Subject,
    permission: DeliveryPermission,
    resource: &DeliveryResource,
) -> Result<(), AuthorizationError> {
    DeliveryAuthorization::check(authorization, subject, permission, resource)
}

fn fulfillment_commands() -> [(DeliveryPermission, DeliveryResource); 2] {
    [
        (
            DeliveryPermission::CreateDelivery,
            DeliveryResource::Order(OrderId::new("order-1")),
        ),
        (
            DeliveryPermission::CancelDelivery,
            DeliveryResource::Order(OrderId::new("order-1")),
        ),
    ]
}

#[test]
fn orders_worker_coordinates_delivery() {
    let authorization = live();

    for (permission, resource) in fulfillment_commands() {
        assert_eq!(
            delivery(
                &authorization,
                &service("orders-worker"),
                permission,
                &resource
            ),
            Ok(()),
            "{permission:?}"
        );
    }
}

#[test]
fn other_service_is_denied_fulfillment_commands() {
    let authorization = live();

    for (permission, resource) in fulfillment_commands() {
        assert_eq!(
            delivery(&authorization, &service("bff"), permission, &resource),
            Err(AuthorizationError::Denied),
            "{permission:?}"
        );
    }
}

#[test]
fn orders_worker_cannot_register_courier() {
    let authorization = live();

    assert_eq!(
        CourierAuthorization::check(
            &authorization,
            &service("orders-worker"),
            CourierPermission::ManageCouriers,
            &CourierResource::Platform,
        ),
        Err(AuthorizationError::Denied)
    );
}

#[test]
fn courier_without_relationship_is_denied() {
    let authorization = live();

    assert_eq!(
        delivery(
            &authorization,
            &Subject::User("courier-user-1".to_owned()),
            DeliveryPermission::ConfirmPickup,
            &DeliveryResource::Delivery(DeliveryId::new("delivery-1")),
        ),
        Err(AuthorizationError::Denied)
    );
}

/// AC-466: SpiceDB не отвечает в пределах timeout — authorization unavailable.
#[test]
fn unreachable_spicedb_is_unavailable() {
    let authorization = authorization(&SpiceDbConfig {
        endpoint: "127.0.0.1:1".to_owned(),
        preshared_key: "devkey".to_owned(),
        insecure: true,
        timeout: Duration::from_millis(300),
    });

    let outcome = delivery(
        &authorization,
        &service("orders-worker"),
        DeliveryPermission::CreateDelivery,
        &DeliveryResource::Order(OrderId::new("order-1")),
    );

    assert!(
        matches!(outcome, Err(AuthorizationError::Unavailable(_))),
        "{outcome:?}"
    );
}

/// Синхронный порт вызывается из async handler на multi-thread runtime.
#[tokio::test(flavor = "multi_thread")]
async fn check_from_async_handler_does_not_block_runtime() {
    let authorization = live();

    assert_eq!(
        delivery(
            &authorization,
            &service("orders-worker"),
            DeliveryPermission::CreateDelivery,
            &DeliveryResource::Order(OrderId::new("order-1")),
        ),
        Ok(())
    );
}
