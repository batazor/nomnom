//! Column lists and row ↔ domain mapping of [`super::PostgresStore`].

use sqlx::postgres::PgRow;
use sqlx::{Postgres, Row};

use crate::pkg::persistence::{AuthorizationRevision, RepositoryError};
use crate::slices::courier::domains::courier::{Courier, CourierId, CourierStatus};
use crate::slices::delivery::domains::delivery::{
    Address, Delivery, DeliveryFailureReason, DeliveryId, DeliveryOffer, DeliveryOfferId,
    OfferState, OrderId, State,
};

use super::{corrupt, domain_version};

pub(super) fn courier_status(status: CourierStatus) -> &'static str {
    match status {
        CourierStatus::Available => "available",
        CourierStatus::Busy => "busy",
        CourierStatus::Unavailable => "unavailable",
    }
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
    }
}

pub(super) fn parse_offer_state(value: &str) -> Result<OfferState, RepositoryError> {
    match value {
        "pending" => Ok(OfferState::Pending),
        "accepted" => Ok(OfferState::Accepted),
        "rejected" => Ok(OfferState::Rejected),
        "withdrawn" => Ok(OfferState::Withdrawn),
        other => Err(corrupt("offer state", other)),
    }
}

pub(super) fn address(value: &str) -> Result<Address, RepositoryError> {
    Address::new(value).map_err(|error| corrupt("address", error))
}

pub(super) fn column<'r, T>(row: &'r PgRow, name: &str) -> Result<T, RepositoryError>
where
    T: sqlx::Decode<'r, Postgres> + sqlx::Type<Postgres>,
{
    row.try_get(name).map_err(|error| corrupt(name, error))
}

pub(super) const COURIER_COLUMNS: &str = "id, status, authorization_revision, version";

pub(super) fn courier_from_row(row: &PgRow) -> Result<Courier, RepositoryError> {
    let status = parse_courier_status(column(row, "status")?)?;
    let revision: Option<String> = column(row, "authorization_revision")?;
    Ok(
        Courier::restore(CourierId::new(column::<String>(row, "id")?), status)
            .with_authorization_revision(revision.map(AuthorizationRevision::new))
            .with_version(domain_version(column(row, "version")?)?),
    )
}

pub(super) const DELIVERY_COLUMNS: &str = "id, order_id, courier_id, pickup_address, dropoff_address, \
     state, failure_reason, authorization_revision, version";

pub(super) fn delivery_from_row(row: &PgRow) -> Result<Delivery, RepositoryError> {
    let failure_reason = column::<Option<String>>(row, "failure_reason")?
        .map(|reason| {
            DeliveryFailureReason::new(&reason).map_err(|error| corrupt("failure reason", error))
        })
        .transpose()?;
    let revision: Option<String> = column(row, "authorization_revision")?;
    let version = domain_version(column(row, "version")?)?;
    Delivery::restore(
        DeliveryId::new(column::<String>(row, "id")?),
        OrderId::new(column::<String>(row, "order_id")?),
        column::<Option<String>>(row, "courier_id")?.map(CourierId::new),
        address(column(row, "pickup_address")?)?,
        address(column(row, "dropoff_address")?)?,
        parse_delivery_state(column(row, "state")?)?,
        failure_reason,
    )
    .map_err(|error| corrupt("delivery", error))
    .map(|delivery| {
        delivery
            .with_authorization_revision(revision.map(AuthorizationRevision::new))
            .with_version(version)
    })
}

pub(super) const OFFER_COLUMNS: &str =
    "id, delivery_id, courier_id, offered_at, expires_at, state, version";

pub(super) fn offer_from_row(row: &PgRow) -> Result<DeliveryOffer, RepositoryError> {
    let version = domain_version(column(row, "version")?)?;
    DeliveryOffer::restore(
        DeliveryOfferId::new(column::<String>(row, "id")?),
        DeliveryId::new(column::<String>(row, "delivery_id")?),
        CourierId::new(column::<String>(row, "courier_id")?),
        column(row, "offered_at")?,
        column(row, "expires_at")?,
        parse_offer_state(column(row, "state")?)?,
    )
    .map_err(|error| corrupt("delivery offer", error))
    .map(|offer| offer.with_version(version))
}
