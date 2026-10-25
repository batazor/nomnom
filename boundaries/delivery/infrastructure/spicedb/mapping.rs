//! Нормативное соответствие портов `delivery` и permissions SpiceDB
//! (`openspec/changes/add-spicedb-authorization/design.md`). Неотображённые
//! сочетания permission и resource — отказ без обращения к SpiceDB.

use std::fmt::{Display, Formatter};

use crate::pkg::application::Subject;
use crate::slices::courier::applications::authorization::{CourierPermission, CourierResource};
use crate::slices::delivery::applications::authorization::{DeliveryPermission, DeliveryResource};

/// Синглтон platform, на котором выданы fulfillment capabilities.
const PLATFORM: (&str, &str) = ("platform", "nomnom");

/// Один `CheckPermission`: `resource_type:resource_id#permission@subject_type:subject_id`.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PermissionCheck {
    pub resource_type: &'static str,
    pub resource_id: String,
    pub permission: &'static str,
    pub subject_type: &'static str,
    pub subject_id: String,
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
        (CourierPermission::RegisterCourier, CourierResource::Platform) => {
            Some(PermissionCheck::platform("register_courier", subject))
        }
        (CourierPermission::RegisterCourier, CourierResource::Courier(_))
        | (_, CourierResource::Platform) => None,
        (_, CourierResource::Courier(id)) => {
            Some(PermissionCheck::new("courier", id.as_str(), "act", subject))
        }
    }
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
    )
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
    match resource {
        DeliveryResource::Courier(id) => {
            Some(PermissionCheck::new("courier", id.as_str(), "act", subject))
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

    const ALL_DELIVERY_PERMISSIONS: [DeliveryPermission; 12] = [
        DeliveryPermission::CreateDelivery,
        DeliveryPermission::FindCourier,
        DeliveryPermission::OfferDelivery,
        DeliveryPermission::AcceptDeliveryOffer,
        DeliveryPermission::RejectDeliveryOffer,
        DeliveryPermission::ReassignCourier,
        DeliveryPermission::ConfirmPickup,
        DeliveryPermission::ConfirmDelivery,
        DeliveryPermission::ReportDeliveryFailure,
        DeliveryPermission::ListAvailableDeliveryOffers,
        DeliveryPermission::CancelDelivery,
        DeliveryPermission::ReplanDelivery,
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

    #[test]
    fn register_courier_on_platform_checks_platform_capability() {
        assert_eq!(
            rendered(courier_check(
                &user(),
                CourierPermission::RegisterCourier,
                &CourierResource::Platform
            )),
            Some("platform:nomnom#register_courier@user:user-1".to_owned())
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
                CourierPermission::RegisterCourier,
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
        let others = ALL_DELIVERY_PERMISSIONS
            .into_iter()
            .filter(|permission| !coordinates_delivery(*permission));
        for permission in others {
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
                Some("delivery:delivery-1#act_as_courier@user:user-1".to_owned())
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
    fn six_commands_are_fulfillment_capabilities() {
        let count = ALL_DELIVERY_PERMISSIONS
            .into_iter()
            .filter(|permission| coordinates_delivery(*permission))
            .count();
        assert_eq!(count, 6);
    }
}
