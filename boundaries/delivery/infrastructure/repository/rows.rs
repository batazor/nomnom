//! Rows of the compile-time checked queries (`sqlx::query_as!`) and row ↔
//! domain mapping of [`super::Store`].

use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::domains::courier::{Courier, CourierId, CourierName, CourierStatus};
use crate::slices::delivery::domains::delivery::{
    CompoundState, Delivery, DeliveryFailureReason, DeliveryId, DeliveryOffer, DeliveryOfferId,
    ExpectedTime, OfferState, OrderId, State,
};

use super::{corrupt, domain_version};

pub(super) fn courier_status(status: CourierStatus) -> &'static str {
    match status {
        CourierStatus::Available => "available",
        CourierStatus::Busy => "busy",
        CourierStatus::Unavailable => "unavailable",
    }
}

pub(super) fn courier_name(value: &str) -> Result<CourierName, RepositoryError> {
    CourierName::new(value).map_err(|error| corrupt("courier name", error))
}

pub(super) fn parse_courier_status(value: &str) -> Result<CourierStatus, RepositoryError> {
    match value {
        "available" => Ok(CourierStatus::Available),
        "busy" => Ok(CourierStatus::Busy),
        "unavailable" => Ok(CourierStatus::Unavailable),
        other => Err(corrupt("courier status", other)),
    }
}

pub(super) fn delivery_state(state: State) -> &'static str {
    match state {
        State::Waiting => "waiting",
        State::Assigned => "assigned",
        State::PickedUp => "picked_up",
        State::Delivered => "delivered",
        State::Failed => "failed",
        State::Cancelled => "cancelled",
    }
}

/// Stored states of an active Courier Assignment: `delivery#courier` exists
/// only for them (FR-444); the set is the leaves of the domain compound state.
/// Bound as `&[&str]` (the macros check `TEXT[]` against `&[String]`).
pub(super) fn active_assignment_states() -> Vec<&'static str> {
    State::leaves_of(CompoundState::ActiveAssignment)
        .map(delivery_state)
        .collect()
}

pub(super) fn parse_delivery_state(value: &str) -> Result<State, RepositoryError> {
    match value {
        "waiting" => Ok(State::Waiting),
        "assigned" => Ok(State::Assigned),
        "picked_up" => Ok(State::PickedUp),
        "delivered" => Ok(State::Delivered),
        "failed" => Ok(State::Failed),
        "cancelled" => Ok(State::Cancelled),
        other => Err(corrupt("delivery state", other)),
    }
}

pub(super) fn offer_state(state: OfferState) -> &'static str {
    match state {
        OfferState::Pending => "pending",
        OfferState::Accepted => "accepted",
        OfferState::Rejected => "rejected",
        OfferState::Withdrawn => "withdrawn",
        OfferState::Expired => "expired",
    }
}

pub(super) fn parse_offer_state(value: &str) -> Result<OfferState, RepositoryError> {
    match value {
        "pending" => Ok(OfferState::Pending),
        "accepted" => Ok(OfferState::Accepted),
        "rejected" => Ok(OfferState::Rejected),
        "withdrawn" => Ok(OfferState::Withdrawn),
        "expired" => Ok(OfferState::Expired),
        other => Err(corrupt("offer state", other)),
    }
}

/// Row of `couriers` as `sqlx::query_as!` reads it.
pub(super) struct CourierRow {
    pub(super) id: String,
    pub(super) name: String,
    pub(super) status: String,
    pub(super) retired_at: Option<i64>,
    pub(super) authorization_revision: Option<String>,
    pub(super) version: i64,
}

pub(super) fn courier_from_row(row: CourierRow) -> Result<Courier, RepositoryError> {
    let status = parse_courier_status(&row.status)?;
    Ok(
        Courier::restore(CourierId::new(row.id), courier_name(&row.name)?, status)
            .with_retired_at(row.retired_at)
            .with_authorization_revision(row.authorization_revision.map(AuthorizationRevision::new))
            .with_version(domain_version(row.version)?),
    )
}

/// Row of `deliveries` as `sqlx::query_as!` reads it.
pub(super) struct DeliveryRow {
    pub(super) id: String,
    pub(super) order_id: String,
    pub(super) courier_id: Option<String>,
    pub(super) state: String,
    pub(super) failure_reason: Option<String>,
    pub(super) failure_detail: Option<String>,
    pub(super) authorization_revision: Option<String>,
    pub(super) version: i64,
    pub(super) created_at: Option<i64>,
    pub(super) expected_pickup_at: Option<i64>,
    pub(super) expected_delivered_at: Option<i64>,
}

pub(super) fn delivery_from_row(row: DeliveryRow) -> Result<Delivery, RepositoryError> {
    // A free-text reason stored before the catalog reads back as `other`.
    let failure_reason = row
        .failure_reason
        .map(|reason| DeliveryFailureReason::restore(&reason, row.failure_detail.as_deref()));
    let version = domain_version(row.version)?;
    Delivery::restore(
        DeliveryId::new(row.id),
        OrderId::new(row.order_id),
        row.courier_id.map(CourierId::new),
        parse_delivery_state(&row.state)?,
        failure_reason,
    )
    .map_err(|error| corrupt("delivery", error))
    .map(|delivery| {
        delivery
            .with_created_at(row.created_at)
            .with_expected_times(
                row.expected_pickup_at.map(ExpectedTime::restore),
                row.expected_delivered_at.map(ExpectedTime::restore),
            )
            .with_authorization_revision(row.authorization_revision.map(AuthorizationRevision::new))
            .with_version(version)
    })
}

/// Row of `delivery_offers` as `sqlx::query_as!` reads it.
pub(super) struct OfferRow {
    pub(super) id: String,
    pub(super) delivery_id: String,
    pub(super) courier_id: String,
    pub(super) offered_at: i64,
    pub(super) expires_at: i64,
    pub(super) state: String,
    pub(super) version: i64,
}

pub(super) fn offer_from_row(row: OfferRow) -> Result<DeliveryOffer, RepositoryError> {
    let version = domain_version(row.version)?;
    DeliveryOffer::restore(
        DeliveryOfferId::new(row.id),
        DeliveryId::new(row.delivery_id),
        CourierId::new(row.courier_id),
        row.offered_at,
        row.expires_at,
        parse_offer_state(&row.state)?,
    )
    .map_err(|error| corrupt("delivery offer", error))
    .map(|offer| offer.with_version(version))
}
