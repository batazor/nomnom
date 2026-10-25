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
mod tests {
    #[test]
    fn concurrent_modification_maps_to_aborted() {
        assert_eq!(
            delivery_transport_error(DeliveryApplicationError::ConcurrentModification).code(),
            tonic::Code::Aborted
        );
    }

    use super::super::v1::delivery_service_client::DeliveryServiceClient;
    use super::*;
    use crate::infrastructure::memory::StaticCourierLocator;
    use crate::slices::courier::applications::{register_courier, set_courier_availability};
    use crate::slices::courier::domains::courier::CourierId;
    use crate::slices::delivery::applications::courier_locator::CourierCandidate;
    use crate::slices::delivery::applications::{
        accept_delivery_offer, confirm_pickup, list_available_delivery_offers,
    };

    struct StaticSubjectResolver;

    impl SubjectResolver for StaticSubjectResolver {
        fn resolve(&self, _metadata: &MetadataMap) -> Result<Subject, Status> {
            Ok(Subject::User("courier-1".to_owned()))
        }
    }

    #[tokio::test]
    async fn serves_the_application_query_and_maps_not_found() {
        let worker = GrpcWorker::bind(
            ([127, 0, 0, 1], 0).into(),
            Arc::new(crate::di::build()),
            Arc::new(StaticSubjectResolver),
        )
        .expect("bind");
        let addr = worker.local_addr().expect("local addr");
        let (stop_tx, stop_rx) = watch::channel(false);
        let running = tokio::spawn(worker.run(stop_rx));

        let mut client = DeliveryServiceClient::connect(format!("http://{addr}"))
            .await
            .expect("connect");
        let error = client
            .get_courier_current_delivery(GetCourierCurrentDeliveryRequest {
                courier_id: "missing".to_owned(),
            })
            .await
            .expect_err("missing courier");
        assert_eq!(error.code(), tonic::Code::NotFound);

        stop_tx.send(true).expect("stop");
        running.await.expect("join").expect("graceful shutdown");
    }

    /// `orders` Temporal worker identity used by the fulfillment commands.
    struct OrdersWorkerResolver;

    impl SubjectResolver for OrdersWorkerResolver {
        fn resolve(&self, _metadata: &MetadataMap) -> Result<Subject, Status> {
            Ok(Subject::Service("orders-worker".to_owned()))
        }
    }

    struct Running {
        client: DeliveryServiceClient<tonic::transport::Channel>,
        module: Arc<ServiceModule>,
        stop: watch::Sender<bool>,
        handle: tokio::task::JoinHandle<io::Result<()>>,
    }

    impl Running {
        async fn stop(self) {
            self.stop.send(true).expect("stop");
            self.handle.await.expect("join").expect("graceful shutdown");
        }
    }

    /// Serves a process graph whose locator sees one available courier.
    async fn start_with_courier(courier: &str) -> Running {
        let module = Arc::new(crate::di::build_with_locator(StaticCourierLocator::new(
            vec![CourierCandidate {
                courier_id: CourierId::new(courier),
                within_delivery_zone: true,
                distance_to_pickup_meters: 500,
                active_assignments: 0,
            }],
        )));
        let couriers = module.couriers();
        couriers
            .register_courier(
                &Subject::User("operator-1".to_owned()),
                register_courier::Input {
                    courier_id: courier.to_owned(),
                },
            )
            .expect("courier registered");

        let worker = GrpcWorker::bind(
            ([127, 0, 0, 1], 0).into(),
            module.clone(),
            Arc::new(OrdersWorkerResolver),
        )
        .expect("bind");
        let addr = worker.local_addr().expect("local addr");
        let (stop, stop_rx) = watch::channel(false);
        let handle = tokio::spawn(worker.run(stop_rx));
        let client = DeliveryServiceClient::connect(format!("http://{addr}"))
            .await
            .expect("connect");
        Running {
            client,
            module,
            stop,
            handle,
        }
    }

    fn create_request(order: &str) -> CreateDeliveryRequest {
        CreateDeliveryRequest {
            order_id: order.to_owned(),
            pickup_address: "Restaurant street 1".to_owned(),
            dropoff_address: "Customer street 2".to_owned(),
        }
    }

    fn cancel_request(order: &str) -> CancelDeliveryRequest {
        CancelDeliveryRequest {
            order_id: order.to_owned(),
            reason: "order cancelled".to_owned(),
        }
    }

