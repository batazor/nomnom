//! Courier API over gRPC (FR-580, FR-581): every RPC reaches its application
//! handler with the handler's authorization, Unit of Work and Outbox.
//! Authorization grants only what the scenario needs, so a missing relation
//! is a denial as in SpiceDB.

use super::super::v1::delivery_service_client::DeliveryServiceClient;
use super::*;
use crate::infrastructure::memory::{
    MemoryStore, RecordingRelationships, RelationshipWrite, StaticAuthorization,
};
use crate::pkg::application::RelationshipWriteError;
use crate::pkg::persistence::AuthorizationRevision;
use crate::slices::courier::applications::authorization::{CourierPermission, CourierResource};
use crate::slices::courier::applications::relationships::CourierRelationshipWriter;
use crate::slices::courier::domains::courier::{CourierId, CourierRepository};
use crate::slices::delivery::applications::authorization::{DeliveryPermission, DeliveryResource};
use crate::slices::delivery::applications::relationships::DeliveryRelationshipWriter;
use crate::slices::delivery::domains::delivery::{
    DeliveryId, DeliveryRepository, DomainEvent, OrderId,
};

const SUBJECT_HEADER: &str = "x-test-subject";
const OPERATOR: &str = "operator-1";
const ORDERS_WORKER: &str = "orders-worker";

/// Subject named by the request (`user:<id>` or `service:<id>`), as the
/// identity resolver would produce it from a token and an assertion.
struct HeaderSubjectResolver;

impl SubjectResolver for HeaderSubjectResolver {
    fn resolve(&self, metadata: &MetadataMap) -> Result<Subject, Status> {
        let value = metadata
            .get(SUBJECT_HEADER)
            .and_then(|value| value.to_str().ok())
            .ok_or_else(|| Status::unauthenticated("no subject"))?;
        match value.split_once(':') {
            Some(("user", id)) => Ok(Subject::User(id.to_owned())),
            Some(("service", id)) => Ok(Subject::Service(id.to_owned())),
            _ => Err(Status::unauthenticated("unknown subject")),
        }
    }
}

fn as_user<T>(user: &str, message: T) -> Request<T> {
    let mut request = Request::new(message);
    request.metadata_mut().insert(
        SUBJECT_HEADER,
        format!("user:{user}").parse().expect("metadata"),
    );
    request
}

fn as_orders_worker<T>(message: T) -> Request<T> {
    let mut request = Request::new(message);
    request.metadata_mut().insert(
        SUBJECT_HEADER,
        format!("service:{ORDERS_WORKER}")
            .parse()
            .expect("metadata"),
    );
    request
}

/// Relationship writes recorded by the process graph and read by the test.
struct SharedRelationships(Arc<RecordingRelationships>);

impl CourierRelationshipWriter for SharedRelationships {
    fn register_courier(
        &self,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.0.register_courier(courier_id)
    }
}

impl DeliveryRelationshipWriter for SharedRelationships {
    fn assign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.0.assign_courier(delivery_id, courier_id)
    }

    fn unassign_courier(
        &self,
        delivery_id: &DeliveryId,
        courier_id: &CourierId,
    ) -> Result<AuthorizationRevision, RelationshipWriteError> {
        self.0.unassign_courier(delivery_id, courier_id)
    }
}

