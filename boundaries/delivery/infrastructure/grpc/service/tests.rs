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
use crate::infrastructure::repository::test_support::TestStore;
use crate::slices::courier::applications::{register_courier, set_courier_availability};
use crate::slices::courier::domains::courier::CourierId;
use crate::slices::delivery::applications::courier_locator::CourierCandidate;
use crate::slices::delivery::applications::error::DeliveryApplicationError;
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
        Arc::new(crate::di::build_with_locator(
            &Arc::new(TestStore::new()),
            StaticCourierLocator::new(Vec::new()),
        )),
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
    let module = Arc::new(crate::di::build_with_locator(
        &Arc::new(TestStore::new()),
        StaticCourierLocator::new(vec![CourierCandidate {
            courier_id: CourierId::new(courier),
            within_delivery_zone: true,
            distance_to_pickup_meters: 500,
            active_assignments: 0,
        }]),
    ));
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
                expected_pickup_at_epoch_seconds: None,
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
    assert_eq!(current.courier_status, CourierStatus::Available as i32);
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
                expected_delivered_at_epoch_seconds: None,
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
        Arc::new(crate::di::build_with_locator(
            &Arc::new(TestStore::new()),
            StaticCourierLocator::new(Vec::new()),
        )),
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
