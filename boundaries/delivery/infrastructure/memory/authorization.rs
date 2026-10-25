use std::sync::{Mutex, PoisonError};

use crate::pkg::application::{AuthorizationError, Subject};
use crate::slices::courier::applications::authorization::{
    CourierAuthorization, CourierPermission, CourierResource,
};
use crate::slices::delivery::applications::authorization::{
    DeliveryAuthorization, DeliveryPermission, DeliveryResource,
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Mode {
    AllowAll,
    DenyAll,
    Unavailable,
    Grants,
}

type CourierCheck = (Subject, CourierPermission, CourierResource);
type DeliveryCheck = (Subject, DeliveryPermission, DeliveryResource);

/// Test authorization port: allow everything, deny everything, simulate an
/// outage, or allow only explicit grants. Every check is recorded.
#[derive(Debug)]
pub struct StaticAuthorization {
    mode: Mode,
    courier_grants: Mutex<Vec<CourierCheck>>,
    delivery_grants: Mutex<Vec<DeliveryCheck>>,
    courier_checks: Mutex<Vec<CourierCheck>>,
    delivery_checks: Mutex<Vec<DeliveryCheck>>,
}

impl StaticAuthorization {
    const fn with_mode(mode: Mode) -> Self {
        Self {
            mode,
            courier_grants: Mutex::new(Vec::new()),
            delivery_grants: Mutex::new(Vec::new()),
            courier_checks: Mutex::new(Vec::new()),
            delivery_checks: Mutex::new(Vec::new()),
        }
    }

    #[must_use]
    pub const fn allow_all() -> Self {
        Self::with_mode(Mode::AllowAll)
    }

    #[must_use]
    pub const fn deny_all() -> Self {
        Self::with_mode(Mode::DenyAll)
    }

    #[must_use]
    pub const fn unavailable() -> Self {
        Self::with_mode(Mode::Unavailable)
    }

    /// Denies everything except explicitly granted checks.
    #[must_use]
    pub const fn grants_only() -> Self {
        Self::with_mode(Mode::Grants)
    }

    pub fn grant_courier(
        &self,
        subject: Subject,
        permission: CourierPermission,
        resource: CourierResource,
    ) {
        lock(&self.courier_grants).push((subject, permission, resource));
    }

    pub fn grant_delivery(
        &self,
        subject: Subject,
        permission: DeliveryPermission,
        resource: DeliveryResource,
    ) {
        lock(&self.delivery_grants).push((subject, permission, resource));
    }

    #[must_use]
    pub fn courier_checks(&self) -> Vec<CourierCheck> {
        lock(&self.courier_checks).clone()
    }

    #[must_use]
    pub fn delivery_checks(&self) -> Vec<DeliveryCheck> {
        lock(&self.delivery_checks).clone()
    }

    fn decide(&self, granted: bool) -> Result<(), AuthorizationError> {
        match self.mode {
            Mode::AllowAll => Ok(()),
            Mode::DenyAll => Err(AuthorizationError::Denied),
            Mode::Unavailable => Err(AuthorizationError::Unavailable(
                "authorization offline".to_owned(),
            )),
            Mode::Grants if granted => Ok(()),
            Mode::Grants => Err(AuthorizationError::Denied),
        }
    }
}

fn lock<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(PoisonError::into_inner)
}

impl CourierAuthorization for StaticAuthorization {
    fn check(
        &self,
        subject: &Subject,
        permission: CourierPermission,
        resource: &CourierResource,
    ) -> Result<(), AuthorizationError> {
        let check = (subject.clone(), permission, resource.clone());
        let granted = lock(&self.courier_grants).contains(&check);
        lock(&self.courier_checks).push(check);
        self.decide(granted)
    }
}

impl DeliveryAuthorization for StaticAuthorization {
    fn check(
        &self,
        subject: &Subject,
        permission: DeliveryPermission,
        resource: &DeliveryResource,
    ) -> Result<(), AuthorizationError> {
        let check = (subject.clone(), permission, resource.clone());
        let granted = lock(&self.delivery_grants).contains(&check);
        lock(&self.delivery_checks).push(check);
        self.decide(granted)
    }
}