/// Grants of the scenario: the operator registers couriers, `orders-worker`
/// creates and replans deliveries, and each courier user has `courier#act`
/// on itself only.
fn authorization(couriers: &[&str], orders: &[&str]) -> StaticAuthorization {
    let authorization = StaticAuthorization::grants_only();
    authorization.grant_courier(
        Subject::User(OPERATOR.to_owned()),
        CourierPermission::RegisterCourier,
        CourierResource::Platform,
    );
    let worker = Subject::Service(ORDERS_WORKER.to_owned());
    authorization.grant_delivery(
        worker.clone(),
        DeliveryPermission::CreateDelivery,
        DeliveryResource::Platform,
    );
    for order in orders {
        authorization.grant_delivery(
            worker.clone(),
            DeliveryPermission::ReplanDelivery,
            DeliveryResource::Order(OrderId::new(*order)),
        );
    }
    for courier in couriers {
        let subject = Subject::User((*courier).to_owned());
        for permission in [
            CourierPermission::SetCourierAvailability,
            CourierPermission::UpdateCourierLocation,
            CourierPermission::ViewCourierCurrentDelivery,
        ] {
            authorization.grant_courier(
                subject.clone(),
                permission,
                CourierResource::Courier(CourierId::new(*courier)),
            );
        }
        for permission in [
            DeliveryPermission::ListAvailableDeliveryOffers,
            DeliveryPermission::AcceptDeliveryOffer,
            DeliveryPermission::RejectDeliveryOffer,
            DeliveryPermission::ConfirmPickup,
            DeliveryPermission::ConfirmDelivery,
            DeliveryPermission::ReportDeliveryFailure,
        ] {
            authorization.grant_delivery(
                subject.clone(),
                permission,
                DeliveryResource::Courier(CourierId::new(*courier)),
            );
        }
    }
    authorization
}

struct Api {
    client: DeliveryServiceClient<tonic::transport::Channel>,
    store: Arc<MemoryStore>,
    relationships: Arc<RecordingRelationships>,
    stop: watch::Sender<bool>,
    handle: tokio::task::JoinHandle<io::Result<()>>,
}

impl Api {
    async fn start(authorization: StaticAuthorization) -> Self {
        let store = Arc::new(MemoryStore::new());
        let relationships = Arc::new(RecordingRelationships::new());
        let module = crate::di::build_over(
            &store,
            Box::new(authorization),
            Box::new(SharedRelationships(Arc::clone(&relationships))),
        );
        let worker = GrpcWorker::bind(
            ([127, 0, 0, 1], 0).into(),
            Arc::new(module),
            Arc::new(HeaderSubjectResolver),
        )
        .expect("bind");
        let addr = worker.local_addr().expect("local addr");
        let (stop, stop_rx) = watch::channel(false);
        let handle = tokio::spawn(worker.run(stop_rx));
        let client = DeliveryServiceClient::connect(format!("http://{addr}"))
            .await
            .expect("connect");
        Self {
            client,
            store,
            relationships,
            stop,
            handle,
        }
    }

    async fn stop(self) {
        self.stop.send(true).expect("stop");
        self.handle.await.expect("join").expect("graceful shutdown");
    }

    /// Operator registers the courier; the courier reports a position and
    /// goes on line, so the locator sees it.
    async fn courier_on_line(&mut self, courier: &str) {
        self.client
            .register_courier(as_user(
                OPERATOR,
                RegisterCourierRequest {
                    courier_id: courier.to_owned(),
                },
            ))
            .await
            .expect("registered");
        self.client
            .update_courier_location(as_user(
                courier,
                UpdateCourierLocationRequest {
                    courier_id: courier.to_owned(),
                    latitude: 52.37,
                    longitude: 4.89,
                },
            ))
            .await
            .expect("position recorded");
        let availability = self
            .client
            .set_courier_availability(as_user(
                courier,
                SetCourierAvailabilityRequest {
                    courier_id: courier.to_owned(),
                    availability: CourierAvailability::Available as i32,
                },
            ))
            .await
            .expect("available")
            .into_inner();
        assert_eq!(availability.status, CourierStatus::Available as i32);
    }

    /// `orders-worker` creates the delivery and replans it, so the only
    /// courier on line receives an offer. Returns the delivery id.
    async fn offered(&mut self, order: &str) -> String {
        let delivery_id = self
            .client
            .create_delivery(as_orders_worker(CreateDeliveryRequest {
                order_id: order.to_owned(),
                pickup_address: "Restaurant street 1".to_owned(),
                dropoff_address: "Customer street 2".to_owned(),
            }))
            .await
            .expect("created")
            .into_inner()
            .delivery_id;
        self.client
            .replan_delivery(as_orders_worker(ReplanDeliveryRequest {
                order_id: order.to_owned(),
            }))
            .await
            .expect("replanned");
        delivery_id
    }