    /// Creates and replans the delivery over gRPC, then lets the offered
    /// courier accept it. Returns the delivery id.
    async fn assigned_over_grpc(running: &mut Running, order: &str, courier: &str) -> String {
        let delivery_id = running
            .client
            .create_delivery(create_request(order))
            .await
            .expect("created")
            .into_inner()
            .delivery_id;
        let replan = running
            .client
            .replan_delivery(ReplanDeliveryRequest {
                order_id: order.to_owned(),
            })
            .await
            .expect("replanned")
            .into_inner();
        assert_eq!(replan.delivery_id, delivery_id);
        assert_eq!(replan.offered_courier_id, courier);

        let courier_subject = Subject::User(courier.to_owned());
        let deliveries = running.module.deliveries();
        let offers = deliveries
            .list_available_delivery_offers(
                &courier_subject,
                list_available_delivery_offers::Input {
                    courier_id: courier.to_owned(),
                },
            )
            .expect("offers");
        let offer = offers.offers.first().expect("pending offer");
        deliveries
            .accept_delivery_offer(
                &courier_subject,
                accept_delivery_offer::Input {
                    offer_id: offer.offer_id.as_str().to_owned(),
                    courier_id: courier.to_owned(),
                },
            )
            .expect("accepted");
        delivery_id
    }

    #[tokio::test]
    async fn create_delivery_is_idempotent_by_order_id() {
        let mut running = start_with_courier("courier-1").await;

        let first = running
            .client
            .create_delivery(create_request("order-1"))
            .await
            .expect("created")
            .into_inner();
        assert!(first.created);
        let repeat = running
            .client
            .create_delivery(create_request("order-1"))
            .await
            .expect("repeat")
            .into_inner();
        assert!(!repeat.created);
        assert_eq!(repeat.delivery_id, first.delivery_id);

        let conflict = running
            .client
            .create_delivery(CreateDeliveryRequest {
                pickup_address: "Other street 9".to_owned(),
                ..create_request("order-1")
            })
            .await
            .expect_err("different data");
        assert_eq!(conflict.code(), tonic::Code::AlreadyExists);
        let invalid = running
            .client
            .create_delivery(create_request(" "))
            .await
            .expect_err("empty order id");
        assert_eq!(invalid.code(), tonic::Code::InvalidArgument);

        running.stop().await;
    }

    #[tokio::test]
    async fn replan_offers_the_candidate_or_nothing() {
        let mut running = start_with_courier("courier-1").await;
        running
            .client
            .create_delivery(create_request("order-1"))
            .await
            .expect("created");

        let replan = running
            .client
            .replan_delivery(ReplanDeliveryRequest {
                order_id: "order-1".to_owned(),
            })
            .await
            .expect("replanned")
            .into_inner();
        assert_eq!(replan.offered_courier_id, "courier-1");

        // The only candidate already has an offer of this delivery.
        let again = running
            .client
            .replan_delivery(ReplanDeliveryRequest {
                order_id: "order-1".to_owned(),
            })
            .await
            .expect("replanned without candidate")
            .into_inner();
        assert_eq!(again.offered_courier_id, "");

        let missing = running
            .client
            .replan_delivery(ReplanDeliveryRequest {
                order_id: "missing".to_owned(),
            })
            .await
            .expect_err("unknown order");
        assert_eq!(missing.code(), tonic::Code::NotFound);

        running.stop().await;
    }

    /// AC-455
    #[tokio::test]
    async fn cancel_assigned_delivery_releases_courier_and_repeat_succeeds() {
        let mut running = start_with_courier("courier-1").await;
        let delivery_id = assigned_over_grpc(&mut running, "order-1", "courier-1").await;

        let cancelled = running
            .client
            .cancel_delivery(cancel_request("order-1"))
            .await
            .expect("cancelled")
            .into_inner();
        assert_eq!(
            cancelled,
            CancelDeliveryResponse {
                delivery_id: delivery_id.clone(),
                cancelled: true,
                rejection_reason: String::new(),
            }
        );

        let current = running
            .client
            .get_courier_current_delivery(GetCourierCurrentDeliveryRequest {
                courier_id: "courier-1".to_owned(),
            })
            .await
            .expect("current delivery")
            .into_inner();
        assert_eq!(current.delivery, None);
        let couriers = running.module.couriers();
        let availability = couriers
            .set_courier_availability(
                &Subject::User("courier-1".to_owned()),
                set_courier_availability::Input {
                    courier_id: "courier-1".to_owned(),
                    availability: set_courier_availability::Availability::Available,
                },
            )
            .expect("courier released");
        assert!(!availability.changed, "courier is already available");

        let repeat = running
            .client
            .cancel_delivery(cancel_request("order-1"))
            .await
            .expect("repeat succeeds")
            .into_inner();
        assert!(repeat.cancelled);
        assert_eq!(repeat.delivery_id, delivery_id);

        running.stop().await;
    }

