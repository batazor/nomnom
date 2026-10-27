use std::io;
use std::net::SocketAddr;
use std::sync::Arc;

use tokio::sync::watch;
use tonic::metadata::MetadataMap;
use tonic::transport::server::TcpIncoming;
use tonic::{Request, Response, Status};

use super::tracing::GrpcTraceLayer;
use crate::di::ServiceModule;
use crate::pkg::application::Subject;
use crate::pkg::lifecycle::{Worker, wait_for_stop};
use crate::slices::courier::applications::{
    register_courier, retire_courier, set_courier_availability, update_courier_location,
};
use crate::slices::courier::di::CourierUseCases;
use crate::slices::courier::domains::courier::{CourierStatus as DomainCourierStatus, Location};
use crate::slices::delivery::applications::{
    accept_delivery_offer, cancel_delivery, confirm_delivery, confirm_pickup, create_delivery,
    get_courier_current_delivery, get_courier_pool, get_order_courier, get_order_tracking,
    list_available_delivery_offers, reject_delivery_offer, replan_delivery,
    report_delivery_failure,
};
use crate::slices::delivery::di::DeliveryUseCases;
use crate::slices::delivery::domains::delivery::State;
use crate::slices::simulator::applications::set_simulator_enabled;
use crate::slices::simulator::di::SimulatorUseCases;

use super::v1::delivery_service_server::{DeliveryService, DeliveryServiceServer};
use super::v1::{
    AcceptDeliveryOfferRequest, AcceptDeliveryOfferResponse, CancelDeliveryRequest,
    CancelDeliveryResponse, ConfirmDeliveryRequest, ConfirmDeliveryResponse, ConfirmPickupRequest,
    ConfirmPickupResponse, CourierAvailability, CourierLocation, CourierStatus,
    CreateDeliveryRequest, CreateDeliveryResponse, Delivery, DeliveryOffer, DeliveryState,
    GeoPoint, GetCourierCurrentDeliveryRequest, GetCourierCurrentDeliveryResponse,
    GetCourierPoolRequest, GetCourierPoolResponse, GetOrderCourierRequest, GetOrderCourierResponse,
    GetOrderTrackingRequest, GetOrderTrackingResponse, GetSimulatorRequest, GetSimulatorResponse,
    ListAvailableDeliveryOffersRequest, ListAvailableDeliveryOffersResponse, PoolCourier,
    RegisterCourierRequest, RegisterCourierResponse, RejectDeliveryOfferRequest,
    RejectDeliveryOfferResponse, ReplanDeliveryRequest, ReplanDeliveryResponse,
    ReportDeliveryFailureRequest, ReportDeliveryFailureResponse, RetireCourierRequest,
    RetireCourierResponse, SetCourierAvailabilityRequest, SetCourierAvailabilityResponse,
    SetSimulatorEnabledRequest, SetSimulatorEnabledResponse, UpdateCourierLocationRequest,
    UpdateCourierLocationResponse, VirtualCity,
};

mod status;

use status::{courier_transport_error, delivery_transport_error, simulator_transport_error};

/// Resolves only verified caller identities;
/// [`super::ServiceTokenSubjectResolver`] verifies projected ServiceAccount
/// tokens (ADR 27).
pub trait SubjectResolver: Send + Sync + 'static {
    fn resolve(&self, metadata: &MetadataMap) -> Result<Subject, Status>;
}

struct DeliveryGrpcService {
    module: Arc<ServiceModule>,
    subjects: Arc<dyn SubjectResolver>,
}