    async fn offers(&mut self, courier: &str) -> Vec<DeliveryOffer> {
        self.client
            .list_available_delivery_offers(as_user(
                courier,
                ListAvailableDeliveryOffersRequest {
                    courier_id: courier.to_owned(),
                },
            ))
            .await
            .expect("offers")
            .into_inner()
            .offers
    }

    async fn accept(&mut self, courier: &str, offer_id: String) -> AcceptDeliveryOfferResponse {
        self.client
            .accept_delivery_offer(as_user(
                courier,
                AcceptDeliveryOfferRequest {
                    offer_id,
                    courier_id: courier.to_owned(),
                },
            ))
            .await
            .expect("accepted")
            .into_inner()
    }

    fn delivery_state(&self, delivery_id: &str) -> State {
        DeliveryRepository::get(&*self.store, &DeliveryId::new(delivery_id))
            .expect("read delivery")
            .expect("delivery exists")
            .state()
    }

    fn courier_status(&self, courier: &str) -> DomainCourierStatus {
        CourierRepository::get(&*self.store, &CourierId::new(courier))
            .expect("read courier")
            .expect("courier exists")
            .status()
    }
}

/// Published facts of the assignment lifecycle in Outbox order.
fn lifecycle_facts(store: &MemoryStore) -> Vec<&'static str> {
    store
        .outbox_messages()
        .into_iter()
        .filter_map(|message| match message.event {
            DomainEvent::CourierAssignedToDelivery { .. } => Some("assigned"),
            DomainEvent::DeliveryPickedUp { .. } => Some("picked_up"),
            DomainEvent::DeliveryCompleted { .. } => Some("completed"),
            _ => None,
        })
        .collect()
}

/// AC-730
#[tokio::test]
async fn courier_accepts_picks_up_and_completes_the_delivery() {
    let mut api = Api::start(authorization(&["courier-1"], &["order-1"])).await;
    api.courier_on_line("courier-1").await;
    let delivery_id = api.offered("order-1").await;

    let offers = api.offers("courier-1").await;
    assert_eq!(offers.len(), 1);
    assert_eq!(offers[0].delivery_id, delivery_id);
    assert_eq!(offers[0].pickup_address, "Restaurant street 1");
    let accepted = api.accept("courier-1", offers[0].offer_id.clone()).await;
    assert_eq!(
        accepted,
        AcceptDeliveryOfferResponse {
            offer_id: offers[0].offer_id.clone(),
            delivery_id: delivery_id.clone(),
            courier_id: "courier-1".to_owned(),
            already_accepted: false,
        }
    );
    let pickup = api
        .client
        .confirm_pickup(as_user(
            "courier-1",
            ConfirmPickupRequest {
                delivery_id: delivery_id.clone(),
                courier_id: "courier-1".to_owned(),
            },
        ))
        .await
        .expect("picked up")
        .into_inner();
    assert_eq!(pickup.state, DeliveryState::PickedUp as i32);
    assert!(pickup.changed);
    let completion = api
        .client
        .confirm_delivery(as_user(
            "courier-1",
            ConfirmDeliveryRequest {
                delivery_id: delivery_id.clone(),
                courier_id: "courier-1".to_owned(),
            },
        ))
        .await
        .expect("delivered")
        .into_inner();
    assert_eq!(
        completion,
        ConfirmDeliveryResponse {
            delivery_id: delivery_id.clone(),
            state: DeliveryState::Completed as i32,
            changed: true,
        }
    );

    assert_eq!(api.delivery_state(&delivery_id), State::Delivered);
    assert_eq!(
        api.courier_status("courier-1"),
        DomainCourierStatus::Available
    );
    assert_eq!(
        lifecycle_facts(&api.store),
        vec!["assigned", "picked_up", "completed"]
    );
    api.stop().await;
}

