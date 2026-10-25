//! SpiceDB-адаптеры портов [`CourierAuthorization`] и [`DeliveryAuthorization`]
//! (ADR 18, FR-435). Каждый check отображается по таблице `design.md`
//! (`mapping`); разрешает только `PERMISSIONSHIP_HAS_PERMISSION`, любая ошибка
//! или timeout SpiceDB — [`AuthorizationError::Unavailable`] (fail closed).
//!
//! [`SpiceDbRelationships`] закрывает порты записи relationships слайсов
//! (FR-444), а [`RelationshipGraph`] читает граф для reconciliation (FR-445).
//!
//! Checks на `courier:<id>` и `delivery:<id>` идут с `at_least_as_fresh` по
//! revision, сохранённой с ресурсом ([`RevisionReader`], FR-447); без revision
//! и для `platform:nomnom` — `minimize_latency` (FR-448). Ошибка чтения
//! revision — authorization unavailable (fail closed).

mod client;
mod mapping;
mod relationships;
mod revisions;

/// Сгенерированные stubs `authzed.api.v1` (`buf.gen.yaml`) не выходят за
/// границу infrastructure.
#[allow(clippy::all, clippy::pedantic, dead_code, missing_docs)]
pub mod v1 {
    include!("gen/authzed/api/v1/authzed.api.v1.rs");
}

pub use client::{ClientTelemetry, PermissionChecker, SpiceDbClient, SpiceDbConfigError};
pub use mapping::{CheckConsistency, PermissionCheck, courier_check, delivery_check};
pub use relationships::{
    COURIER, COURIER_SELF, DELIVERY, DELIVERY_COURIER, Relationship, RelationshipGraph,
    RelationshipOperation, RelationshipPage, SpiceDbRelationships, USER, courier_self,
    delivery_courier,
};
pub use revisions::{RepositoryRevisions, RevisionReader};

#[cfg(test)]
pub(crate) use relationships::tests::FakeGraph;

use std::sync::Arc;

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
    revisions: Option<Arc<dyn RevisionReader>>,
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
    /// Без [`RevisionReader`] все checks идут с `minimize_latency`.
    #[must_use]
    pub fn new(checker: Box<dyn PermissionChecker>) -> Self {
        Self {
            checker,
            revisions: None,
        }
    }

    /// Checks ресурсов с сохранённой revision — `at_least_as_fresh`.
    #[must_use]
    pub fn with_revisions(mut self, revisions: Arc<dyn RevisionReader>) -> Self {
        self.revisions = Some(revisions);
        self
    }

    /// Ошибка — пустой endpoint или невалидная конфигурация.
    pub fn from_config(config: &SpiceDbConfig) -> Result<Self, SpiceDbConfigError> {
        Ok(Self::new(Box::new(SpiceDbClient::connect(config)?)))
    }

    fn decide(&self, check: Option<PermissionCheck>) -> Result<(), AuthorizationError> {
        let Some(mut check) = check else {
            return Err(AuthorizationError::Denied);
        };
        check.consistency = self.consistency(&check)?;
        match self.checker.check(&check) {
            Ok(true) => Ok(()),
            Ok(false) => Err(AuthorizationError::Denied),
            Err(reason) => Err(AuthorizationError::Unavailable(reason)),
        }
    }
}

impl SpiceDbAuthorization {
    /// `at_least_as_fresh` для `courier`/`delivery` с сохранённой revision,
    /// иначе `minimize_latency`.
    fn consistency(&self, check: &PermissionCheck) -> Result<CheckConsistency, AuthorizationError> {
        let Some(revisions) = &self.revisions else {
            return Ok(CheckConsistency::MinimizeLatency);
        };
        if !matches!(check.resource_type, COURIER | DELIVERY) {
            return Ok(CheckConsistency::MinimizeLatency);
        }
        let revision = revisions
            .revision(check.resource_type, &check.resource_id)
            .map_err(|reason| {
                AuthorizationError::Unavailable(format!(
                    "read authorization revision of {}:{}: {reason}",
                    check.resource_type, check.resource_id
                ))
            })?;
        Ok(revision.map_or(
            CheckConsistency::MinimizeLatency,
            CheckConsistency::AtLeastAsFresh,
        ))
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
mod tests;