#[tonic::async_trait]
impl DeliveryService for DeliveryGrpcService {
    async fn get_simulator(
        &self,
        request: Request<GetSimulatorRequest>,
    ) -> Result<Response<GetSimulatorResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let output = self
            .simulator()
            .get_simulator(&subject)
            .map_err(simulator_transport_error)?;
        Ok(Response::new(GetSimulatorResponse {
            enabled: output.enabled,
        }))
    }

    async fn set_simulator_enabled(
        &self,
        request: Request<SetSimulatorEnabledRequest>,
    ) -> Result<Response<SetSimulatorEnabledResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let enabled = request.into_inner().enabled;
        let output = self
            .simulator()
            .set_simulator_enabled(&subject, set_simulator_enabled::Input { enabled })
            .map_err(simulator_transport_error)?;
        Ok(Response::new(SetSimulatorEnabledResponse {
            enabled: output.enabled,
        }))
    }

    async fn get_courier_current_delivery(
        &self,
        request: Request<GetCourierCurrentDeliveryRequest>,
    ) -> Result<Response<GetCourierCurrentDeliveryResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let courier_id = request.into_inner().courier_id;
        let output = self
            .deliveries()
            .get_courier_current_delivery(
                &subject,
                get_courier_current_delivery::Input { courier_id },
            )
            .map_err(delivery_transport_error)?;

        let delivery = output.current_delivery.map(|view| Delivery {
            delivery_id: view.delivery_id.as_str().to_owned(),
            order_id: view.order_id.as_str().to_owned(),
            courier_id: output.courier_id.as_str().to_owned(),
            state: delivery_state(view.state) as i32,
            market_id: view.market_id.as_str().to_owned(),
        });
        Ok(Response::new(GetCourierCurrentDeliveryResponse {
            delivery,
            courier_status: courier_status(output.courier_status) as i32,
        }))
    }

    async fn get_order_courier(
        &self,
        request: Request<GetOrderCourierRequest>,
    ) -> Result<Response<GetOrderCourierResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let order_id = request.into_inner().order_id;
        let output = self
            .deliveries()
            .get_order_courier(&subject, get_order_courier::Input { order_id })
            .map_err(delivery_transport_error)?;
        Ok(Response::new(output.courier.map_or_else(
            GetOrderCourierResponse::default,
            |courier| GetOrderCourierResponse {
                courier_id: courier.courier_id.as_str().to_owned(),
                name: courier.name.as_str().to_owned(),
            },
        )))
    }

    async fn get_order_tracking(
        &self,
        request: Request<GetOrderTrackingRequest>,
    ) -> Result<Response<GetOrderTrackingResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let order_id = request.into_inner().order_id;
        let output = self
            .deliveries()
            .get_order_tracking(&subject, get_order_tracking::Input { order_id })
            .map_err(delivery_transport_error)?;
        Ok(Response::new(output.tracking.map_or_else(
            GetOrderTrackingResponse::default,
            order_tracking,
        )))
    }

    async fn create_delivery(
        &self,
        request: Request<CreateDeliveryRequest>,
    ) -> Result<Response<CreateDeliveryResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let output = self
            .deliveries()
            .create_delivery(
                &subject,
                create_delivery::Input {
                    order_id: request.order_id,
                    market_id: request.market_id,
                },
            )
            .map_err(delivery_transport_error)?;
        Ok(Response::new(CreateDeliveryResponse {
            delivery_id: output.delivery_id.as_str().to_owned(),
            created: output.created,
        }))
    }

    async fn replan_delivery(
        &self,
        request: Request<ReplanDeliveryRequest>,
    ) -> Result<Response<ReplanDeliveryResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let order_id = request.into_inner().order_id;
        let output = self
            .deliveries()
            .replan_delivery(&subject, replan_delivery::Input { order_id })
            .map_err(delivery_transport_error)?;
        Ok(Response::new(ReplanDeliveryResponse {
            delivery_id: output.delivery_id.as_str().to_owned(),
            offered_courier_id: output
                .offer
                .map(|offer| offer.courier_id.as_str().to_owned())
                .unwrap_or_default(),
        }))
    }

    async fn cancel_delivery(
        &self,
        request: Request<CancelDeliveryRequest>,
    ) -> Result<Response<CancelDeliveryResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let output = self
            .deliveries()
            .cancel_delivery(
                &subject,
                cancel_delivery::Input {
                    order_id: request.order_id,
                    reason: request.reason,
                },
            )
            .map_err(delivery_transport_error)?;
        // A business rejection is a successful response, not a gRPC error.
        let (cancelled, rejection_reason) = match output.outcome {
            cancel_delivery::Outcome::Cancelled { .. } => (true, String::new()),
            cancel_delivery::Outcome::Rejected(rejection) => (false, rejection.as_str().to_owned()),
        };
        Ok(Response::new(CancelDeliveryResponse {
            delivery_id: output.delivery_id.as_str().to_owned(),
            cancelled,
            rejection_reason,
        }))
    }

    async fn register_courier(
        &self,
        request: Request<RegisterCourierRequest>,
    ) -> Result<Response<RegisterCourierResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let output = self
            .couriers()
            .register_courier(
                &subject,
                register_courier::Input {
                    courier_id: request.courier_id,
                    name: request.name,
                    market_id: request.market_id,
                },
            )
            .map_err(courier_transport_error)?;
        Ok(Response::new(RegisterCourierResponse {
            courier_id: output.courier_id.as_str().to_owned(),
            status: courier_status(output.status) as i32,
            name: output.name.as_str().to_owned(),
        }))
    }

    async fn retire_courier(
        &self,
        request: Request<RetireCourierRequest>,
    ) -> Result<Response<RetireCourierResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let courier_id = request.into_inner().courier_id;
        let output = self
            .couriers()
            .retire_courier(&subject, retire_courier::Input { courier_id })
            .map_err(courier_transport_error)?;
        Ok(Response::new(RetireCourierResponse {
            courier_id: output.courier_id.as_str().to_owned(),
            changed: output.changed,
        }))
    }

    async fn get_courier_pool(
        &self,
        request: Request<GetCourierPoolRequest>,
    ) -> Result<Response<GetCourierPoolResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let market_id = request.into_inner().market_id;
        let pool = self
            .deliveries()
            .get_courier_pool(&subject, get_courier_pool::Input { market_id })
            .map_err(delivery_transport_error)?;
        let count =
            |value: u64| i64::try_from(value).map_err(|_| Status::internal("internal error"));
        Ok(Response::new(GetCourierPoolResponse {
            size: count(pool.size)?,
            on_line: count(pool.on_line)?,
            busy: count(pool.busy)?,
            waiting: count(pool.waiting)?,
            couriers: pool
                .couriers
                .into_iter()
                .map(|courier| PoolCourier {
                    courier_id: courier.courier_id.as_str().to_owned(),
                    name: courier.name.as_str().to_owned(),
                    status: courier_status(courier.status) as i32,
                    pending_offer_id: courier
                        .pending_offer_id
                        .map(|id| id.as_str().to_owned())
                        .unwrap_or_default(),
                    active_delivery_id: courier
                        .active_delivery_id
                        .map(|id| id.as_str().to_owned())
                        .unwrap_or_default(),
                    active_order_id: courier
                        .active_order_id
                        .map(|id| id.as_str().to_owned())
                        .unwrap_or_default(),
                    offered_order_id: courier
                        .offered_order_id
                        .map(|id| id.as_str().to_owned())
                        .unwrap_or_default(),
                })
                .collect(),
        }))
    }

    async fn update_courier_location(
        &self,
        request: Request<UpdateCourierLocationRequest>,
    ) -> Result<Response<UpdateCourierLocationResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let position = self
            .couriers()
            .update_courier_location(
                &subject,
                update_courier_location::Input {
                    courier_id: request.courier_id,
                    latitude: request.latitude,
                    longitude: request.longitude,
                },
            )
            .map_err(courier_transport_error)?
            .position;
        Ok(Response::new(UpdateCourierLocationResponse {
            courier_id: position.courier_id.as_str().to_owned(),
            latitude: position.location.latitude(),
            longitude: position.location.longitude(),
            recorded_at_epoch_seconds: position.recorded_at_epoch_seconds,
        }))
    }

    async fn set_courier_availability(
        &self,
        request: Request<SetCourierAvailabilityRequest>,
    ) -> Result<Response<SetCourierAvailabilityResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let availability = match CourierAvailability::try_from(request.availability) {
            Ok(CourierAvailability::Available) => set_courier_availability::Availability::Available,
            Ok(CourierAvailability::Unavailable) => {
                set_courier_availability::Availability::Unavailable
            }
            Ok(CourierAvailability::Unspecified) | Err(_) => {
                return Err(Status::invalid_argument(
                    "invalid request: availability must be available or unavailable",
                ));
            }
        };
        let output = self
            .couriers()
            .set_courier_availability(
                &subject,
                set_courier_availability::Input {
                    courier_id: request.courier_id,
                    availability,
                },
            )
            .map_err(courier_transport_error)?;
        Ok(Response::new(SetCourierAvailabilityResponse {
            courier_id: output.courier_id.as_str().to_owned(),
            status: courier_status(output.status) as i32,
            changed: output.changed,
        }))
    }

    async fn list_available_delivery_offers(
        &self,
        request: Request<ListAvailableDeliveryOffersRequest>,
    ) -> Result<Response<ListAvailableDeliveryOffersResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let courier_id = request.into_inner().courier_id;
        let output = self
            .deliveries()
            .list_available_delivery_offers(
                &subject,
                list_available_delivery_offers::Input { courier_id },
            )
            .map_err(delivery_transport_error)?;
        Ok(Response::new(ListAvailableDeliveryOffersResponse {
            courier_id: output.courier_id.as_str().to_owned(),
            offers: output
                .offers
                .into_iter()
                .map(|offer| DeliveryOffer {
                    offer_id: offer.offer_id.as_str().to_owned(),
                    delivery_id: offer.delivery_id.as_str().to_owned(),
                    order_id: offer.order_id.as_str().to_owned(),
                    expires_at_epoch_seconds: offer.expires_at_epoch_seconds,
                })
                .collect(),
        }))
    }

    async fn accept_delivery_offer(
        &self,
        request: Request<AcceptDeliveryOfferRequest>,
    ) -> Result<Response<AcceptDeliveryOfferResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let output = self
            .deliveries()
            .accept_delivery_offer(
                &subject,
                accept_delivery_offer::Input {
                    offer_id: request.offer_id,
                    courier_id: request.courier_id,
                    expected_pickup_at_epoch_seconds: request.expected_pickup_at.map(epoch_seconds),
                },
            )
            .map_err(delivery_transport_error)?;
        Ok(Response::new(AcceptDeliveryOfferResponse {
            offer_id: output.offer_id.as_str().to_owned(),
            delivery_id: output.delivery_id.as_str().to_owned(),
            courier_id: output.courier_id.as_str().to_owned(),
            already_accepted: output.already_accepted,
        }))
    }

    async fn reject_delivery_offer(
        &self,
        request: Request<RejectDeliveryOfferRequest>,
    ) -> Result<Response<RejectDeliveryOfferResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let output = self
            .deliveries()
            .reject_delivery_offer(
                &subject,
                reject_delivery_offer::Input {
                    offer_id: request.offer_id,
                    courier_id: request.courier_id,
                },
            )
            .map_err(delivery_transport_error)?;
        Ok(Response::new(RejectDeliveryOfferResponse {
            offer_id: output.offer_id.as_str().to_owned(),
            delivery_id: output.delivery_id.as_str().to_owned(),
            changed: output.changed,
        }))
    }

    async fn confirm_pickup(
        &self,
        request: Request<ConfirmPickupRequest>,
    ) -> Result<Response<ConfirmPickupResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let output = self
            .deliveries()
            .confirm_pickup(
                &subject,
                confirm_pickup::Input {
                    delivery_id: request.delivery_id,
                    courier_id: request.courier_id,
                    expected_delivered_at_epoch_seconds: request
                        .expected_delivered_at
                        .map(epoch_seconds),
                },
            )
            .map_err(delivery_transport_error)?;
        Ok(Response::new(ConfirmPickupResponse {
            delivery_id: output.delivery_id.as_str().to_owned(),
            state: delivery_state(output.state) as i32,
            changed: output.changed,
        }))
    }

    async fn confirm_delivery(
        &self,
        request: Request<ConfirmDeliveryRequest>,
    ) -> Result<Response<ConfirmDeliveryResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        let output = self
            .deliveries()
            .confirm_delivery(
                &subject,
                confirm_delivery::Input {
                    delivery_id: request.delivery_id,
                    courier_id: request.courier_id,
                },
            )
            .map_err(delivery_transport_error)?;
        Ok(Response::new(ConfirmDeliveryResponse {
            delivery_id: output.delivery_id.as_str().to_owned(),
            state: delivery_state(output.state) as i32,
            changed: output.changed,
        }))
    }

    async fn report_delivery_failure(
        &self,
        request: Request<ReportDeliveryFailureRequest>,
    ) -> Result<Response<ReportDeliveryFailureResponse>, Status> {
        let subject = self.subjects.resolve(request.metadata())?;
        let request = request.into_inner();
        // No courier in the request: an authorized dispatcher reports.
        let reporter = if request.courier_id.trim().is_empty() {
            report_delivery_failure::Reporter::Dispatcher
        } else {
            report_delivery_failure::Reporter::Courier(request.courier_id)
        };
        let output = self
            .deliveries()
            .report_delivery_failure(
                &subject,
                report_delivery_failure::Input {
                    delivery_id: request.delivery_id,
                    reporter,
                    reason: request.reason,
                    detail: Some(request.detail),
                },
            )
            .map_err(delivery_transport_error)?;
        Ok(Response::new(ReportDeliveryFailureResponse {
            delivery_id: output.delivery_id.as_str().to_owned(),
            reason: output.reason.as_str().to_owned(),
            detail: output.reason.detail().unwrap_or_default().to_owned(),
            released_courier_id: output.released_courier_id.as_str().to_owned(),
        }))
    }
}

