//! SpiceDB-адаптеры портов [`CourierAuthorization`] и [`DeliveryAuthorization`]
//! (ADR 18, FR-435). Каждый check отображается по таблице `design.md`
//! (`mapping`); разрешает только `PERMISSIONSHIP_HAS_PERMISSION`, любая ошибка
//! или timeout SpiceDB — [`AuthorizationError::Unavailable`] (fail closed).
//!
//! [`SpiceDbRelationships`] закрывает порты записи relationships слайсов
//! (FR-444), а [`RelationshipGraph`] читает граф для reconciliation (FR-445).

mod client;
mod mapping;
mod relationships;

/// Сгенерированные stubs `authzed.api.v1` (`buf.gen.yaml`) не выходят за
/// границу infrastructure.
#[allow(clippy::all, clippy::pedantic, dead_code, missing_docs)]
pub mod v1 {
    include!("gen/authzed/api/v1/authzed.api.v1.rs");
}

pub use client::{PermissionChecker, SpiceDbClient, SpiceDbConfigError};
pub use mapping::{PermissionCheck, courier_check, delivery_check};
pub use relationships::{
    COURIER, COURIER_SELF, DELIVERY, DELIVERY_COURIER, Relationship, RelationshipGraph,
    RelationshipOperation, RelationshipPage, SpiceDbRelationships, USER, courier_self,
    delivery_courier,
};

#[cfg(test)]
pub(crate) use relationships::tests::FakeGraph;

use crate::pkg::application::{AuthorizationError, Subject};
use crate::pkg::config::SpiceDbConfig;
use crate::slices::courier::applications::authorization::{
    CourierAuthorization, CourierPermission, CourierResource,
};
use crate::slices::delivery::applications::authorization::{
    DeliveryAuthorization, DeliveryPermission, DeliveryResource,
};

/// Авторизация обоих слайсов через `CheckPermission`.
pub struct SpiceDbAuthorization {
    checker: Box<dyn PermissionChecker>,
}

impl std::fmt::Debug for SpiceDbAuthorization {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("SpiceDbAuthorization")
            .finish_non_exhaustive()
    }
}

impl SpiceDbAuthorization {
    /// Адаптер поверх произвольного checker — для тестов маппинга и отказов.
    #[must_use]
    pub fn new(checker: Box<dyn PermissionChecker>) -> Self {
        Self { checker }
    }

    /// `None`, если endpoint не задан; ошибка — невалидная конфигурация.
    pub fn from_config(config: &SpiceDbConfig) -> Result<Option<Self>, SpiceDbConfigError> {
        if !config.enabled() {
            return Ok(None);
        }
        Ok(Some(Self::new(Box::new(SpiceDbClient::connect(config)?))))
    }

    fn decide(&self, check: Option<PermissionCheck>) -> Result<(), AuthorizationError> {
        let Some(check) = check else {
            return Err(AuthorizationError::Denied);
        };
        match self.checker.check(&check) {
            Ok(true) => Ok(()),
            Ok(false) => Err(AuthorizationError::Denied),
            Err(reason) => Err(AuthorizationError::Unavailable(reason)),
        }
    }
}

impl CourierAuthorization for SpiceDbAuthorization {
    fn check(
        &self,
        subject: &Subject,
        permission: CourierPermission,
        resource: &CourierResource,
    ) -> Result<(), AuthorizationError> {
        self.decide(courier_check(subject, permission, resource))
    }
}

impl DeliveryAuthorization for SpiceDbAuthorization {
    fn check(
        &self,
        subject: &Subject,
        permission: DeliveryPermission,
        resource: &DeliveryResource,
    ) -> Result<(), AuthorizationError> {
        self.decide(delivery_check(subject, permission, resource))
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex, PoisonError};

    use super::*;
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::delivery::domains::delivery::OrderId;

    /// Fake SpiceDB: фиксированный ответ и журнал запросов.
    struct FakeChecker {
        outcome: Result<bool, String>,
        seen: Arc<Mutex<Vec<String>>>,
    }

    impl PermissionChecker for FakeChecker {
        fn check(&self, check: &PermissionCheck) -> Result<bool, String> {
            self.seen
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .push(check.to_string());
            self.outcome.clone()
        }
    }

    fn adapter(outcome: Result<bool, String>) -> (SpiceDbAuthorization, Arc<Mutex<Vec<String>>>) {
        let seen = Arc::new(Mutex::new(Vec::new()));
        let checker = FakeChecker {
            outcome,
            seen: seen.clone(),
        };
        (SpiceDbAuthorization::new(Box::new(checker)), seen)
    }

    fn worker() -> Subject {
        Subject::Service("orders-worker".to_owned())
    }

    fn create_delivery(authorization: &SpiceDbAuthorization) -> Result<(), AuthorizationError> {
        DeliveryAuthorization::check(
            authorization,
            &worker(),
            DeliveryPermission::CreateDelivery,
            &DeliveryResource::Order(OrderId::new("order-1")),
        )
    }

    #[test]
    fn has_permission_allows() {
        let (authorization, seen) = adapter(Ok(true));

        assert_eq!(create_delivery(&authorization), Ok(()));
        assert_eq!(
            *seen.lock().unwrap_or_else(PoisonError::into_inner),
            vec!["platform:nomnom#coordinate_delivery@service:orders-worker".to_owned()]
        );
    }

    #[test]
    fn no_permission_denies() {
        let (authorization, _) = adapter(Ok(false));

        assert_eq!(
            create_delivery(&authorization),
            Err(AuthorizationError::Denied)
        );
    }

    #[test]
    fn spicedb_error_fails_closed_as_unavailable() {
        let (authorization, _) = adapter(Err("spicedb check timed out".to_owned()));

        assert!(matches!(
            create_delivery(&authorization),
            Err(AuthorizationError::Unavailable(_))
        ));
        assert!(matches!(
            CourierAuthorization::check(
                &authorization,
                &worker(),
                CourierPermission::RegisterCourier,
                &CourierResource::Platform,
            ),
            Err(AuthorizationError::Unavailable(_))
        ));
    }

    #[test]
    fn unmapped_check_is_denied_without_calling_spicedb() {
        let (authorization, seen) = adapter(Ok(true));

        assert_eq!(
            CourierAuthorization::check(
                &authorization,
                &worker(),
                CourierPermission::UpdateCourierLocation,
                &CourierResource::Platform,
            ),
            Err(AuthorizationError::Denied)
        );
        assert_eq!(
            DeliveryAuthorization::check(
                &authorization,
                &worker(),
                DeliveryPermission::ConfirmPickup,
                &DeliveryResource::Order(OrderId::new("order-1")),
            ),
            Err(AuthorizationError::Denied)
        );
        assert!(
            seen.lock()
                .unwrap_or_else(PoisonError::into_inner)
                .is_empty()
        );
    }

    #[test]
    fn courier_permission_checks_courier_act() {
        let (authorization, seen) = adapter(Ok(true));

        assert_eq!(
            CourierAuthorization::check(
                &authorization,
                &Subject::User("user-1".to_owned()),
                CourierPermission::SetCourierAvailability,
                &CourierResource::Courier(CourierId::new("courier-1")),
            ),
            Ok(())
        );
        assert_eq!(
            *seen.lock().unwrap_or_else(PoisonError::into_inner),
            vec!["courier:courier-1#act@user:user-1".to_owned()]
        );
    }

    #[test]
    fn empty_endpoint_keeps_local_authorization() {
        assert!(matches!(
            SpiceDbAuthorization::from_config(&SpiceDbConfig::default()),
            Ok(None)
        ));
    }
}