/// AC-731
#[tokio::test]
async fn another_courier_cannot_confirm_pickup() {
    let mut api = Api::start(authorization(&["courier-1", "courier-2"], &["order-1"])).await;
    api.courier_on_line("courier-1").await;
    let delivery_id = api.offered("order-1").await;
    let offer_id = api.offers("courier-1").await[0].offer_id.clone();
    api.accept("courier-1", offer_id).await;
    let outbox_before = api.store.outbox_messages();

    let denied = api
        .client
        .confirm_pickup(as_user(
            "courier-2",
            ConfirmPickupRequest {
                delivery_id: delivery_id.clone(),
                courier_id: "courier-1".to_owned(),
            },
        ))
        .await
        .expect_err("no courier#act on courier-1");

    assert_eq!(denied.code(), tonic::Code::PermissionDenied);
    assert_eq!(api.delivery_state(&delivery_id), State::Assigned);
    assert_eq!(api.store.outbox_messages(), outbox_before);
    api.stop().await;
}

/// AC-732
#[tokio::test]
async fn operator_registers_a_courier_for_the_user() {
    let mut api = Api::start(authorization(&[], &[])).await;

    let registered = api
        .client
        .register_courier(as_user(
            OPERATOR,
            RegisterCourierRequest {
                courier_id: "user-7".to_owned(),
            },
        ))
        .await
        .expect("registered")
        .into_inner();

    assert_eq!(
        registered,
        RegisterCourierResponse {
            courier_id: "user-7".to_owned(),
            status: CourierStatus::Available as i32,
        }
    );
    // courier:user-7#self@user:user-7 grants courier#act to the user.
    assert_eq!(
        api.relationships.writes(),
        vec![RelationshipWrite::RegisterCourier(CourierId::new("user-7"))]
    );
    let repeat = api
        .client
        .register_courier(as_user(
            OPERATOR,
            RegisterCourierRequest {
                courier_id: "user-7".to_owned(),
            },
        ))
        .await
        .expect_err("already registered");
    assert_eq!(repeat.code(), tonic::Code::AlreadyExists);
    api.stop().await;
}

/// AC-733
#[tokio::test]
async fn non_operator_cannot_register_a_courier() {
    let mut api = Api::start(authorization(&["user-7"], &[])).await;

    let denied = api
        .client
        .register_courier(as_user(
            "user-7",
            RegisterCourierRequest {
                courier_id: "user-7".to_owned(),
            },
        ))
        .await
        .expect_err("no platform operator relation");

    assert_eq!(denied.code(), tonic::Code::PermissionDenied);
    assert_eq!(
        CourierRepository::get(&*api.store, &CourierId::new("user-7")),
        Ok(None)
    );
    assert!(api.relationships.writes().is_empty());
    api.stop().await;
}

#[tokio::test]
async fn rejection_and_failure_report_reach_their_handlers() {
    let mut api = Api::start(authorization(&["courier-1"], &["order-1", "order-2"])).await;
    api.courier_on_line("courier-1").await;
    let rejected_delivery = api.offered("order-1").await;
    let offer_id = api.offers("courier-1").await[0].offer_id.clone();
    let rejected = api
        .client
        .reject_delivery_offer(as_user(
            "courier-1",
            RejectDeliveryOfferRequest {
                offer_id: offer_id.clone(),
                courier_id: "courier-1".to_owned(),
            },
        ))
        .await
        .expect("rejected")
        .into_inner();
    assert_eq!(
        rejected,
        RejectDeliveryOfferResponse {
            offer_id,
            delivery_id: rejected_delivery,
            changed: true,
        }
    );

    let delivery_id = api.offered("order-2").await;
    let offer_id = api.offers("courier-1").await[0].offer_id.clone();
    api.accept("courier-1", offer_id).await;
    let failed = api
        .client
        .report_delivery_failure(as_user(
            "courier-1",
            ReportDeliveryFailureRequest {
                delivery_id: delivery_id.clone(),
                courier_id: "courier-1".to_owned(),
                reason: "customer_unreachable".to_owned(),
                detail: "no answer at the door".to_owned(),
            },
        ))
        .await
        .expect("failure reported")
        .into_inner();
    assert_eq!(
        failed,
        ReportDeliveryFailureResponse {
            delivery_id: delivery_id.clone(),
            reason: "customer_unreachable".to_owned(),
            detail: "no answer at the door".to_owned(),
            released_courier_id: "courier-1".to_owned(),
        }
    );
    assert_eq!(api.delivery_state(&delivery_id), State::Failed);

    // Without a courier the dispatcher reports and needs dispatch_delivery.
    let dispatcher = api
        .client
        .report_delivery_failure(as_user(
            "courier-1",
            ReportDeliveryFailureRequest {
                delivery_id,
                courier_id: String::new(),
                reason: "other".to_owned(),
                detail: String::new(),
            },
        ))
        .await
        .expect_err("courier is not a dispatcher");
    assert_eq!(dispatcher.code(), tonic::Code::PermissionDenied);
    api.stop().await;
}