impl DeliveryGrpcService {
    fn deliveries(&self) -> &dyn DeliveryUseCases {
        self.module.deliveries()
    }

    fn couriers(&self) -> &dyn CourierUseCases {
        self.module.couriers()
    }

    fn simulator(&self) -> &dyn SimulatorUseCases {
        self.module.simulator()
    }
}

/// Expected time of a request in whole epoch seconds: a fraction rounds up,
/// so a moment in the future stays in the future.
fn epoch_seconds(timestamp: prost_types::Timestamp) -> i64 {
    timestamp
        .seconds
        .saturating_add(i64::from(timestamp.nanos > 0))
}

fn courier_status(status: DomainCourierStatus) -> CourierStatus {
    match status {
        DomainCourierStatus::Available => CourierStatus::Available,
        DomainCourierStatus::Busy => CourierStatus::Busy,
        DomainCourierStatus::Unavailable => CourierStatus::Unavailable,
    }
}

fn delivery_state(state: State) -> DeliveryState {
    match state {
        State::Waiting => DeliveryState::SearchingCourier,
        State::Assigned => DeliveryState::Assigned,
        State::PickedUp => DeliveryState::PickedUp,
        State::Delivered => DeliveryState::Completed,
        State::Failed => DeliveryState::Failed,
        // The contract has no cancelled value yet; a cancelled delivery is
        // never a courier's current delivery, so this is not served today.
        State::Cancelled => DeliveryState::Cancelled,
    }
}

