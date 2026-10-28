//! Нормативное соответствие портов `delivery` и permissions SpiceDB
//! (`openspec/changes/add-spicedb-authorization/design.md`). Неотображённые
//! сочетания permission и resource — отказ без обращения к SpiceDB.

use std::fmt::{Display, Formatter};

use crate::pkg::application::Subject;
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::applications::authorization::{CourierPermission, CourierResource};
use crate::slices::delivery::applications::authorization::{DeliveryPermission, DeliveryResource};

/// Синглтон platform, на котором выданы fulfillment capabilities.
const PLATFORM: (&str, &str) = ("platform", "nomnom");

/// Consistency одного `CheckPermission` (FR-447, FR-448).
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub enum CheckConsistency {
    /// Ресурс без сохранённой revision и `platform:nomnom`.
    #[default]
    MinimizeLatency,
    /// Ресурс с сохранённой revision собственной записи relationship.
    AtLeastAsFresh(AuthorizationRevision),
}

/// Один `CheckPermission`: `resource_type:resource_id#permission@subject_type:subject_id`.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PermissionCheck {
    pub resource_type: &'static str,
    pub resource_id: String,
    pub permission: &'static str,
    pub subject_type: &'static str,
    pub subject_id: String,
    pub consistency: CheckConsistency,
}

impl Display for PermissionCheck {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        write!(
            formatter,
            "{}:{}#{}@{}:{}",
            self.resource_type,
            self.resource_id,
            self.permission,
            self.subject_type,
            self.subject_id
        )
    }
}

impl PermissionCheck {
    fn new(
        resource_type: &'static str,
        resource_id: &str,
        permission: &'static str,
        subject: &Subject,
    ) -> Self {
        let (subject_type, subject_id) = match subject {
            Subject::User(id) => ("user", id),
            Subject::Service(id) => ("service", id),
        };
        Self {
            resource_type,
            resource_id: resource_id.to_owned(),
            permission,
            subject_type,
            subject_id: subject_id.clone(),
            consistency: CheckConsistency::MinimizeLatency,
        }
    }

    fn platform(permission: &'static str, subject: &Subject) -> Self {
        Self::new(PLATFORM.0, PLATFORM.1, permission, subject)
    }
}

/// Check слайса `courier`; `None` — сочетание не отображено и отклоняется.
#[must_use]
pub fn courier_check(
    subject: &Subject,
    permission: CourierPermission,
    resource: &CourierResource,
) -> Option<PermissionCheck> {
    match (permission, resource) {
        (CourierPermission::ManageCouriers, CourierResource::Platform) => {
            Some(PermissionCheck::platform("manage_couriers", subject))
        }
        (CourierPermission::ManageCouriers, CourierResource::Courier(_))
        | (_, CourierResource::Platform) => None,
        (_, CourierResource::Courier(id)) => {
            Some(PermissionCheck::new("courier", id.as_str(), "act", subject))
        }
    }
}

/// Check слайса `simulator`: `platform:nomnom#operate_simulator` (FR-604).
#[must_use]
pub fn simulator_check(subject: &Subject) -> PermissionCheck {
    PermissionCheck::platform("operate_simulator", subject)
}

/// Команды Order Fulfillment Workflow: capability `platform:nomnom`
/// независимо от resource.
const fn coordinates_delivery(permission: DeliveryPermission) -> bool {
    matches!(
        permission,
        DeliveryPermission::CreateDelivery
            | DeliveryPermission::FindCourier
            | DeliveryPermission::OfferDelivery
            | DeliveryPermission::ReassignCourier
            | DeliveryPermission::CancelDelivery
            | DeliveryPermission::ReplanDelivery
            | DeliveryPermission::EstimateDeliveryDuration
    )
}

/// Permissions that follow `order#view`.
fn views_order(permission: DeliveryPermission) -> bool {
    permission == DeliveryPermission::ViewOrderDelivery
}

/// Permissions of a dispatcher of the Courier Pool on the platform.
fn dispatches_offers(permission: DeliveryPermission) -> bool {
    permission == DeliveryPermission::OfferDeliveryToCourier
}

