use super::*;
use crate::slices::courier::domains::courier::{CourierRepository, CourierStatus};
use crate::slices::delivery::domains::delivery::DeliveryRepository;

#[derive(Debug, PartialEq)]
enum TestError {
    Work,
    Transaction(UnitOfWorkError),
}

impl From<UnitOfWorkError> for TestError {
    fn from(error: UnitOfWorkError) -> Self {
        Self::Transaction(error)
    }
}

fn courier(id: &str) -> Courier {
    Courier::new(CourierId::new(id))
}

#[test]
fn unit_of_work_commits_successful_work() {
    let store = MemoryStore::new();
    let result: Result<(), TestError> = store
        .run(|| CourierRepository::add(&store, &courier("courier-1")).map_err(|_| TestError::Work));

    assert_eq!(result, Ok(()));
    assert!(
        CourierRepository::get(&store, &CourierId::new("courier-1"))
            .expect("read")
            .is_some()
    );
    assert_eq!((store.commits(), store.rollbacks()), (1, 0));
}

#[test]
fn unit_of_work_rolls_back_failed_work() {
    let store = MemoryStore::new();
    CourierRepository::add(&store, &courier("courier-1")).expect("seed");

    let result: Result<(), TestError> = store.run(|| {
        let busy = Courier::restore(CourierId::new("courier-1"), CourierStatus::Busy);
        CourierRepository::save(&store, &busy).map_err(|_| TestError::Work)?;
        CourierRepository::add(&store, &courier("courier-2")).map_err(|_| TestError::Work)?;
        Err(TestError::Work)
    });

    assert_eq!(result, Err(TestError::Work));
    assert_eq!(
        CourierRepository::get(&store, &CourierId::new("courier-1")),
        Ok(Some(courier("courier-1")))
    );
    assert_eq!(
        CourierRepository::get(&store, &CourierId::new("courier-2")),
        Ok(None)
    );
    assert_eq!((store.commits(), store.rollbacks()), (0, 1));
}

#[test]
fn unit_of_work_rolls_back_on_commit_failure() {
    let store = MemoryStore::new();
    store.fail_next_commit(UnitOfWorkError::CommitFailed("disk full".to_owned()));

    let result: Result<(), TestError> = store
        .run(|| CourierRepository::add(&store, &courier("courier-1")).map_err(|_| TestError::Work));

    assert_eq!(
        result,
        Err(TestError::Transaction(UnitOfWorkError::CommitFailed(
            "disk full".to_owned()
        )))
    );
    assert_eq!(
        CourierRepository::get(&store, &CourierId::new("courier-1")),
        Ok(None)
    );
    let next: Result<(), TestError> = store.run(|| Ok(()));
    assert_eq!(next, Ok(()));
}

#[test]
fn delivery_uniqueness_covers_order_id() {
    let store = MemoryStore::new();
    let delivery = |id: &str| {
        Delivery::new(
            DeliveryId::new(id),
            OrderId::new("order-1"),
            Address::new("a").expect("address"),
            Address::new("b").expect("address"),
        )
    };
    DeliveryRepository::add(&store, &delivery("delivery-1")).expect("first");
    assert_eq!(
        DeliveryRepository::add(&store, &delivery("delivery-2")),
        Err(RepositoryError::Conflict)
    );
}

#[test]
fn stale_save_is_a_concurrent_modification() {
    let store = MemoryStore::new();
    CourierRepository::add(&store, &courier("courier-1")).expect("seed");
    let first = CourierRepository::get(&store, &CourierId::new("courier-1"))
        .expect("read")
        .expect("courier");
    let second = first.clone();

    CourierRepository::save(&store, &first).expect("first writer wins");
    assert_eq!(
        CourierRepository::save(&store, &second),
        Err(RepositoryError::ConcurrentModification)
    );
    assert_eq!(
        CourierRepository::save(&store, &courier("courier-2")),
        Err(RepositoryError::Conflict)
    );
}

#[test]
fn unavailable_store_rejects_every_port() {
    let store = MemoryStore::new();
    store.set_unavailable(true);
    assert!(matches!(
        CourierRepository::get(&store, &CourierId::new("courier-1")),
        Err(RepositoryError::Unavailable(_))
    ));
    assert!(matches!(
        DeliveryRepository::get(&store, &DeliveryId::new("delivery-1")),
        Err(RepositoryError::Unavailable(_))
    ));
}