/// Tonic server integrated with the common process lifecycle.
pub struct GrpcWorker {
    incoming: TcpIncoming,
    module: Arc<ServiceModule>,
    subjects: Arc<dyn SubjectResolver>,
    tracing: GrpcTraceLayer,
}

impl GrpcWorker {
    pub fn bind(
        addr: SocketAddr,
        module: Arc<ServiceModule>,
        subjects: Arc<dyn SubjectResolver>,
    ) -> io::Result<Self> {
        let incoming = TcpIncoming::bind(addr)?;
        tracing::info!(operation = "grpc.listen", address = %incoming.local_addr()?, "delivery gRPC listening");
        Ok(Self {
            incoming,
            module,
            subjects,
            tracing: GrpcTraceLayer::global(),
        })
    }

    /// Replaces the server span layer (tests use an in-memory exporter).
    #[must_use]
    pub fn with_tracing(mut self, tracing: GrpcTraceLayer) -> Self {
        self.tracing = tracing;
        self
    }

    pub fn local_addr(&self) -> io::Result<SocketAddr> {
        self.incoming.local_addr()
    }
}

impl Worker for GrpcWorker {
    async fn run(self, mut stop: watch::Receiver<bool>) -> io::Result<()> {
        let service = DeliveryGrpcService {
            module: self.module,
            subjects: self.subjects,
        };
        tonic::transport::Server::builder()
            .layer(self.tracing)
            .add_service(DeliveryServiceServer::new(service))
            .serve_with_incoming_shutdown(self.incoming, async move {
                wait_for_stop(&mut stop).await;
            })
            .await
            .map_err(io::Error::other)
    }
}

#[cfg(test)]
mod tests;

#[cfg(test)]
mod courier_api_tests;

fn geo_point(location: Location) -> GeoPoint {
    GeoPoint {
        latitude: location.latitude(),
        longitude: location.longitude(),
    }
}

fn order_tracking(tracking: get_order_tracking::OrderTrackingView) -> GetOrderTrackingResponse {
    GetOrderTrackingResponse {
        tracking_channel: tracking.channel.as_str().to_owned(),
        city: Some(VirtualCity {
            origin: Some(geo_point(tracking.city.origin())),
            blocks: tracking.city.blocks(),
            block_meters: tracking.city.block_meters(),
        }),
        pickup: Some(geo_point(tracking.pickup)),
        dropoff: Some(geo_point(tracking.dropoff)),
        courier_position: tracking.courier_position.map(|position| CourierLocation {
            latitude: position.location.latitude(),
            longitude: position.location.longitude(),
            recorded_at_epoch_seconds: position.recorded_at_epoch_seconds,
        }),
    }
}