/// Check слайса `delivery`; `None` — сочетание не отображено и отклоняется.
#[must_use]
pub fn delivery_check(
    subject: &Subject,
    permission: DeliveryPermission,
    resource: &DeliveryResource,
) -> Option<PermissionCheck> {
    if coordinates_delivery(permission) {
        return Some(PermissionCheck::platform("coordinate_delivery", subject));
    }
    // The courier of an order and its tracking are part of what its viewers
    // see (FR-626, FR-660).
    // A dispatcher offers the delivery of any order of the platform to a
    // courier they chose.
    if dispatches_offers(permission) {
        return match resource {
            DeliveryResource::Platform => {
                Some(PermissionCheck::platform("offer_delivery", subject))
            }
            _ => None,
        };
    }
    if views_order(permission) {
        return match resource {
            DeliveryResource::Order(id) => {
                Some(PermissionCheck::new("order", id.as_str(), "view", subject))
            }
            _ => None,
        };
    }
    match resource {
        DeliveryResource::Courier(id) => {
            Some(PermissionCheck::new("courier", id.as_str(), "act", subject))
        }
        // Dispatcher action on any delivery: a platform right of the
        // operator, not the courier's access to the delivery (AC-694).
        DeliveryResource::Delivery(_)
            if permission == DeliveryPermission::ReportDeliveryFailure =>
        {
            Some(PermissionCheck::platform("dispatch_delivery", subject))
        }
        DeliveryResource::Delivery(id) => Some(PermissionCheck::new(
            "delivery",
            id.as_str(),
            "act_as_courier",
            subject,
        )),
        DeliveryResource::Platform | DeliveryResource::Order(_) => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::delivery::domains::delivery::{DeliveryId, OrderId};

    const ALL_DELIVERY_PERMISSIONS: [DeliveryPermission; 15] = [
        DeliveryPermission::CreateDelivery,
        DeliveryPermission::FindCourier,
        DeliveryPermission::OfferDelivery,
        DeliveryPermission::OfferDeliveryToCourier,
        DeliveryPermission::AcceptDeliveryOffer,
        DeliveryPermission::RejectDeliveryOffer,
        DeliveryPermission::ReassignCourier,
        DeliveryPermission::ConfirmPickup,
        DeliveryPermission::ConfirmDelivery,
        DeliveryPermission::ReportDeliveryFailure,
        DeliveryPermission::ListAvailableDeliveryOffers,
        DeliveryPermission::CancelDelivery,
        DeliveryPermission::ReplanDelivery,
        DeliveryPermission::ViewOrderDelivery,
        DeliveryPermission::EstimateDeliveryDuration,
    ];

    fn worker() -> Subject {
        Subject::Service("orders-worker".to_owned())
    }

    fn user() -> Subject {
        Subject::User("user-1".to_owned())
    }

    fn rendered(check: Option<PermissionCheck>) -> Option<String> {
        check.map(|check| check.to_string())
    }

    fn delivery_resources() -> [DeliveryResource; 4] {
        [
            DeliveryResource::Platform,
            DeliveryResource::Delivery(DeliveryId::new("delivery-1")),
            DeliveryResource::Order(OrderId::new("order-1")),
            DeliveryResource::Courier(CourierId::new("courier-1")),
        ]
    }

    /// FR-604
    #[test]
    fn simulator_checks_platform_operate_simulator() {
        assert_eq!(
            simulator_check(&user()).to_string(),
            "platform:nomnom#operate_simulator@user:user-1"
        );
    }

    #[test]
    fn manage_couriers_on_platform_checks_platform_capability() {
        assert_eq!(
            rendered(courier_check(
                &user(),
                CourierPermission::ManageCouriers,
                &CourierResource::Platform
            )),
            Some("platform:nomnom#manage_couriers@user:user-1".to_owned())
        );
    }

    #[test]
    fn courier_permissions_on_courier_check_act() {
        for permission in [
            CourierPermission::SetCourierAvailability,
            CourierPermission::UpdateCourierLocation,
            CourierPermission::ViewCourierCurrentDelivery,
        ] {
            assert_eq!(
                rendered(courier_check(
                    &user(),
                    permission,
                    &CourierResource::Courier(CourierId::new("courier-1"))
                )),
                Some("courier:courier-1#act@user:user-1".to_owned()),
                "{permission:?}"
            );
        }
    }

    #[test]
    fn unmapped_courier_checks_are_denied() {
        assert_eq!(
            courier_check(
                &user(),
                CourierPermission::ManageCouriers,
                &CourierResource::Courier(CourierId::new("courier-1"))
            ),
            None
        );
        assert_eq!(
            courier_check(
                &user(),
                CourierPermission::UpdateCourierLocation,
                &CourierResource::Platform
            ),
            None
        );
    }

    #[test]
    fn fulfillment_commands_check_coordinate_delivery_on_any_resource() {
        for permission in ALL_DELIVERY_PERMISSIONS
            .into_iter()
            .filter(|permission| coordinates_delivery(*permission))
        {
            for resource in delivery_resources() {
                assert_eq!(
                    rendered(delivery_check(&worker(), permission, &resource)),
                    Some("platform:nomnom#coordinate_delivery@service:orders-worker".to_owned()),
                    "{permission:?} on {resource:?}"
                );
            }
        }
    }

    #[test]
    fn other_delivery_permissions_check_courier_or_delivery() {
        let others = ALL_DELIVERY_PERMISSIONS.into_iter().filter(|permission| {
            !coordinates_delivery(*permission)
                && !views_order(*permission)
                && !dispatches_offers(*permission)
        });
        for permission in others {
            let on_delivery = if permission == DeliveryPermission::ReportDeliveryFailure {
                "platform:nomnom#dispatch_delivery@user:user-1"
            } else {
                "delivery:delivery-1#act_as_courier@user:user-1"
            };
            assert_eq!(
                rendered(delivery_check(
                    &user(),
                    permission,
                    &DeliveryResource::Courier(CourierId::new("courier-1"))
                )),
                Some("courier:courier-1#act@user:user-1".to_owned())
            );
            assert_eq!(
                rendered(delivery_check(
                    &user(),
                    permission,
                    &DeliveryResource::Delivery(DeliveryId::new("delivery-1"))
                )),
                Some(on_delivery.to_owned()),
                "{permission:?}"
            );
            assert_eq!(
                delivery_check(&user(), permission, &DeliveryResource::Platform),
                None
            );
            assert_eq!(
                delivery_check(
                    &user(),
                    permission,
                    &DeliveryResource::Order(OrderId::new("order-1"))
                ),
                None
            );
        }
    }

    #[test]
    fn dispatcher_offer_checks_platform_offer_delivery() {
        assert_eq!(
            rendered(delivery_check(
                &user(),
                DeliveryPermission::OfferDeliveryToCourier,
                &DeliveryResource::Platform
            )),
            Some("platform:nomnom#offer_delivery@user:user-1".to_owned())
        );
        for resource in delivery_resources() {
            if matches!(resource, DeliveryResource::Platform) {
                continue;
            }
            assert_eq!(
                delivery_check(
                    &user(),
                    DeliveryPermission::OfferDeliveryToCourier,
                    &resource
                ),
                None
            );
        }
    }

    /// FR-626, FR-660: the delivery of an order — courier and tracking — follows
    /// `order#view`; other resources are not mapped.
    #[test]
    fn order_courier_and_tracking_check_order_view() {
        for permission in [DeliveryPermission::ViewOrderDelivery] {
            assert_eq!(
                rendered(delivery_check(
                    &user(),
                    permission,
                    &DeliveryResource::Order(OrderId::new("order-1"))
                )),
                Some("order:order-1#view@user:user-1".to_owned())
            );
            for resource in delivery_resources() {
                if matches!(resource, DeliveryResource::Order(_)) {
                    continue;
                }
                assert_eq!(delivery_check(&user(), permission, &resource), None);
            }
        }
    }
}
