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
//!
//! Каждое решение по отображённому check, включая отказ из-за непрочитанной
//! revision, пишет метрики порта авторизации ([`CheckMetrics`]).

mod checks;
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

pub use checks::{AUTHZ_METER_NAME, CheckMetrics};
pub use client::{
    CallFailure, ClientTelemetry, PermissionChecker, SpiceDbClient, SpiceDbConfigError,
};
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
use std::time::Instant;

use checks::CheckResult;

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
    metrics: CheckMetrics,
}

impl std::fmt::Debug for SpiceDbAuthorization {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("SpiceDbAuthorization")
            .finish_non_exhaustive()
    }
}

impl SpiceDbAuthorization {
    /// Адаптер поверх произвольного checker (в тестах — fake маппинга и
    /// отказов) с метриками checks. Без [`RevisionReader`] все checks идут с
    /// `minimize_latency`.
    #[must_use]
    pub fn new(checker: Box<dyn PermissionChecker>, metrics: CheckMetrics) -> Self {
        Self {
            checker,
            revisions: None,
            metrics,
        }
    }

    /// Checks ресурсов с сохранённой revision — `at_least_as_fresh`.
    #[must_use]
    pub fn with_revisions(mut self, revisions: Arc<dyn RevisionReader>) -> Self {
        self.revisions = Some(revisions);
        self
    }

    /// `None`, если endpoint не задан; ошибка — невалидная конфигурация.
    pub fn from_config(config: &SpiceDbConfig) -> Result<Option<Self>, SpiceDbConfigError> {
        if !config.enabled() {
            return Ok(None);
        }
        Ok(Some(Self::new(
            Box::new(SpiceDbClient::connect(config)?),
            CheckMetrics::global(),
        )))
    }

    fn decide(&self, check: Option<PermissionCheck>) -> Result<(), AuthorizationError> {
        let Some(mut check) = check else {
            return Err(AuthorizationError::Denied);
        };
        let started = Instant::now();
        // Непрочитанная revision — отказ до SpiceDB (fail closed), `error`.
        let answer = self.consistency(&check).map(|consistency| {
            check.consistency = consistency;
            self.checker.check(&check)
        });
        let (result, decision) = match answer {
            Ok(Ok(true)) => (CheckResult::Allowed, Ok(())),
            Ok(Ok(false)) => (CheckResult::Denied, Err(AuthorizationError::Denied)),
            Ok(Err(CallFailure::Timeout(reason))) => (
                CheckResult::Timeout,
                Err(AuthorizationError::Unavailable(reason)),
            ),
            Ok(Err(CallFailure::Failed(reason))) => (
                CheckResult::Error,
                Err(AuthorizationError::Unavailable(reason)),
            ),
            Err(error) => (CheckResult::Error, Err(error)),
        };
        self.metrics.record(&check, result, started);
        decision
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