#[tokio::test]
async fn invalid_courier_requests_are_invalid_arguments() {
    let mut api = Api::start(authorization(&["courier-1"], &[])).await;
    api.courier_on_line("courier-1").await;

    let unspecified = api
        .client
        .set_courier_availability(as_user(
            "courier-1",
            SetCourierAvailabilityRequest {
                courier_id: "courier-1".to_owned(),
                availability: CourierAvailability::Unspecified as i32,
            },
        ))
        .await
        .expect_err("availability required");
    assert_eq!(unspecified.code(), tonic::Code::InvalidArgument);
    let off_the_map = api
        .client
        .update_courier_location(as_user(
            "courier-1",
            UpdateCourierLocationRequest {
                courier_id: "courier-1".to_owned(),
                latitude: 91.0,
                longitude: 0.0,
            },
        ))
        .await
        .expect_err("latitude out of range");
    assert_eq!(off_the_map.code(), tonic::Code::InvalidArgument);
    let unknown = api
        .client
        .accept_delivery_offer(as_user(
            "courier-1",
            AcceptDeliveryOfferRequest {
                offer_id: "missing".to_owned(),
                courier_id: "courier-1".to_owned(),
            },
        ))
        .await
        .expect_err("unknown offer");
    assert_eq!(unknown.code(), tonic::Code::NotFound);
    api.stop().await;
}

/// A courier call carries the user assertion of a delegate: the handler
/// authorizes the asserted user, not the calling service.
#[tokio::test]
async fn user_assertion_reaches_the_courier_handler_as_user_subject() {
    let vectors = super::super::identity::fixtures::user_vectors();
    let user = vectors.expected_subject.clone();
    let store = Arc::new(MemoryStore::new());
    let module = crate::di::build_over(
        &store,
        Box::new(authorization(&[user.as_str()], &[])),
        Box::new(RecordingRelationships::new()),
    );
    let worker = GrpcWorker::bind(
        ([127, 0, 0, 1], 0).into(),
        Arc::new(module),
        Arc::new(super::super::identity::fixtures::user_resolver()),
    )
    .expect("bind");
    let addr = worker.local_addr().expect("local addr");
    let (stop, stop_rx) = watch::channel(false);
    let handle = tokio::spawn(worker.run(stop_rx));
    let mut client = DeliveryServiceClient::connect(format!("http://{addr}"))
        .await
        .expect("connect");

    let request = |courier_id: &str| {
        let mut request = Request::new(ListAvailableDeliveryOffersRequest {
            courier_id: courier_id.to_owned(),
        });
        let metadata = request.metadata_mut();
        metadata.insert(
            "authorization",
            format!("Bearer {}", vectors.delegate_token)
                .parse()
                .expect("metadata"),
        );
        metadata.insert(
            "x-nomnom-user-assertion",
            vectors
                .token(&vectors.accepted[0])
                .parse()
                .expect("metadata"),
        );
        request
    };
    let own = client
        .list_available_delivery_offers(request(&user))
        .await
        .expect("own offers")
        .into_inner();
    assert_eq!(own.courier_id, user);
    assert!(own.offers.is_empty());
    let foreign = client
        .list_available_delivery_offers(request("courier-1"))
        .await
        .expect_err("offers of another courier");
    assert_eq!(foreign.code(), tonic::Code::PermissionDenied);

    stop.send(true).expect("stop");
    handle.await.expect("join").expect("graceful shutdown");
}
