//! Integration-тесты SpiceDB-адаптера (FR-435, AC-465, AC-466) против SpiceDB,
//! поднятого с `authorization/bootstrap.yaml`. Запускаются только при заданном
//! `SPICEDB_ENDPOINT` (например, `localhost:50051`, ключ `devkey`, plaintext):
//!
//! `SPICEDB_ENDPOINT=localhost:50051 cargo test --test spicedb_authorization`
//!
//! Без переменной тесты завершаются сразу, кроме проверки недоступного
//! endpoint, которой SpiceDB не нужен.

use std::time::Duration;

use delivery::infrastructure::spicedb::SpiceDbAuthorization;
use delivery::pkg::application::{AuthorizationError, Subject};
use delivery::pkg::config::SpiceDbConfig;
use delivery::slices::courier::applications::authorization::{
    CourierAuthorization, CourierPermission, CourierResource,
};
use delivery::slices::delivery::applications::authorization::{
    DeliveryAuthorization, DeliveryPermission, DeliveryResource,
};
use delivery::slices::delivery::domains::delivery::{DeliveryId, OrderId};

const ENDPOINT_ENV: &str = "SPICEDB_ENDPOINT";
const PRESHARED_KEY_ENV: &str = "SPICEDB_PRESHARED_KEY";
const DEV_KEY: &str = "devkey";

fn authorization(endpoint: &str, timeout: Duration) -> SpiceDbAuthorization {
    let config = SpiceDbConfig {
        endpoint: endpoint.to_owned(),
        preshared_key: std::env::var(PRESHARED_KEY_ENV).unwrap_or_else(|_| DEV_KEY.to_owned()),
        insecure: true,
        timeout,
    };
    SpiceDbAuthorization::from_config(&config)
        .expect("valid config")
        .expect("endpoint is set")
}

/// `None` — SpiceDB не задан, тест пропускается.
fn live() -> Option<SpiceDbAuthorization> {
    let Ok(endpoint) = std::env::var(ENDPOINT_ENV) else {
        eprintln!("{ENDPOINT_ENV} is not set; skipping SpiceDB integration test");
        return None;
    };
    Some(authorization(&endpoint, Duration::from_secs(2)))
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

fn fulfillment_commands() -> [(DeliveryPermission, DeliveryResource); 3] {
    [
        (
            DeliveryPermission::CreateDelivery,
            DeliveryResource::Order(OrderId::new("order-1")),
        ),
        (
            DeliveryPermission::CancelDelivery,
            DeliveryResource::Order(OrderId::new("order-1")),
        ),
        (
            DeliveryPermission::ReplanDelivery,
            DeliveryResource::Delivery(DeliveryId::new("delivery-1")),
        ),
    ]
}

#[test]
fn orders_worker_coordinates_delivery() {
    let Some(authorization) = live() else { return };

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
    let Some(authorization) = live() else { return };

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
    let Some(authorization) = live() else { return };

    assert_eq!(
        CourierAuthorization::check(
            &authorization,
            &service("orders-worker"),
            CourierPermission::RegisterCourier,
            &CourierResource::Platform,
        ),
        Err(AuthorizationError::Denied)
    );
}

#[test]
fn courier_without_relationship_is_denied() {
    let Some(authorization) = live() else { return };

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
    let authorization = authorization("127.0.0.1:1", Duration::from_millis(300));

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
    let Some(authorization) = live() else { return };

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