    /// AC-456
    #[tokio::test]
    async fn cancel_after_pickup_is_a_business_rejection() {
        let mut running = start_with_courier("courier-1").await;
        let delivery_id = assigned_over_grpc(&mut running, "order-1", "courier-1").await;
        let deliveries = running.module.deliveries();
        deliveries
            .confirm_pickup(
                &Subject::User("courier-1".to_owned()),
                confirm_pickup::Input {
                    delivery_id: delivery_id.clone(),
                    courier_id: "courier-1".to_owned(),
                },
            )
            .expect("picked up");

        let response = running
            .client
            .cancel_delivery(cancel_request("order-1"))
            .await
            .expect("business rejection is not a gRPC error")
            .into_inner();
        assert_eq!(
            response,
            CancelDeliveryResponse {
                delivery_id,
                cancelled: false,
                rejection_reason: "already_picked_up".to_owned(),
            }
        );

        let missing = running
            .client
            .cancel_delivery(cancel_request("missing"))
            .await
            .expect_err("unknown order");
        assert_eq!(missing.code(), tonic::Code::NotFound);

        running.stop().await;
    }

    /// Delegates to the real token resolver and records what the handler got.
    struct RecordingResolver {
        inner: super::super::ServiceTokenSubjectResolver,
        resolved: std::sync::Mutex<Vec<Subject>>,
    }

    impl SubjectResolver for RecordingResolver {
        fn resolve(&self, metadata: &MetadataMap) -> Result<Subject, Status> {
            let subject = self.inner.resolve(metadata)?;
            self.resolved
                .lock()
                .expect("resolved lock")
                .push(subject.clone());
            Ok(subject)
        }
    }

    #[tokio::test]
    async fn service_token_reaches_the_handler_as_service_subject() {
        let vectors = super::super::identity::fixtures::vectors();
        let resolver = Arc::new(RecordingResolver {
            inner: super::super::identity::fixtures::resolver(),
            resolved: std::sync::Mutex::new(Vec::new()),
        });
        let worker = GrpcWorker::bind(
            ([127, 0, 0, 1], 0).into(),
            Arc::new(crate::di::build()),
            resolver.clone(),
        )
        .expect("bind");
        let addr = worker.local_addr().expect("local addr");
        let (stop_tx, stop_rx) = watch::channel(false);
        let running = tokio::spawn(worker.run(stop_rx));
        let mut client = DeliveryServiceClient::connect(format!("http://{addr}"))
            .await
            .expect("connect");

        let mut request = tonic::Request::new(create_request("order-identity"));
        request.metadata_mut().insert(
            "authorization",
            format!("Bearer {}", vectors.token("valid"))
                .parse()
                .expect("metadata"),
        );
        let created = client
            .create_delivery(request)
            .await
            .expect("verified service call handled")
            .into_inner();
        assert!(!created.delivery_id.is_empty());
        assert_eq!(
            *resolver.resolved.lock().expect("resolved lock"),
            vec![Subject::Service("orders-worker".to_owned())]
        );

        let anonymous = client
            .create_delivery(create_request("order-anonymous"))
            .await
            .expect_err("missing token");
        assert_eq!(anonymous.code(), tonic::Code::Unauthenticated);

        stop_tx.send(true).expect("stop");
        running.await.expect("join").expect("graceful shutdown");
    }

    #[tokio::test]
    async fn rejects_unverified_callers_by_default() {
        let worker = GrpcWorker::bind(
            ([127, 0, 0, 1], 0).into(),
            Arc::new(crate::di::build()),
            Arc::new(RejectingSubjectResolver),
        )
        .expect("bind");
        let addr = worker.local_addr().expect("local addr");
        let (stop_tx, stop_rx) = watch::channel(false);
        let running = tokio::spawn(worker.run(stop_rx));

        let mut client = DeliveryServiceClient::connect(format!("http://{addr}"))
            .await
            .expect("connect");
        let error = client
            .get_courier_current_delivery(GetCourierCurrentDeliveryRequest {
                courier_id: "courier-1".to_owned(),
            })
            .await
            .expect_err("caller rejected");
        assert_eq!(error.code(), tonic::Code::Unauthenticated);

        stop_tx.send(true).expect("stop");
        running.await.expect("join").expect("graceful shutdown");
    }
}
