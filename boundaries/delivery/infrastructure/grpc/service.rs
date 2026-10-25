use std::io;
use std::net::SocketAddr;
use std::sync::Arc;

use tokio::sync::watch;
use tonic::metadata::MetadataMap;
use tonic::transport::server::TcpIncoming;
use tonic::{Request, Response, Status};

use crate::di::ServiceModule;
use crate::pkg::application::Subject;
use crate::pkg::lifecycle::{Worker, wait_for_stop};
use crate::slices::delivery::applications::error::DeliveryApplicationError;
use crate::slices::delivery::applications::{
    cancel_delivery, create_delivery, get_courier_current_delivery, replan_delivery,
};
use crate::slices::delivery::di::DeliveryUseCases;
use crate::slices::delivery::domains::delivery::State;

use super::v1::delivery_service_server::{DeliveryService, DeliveryServiceServer};
use super::v1::{
    CancelDeliveryRequest, CancelDeliveryResponse, CreateDeliveryRequest, CreateDeliveryResponse,
    Delivery, DeliveryState, GetCourierCurrentDeliveryRequest, GetCourierCurrentDeliveryResponse,
    ReplanDeliveryRequest, ReplanDeliveryResponse,
};

/// Resolves only verified caller identities. Without identity configuration
/// the composition root keeps [`RejectingSubjectResolver`]; otherwise
/// [`super::ServiceTokenSubjectResolver`] verifies projected ServiceAccount
/// tokens (ADR 27).
pub trait SubjectResolver: Send + Sync + 'static {
    fn resolve(&self, metadata: &MetadataMap) -> Result<Subject, Status>;
}

pub struct RejectingSubjectResolver;

impl SubjectResolver for RejectingSubjectResolver {
    fn resolve(&self, _metadata: &MetadataMap) -> Result<Subject, Status> {
        Err(Status::unauthenticated(
            "verified caller identity is required",
        ))
    }
}

struct DeliveryGrpcService {
    module: Arc<ServiceModule>,
    subjects: Arc<dyn SubjectResolver>,
}

#[tonic::async_trait]
impl DeliveryService for DeliveryGrpcService {
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
        });
        Ok(Response::new(GetCourierCurrentDeliveryResponse {
            delivery,
        }))
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
                    pickup_address: request.pickup_address,
                    dropoff_address: request.dropoff_address,
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
}

impl DeliveryGrpcService {
    fn deliveries(&self) -> &dyn DeliveryUseCases {
        self.module.deliveries()
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

fn delivery_transport_error(error: DeliveryApplicationError) -> Status {
    match error {
        DeliveryApplicationError::PermissionDenied => Status::permission_denied(error.to_string()),
        DeliveryApplicationError::InvalidRequest(_) => Status::invalid_argument(error.to_string()),
        DeliveryApplicationError::DeliveryNotFound(_)
        | DeliveryApplicationError::DeliveryForOrderNotFound(_)
        | DeliveryApplicationError::CourierNotFound(_)
        | DeliveryApplicationError::DeliveryOfferNotFound(_) => {
            Status::not_found(error.to_string())
        }
        DeliveryApplicationError::DeliveryAlreadyExists(_) => {
            Status::already_exists(error.to_string())
        }
        DeliveryApplicationError::DeliveryPrecondition(_)
        | DeliveryApplicationError::CourierPrecondition(_)
        | DeliveryApplicationError::OfferPrecondition(_)
        | DeliveryApplicationError::CourierPositionUnknown(_) => {
            Status::failed_precondition(error.to_string())
        }
        // A lost optimistic concurrency race: the client retries the whole call.
        DeliveryApplicationError::ConcurrentModification => Status::aborted(error.to_string()),
        // An unknown commit outcome is resolved by an idempotent retry.
        DeliveryApplicationError::DependencyUnavailable(_)
        | DeliveryApplicationError::Transaction(_) => Status::unavailable(error.to_string()),
        DeliveryApplicationError::Internal(_) => Status::internal("internal error"),
    }
}

/// Tonic server integrated with the common process lifecycle.
pub struct GrpcWorker {
    incoming: TcpIncoming,
    module: Arc<ServiceModule>,
    subjects: Arc<dyn SubjectResolver>,
}

impl GrpcWorker {
    pub fn bind(
        addr: SocketAddr,
        module: Arc<ServiceModule>,
        subjects: Arc<dyn SubjectResolver>,
    ) -> io::Result<Self> {
        let incoming = TcpIncoming::bind(addr)?;
        println!("delivery gRPC listening on {}", incoming.local_addr()?);
        Ok(Self {
            incoming,
            module,
            subjects,
        })
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
