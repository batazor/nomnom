//! gRPC-клиент `authzed.api.v1.PermissionsService`: `CheckPermission`,
//! `WriteRelationships` и `ReadRelationships`.
//!
//! Порты авторизации и записи relationships синхронные, а use cases
//! вызываются из async handlers tonic. Поэтому клиент живёт в собственном
//! потоке с current-thread runtime: каждый вызов отправляет запрос в этот поток
//! и ждёт ответ, не вызывая `block_on` в чужом runtime. На multi-thread runtime
//! ожидание выполняется через `block_in_place`, чтобы не занимать async worker.

use std::fmt;
use std::sync::mpsc as std_mpsc;
use std::time::Duration;

use tokio::runtime::{Handle, RuntimeFlavor};
use tokio::sync::mpsc;
use tonic::Request;
use tonic::metadata::{Ascii, MetadataValue};
use tonic::service::Interceptor;
use tonic::transport::{ClientTlsConfig, Endpoint};

use super::mapping::PermissionCheck;
use super::relationships::{
    Relationship, RelationshipGraph, RelationshipOperation, RelationshipPage,
};
use super::v1::check_permission_response::Permissionship;
use super::v1::consistency::Requirement;
use super::v1::permissions_service_client::PermissionsServiceClient;
use super::v1::relationship_update::Operation;
use super::v1::{
    CheckPermissionRequest, CheckPermissionResponse, Consistency, Cursor, ObjectReference,
    ReadRelationshipsRequest, RelationshipFilter, RelationshipUpdate, SubjectReference,
    WriteRelationshipsRequest,
};
use crate::pkg::config::SpiceDbConfig;

/// Запас ожидания ответа сверх deadline запроса: deadline соблюдает поток
/// клиента, этот запас лишь страхует от его остановки.
const REPLY_GRACE: Duration = Duration::from_millis(500);

/// Решение SpiceDB по одному check; `Err` — решение не получено.
pub trait PermissionChecker: Send + Sync {
    /// `Ok(true)` только для `PERMISSIONSHIP_HAS_PERMISSION`.
    fn check(&self, check: &PermissionCheck) -> Result<bool, String>;
}

/// Ошибка startup: невалидная конфигурация SpiceDB.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SpiceDbConfigError(String);

impl fmt::Display for SpiceDbConfigError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "spicedb: {}", self.0)
    }
}

impl std::error::Error for SpiceDbConfigError {}

impl SpiceDbConfigError {
    /// Пустой endpoint: процесс, проверяющий права, не стартует без SpiceDB.
    #[must_use]
    pub fn missing_endpoint(env: &str) -> Self {
        config_error(format!(
            "{env} is required: delivery checks permissions in SpiceDB"
        ))
    }
}

fn config_error(message: impl Into<String>) -> SpiceDbConfigError {
    SpiceDbConfigError(message.into())
}

type Reply<T> = std_mpsc::SyncSender<Result<T, String>>;

/// Запрос к потоку клиента и канал ответа.
enum Job {
    Check(CheckPermissionRequest, Reply<bool>),
    Write(WriteRelationshipsRequest, Reply<()>),
    Read(ReadRelationshipsRequest, Reply<RelationshipPage>),
}

/// Bearer preshared key в каждом запросе.
#[derive(Clone)]
struct PresharedKey(MetadataValue<Ascii>);

impl Interceptor for PresharedKey {
    fn call(&mut self, mut request: Request<()>) -> Result<Request<()>, tonic::Status> {
        request
            .metadata_mut()
            .insert("authorization", self.0.clone());
        Ok(request)
    }
}

/// Клиент SpiceDB процесса; клоны делят один поток, который завершается
/// вместе с последним экземпляром.
#[derive(Clone)]
pub struct SpiceDbClient {
    jobs: mpsc::UnboundedSender<Job>,
    timeout: Duration,
}

impl fmt::Debug for SpiceDbClient {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("SpiceDbClient")
            .field("timeout", &self.timeout)
            .finish_non_exhaustive()
    }
}

impl SpiceDbClient {
    /// Проверяет конфигурацию и запускает поток клиента. Соединение
    /// устанавливается лениво: недоступный SpiceDB не мешает startup, а
    /// отклоняет checks как unavailable.
    pub fn connect(config: &SpiceDbConfig) -> Result<Self, SpiceDbConfigError> {
        if config.preshared_key.is_empty() {
            return Err(config_error("preshared key is required"));
        }
        if config.timeout.is_zero() {
            return Err(config_error("timeout must be positive"));
        }
        let endpoint = endpoint(config)?;
        let key = PresharedKey(
            format!("Bearer {}", config.preshared_key)
                .parse()
                .map_err(|_| config_error("preshared key must be printable ASCII"))?,
        );
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|error| config_error(format!("start client runtime: {error}")))?;
        let (jobs, mut queue) = mpsc::unbounded_channel::<Job>();
        let timeout = config.timeout;

        std::thread::Builder::new()
            .name("spicedb-client".to_owned())
            .spawn(move || {
                runtime.block_on(async move {
                    let channel = endpoint.connect_lazy();
                    let client = PermissionsServiceClient::with_interceptor(channel, key);
                    while let Some(job) = queue.recv().await {
                        let client = client.clone();
                        tokio::spawn(serve(client, job, timeout));
                    }
                });
            })
            .map_err(|error| config_error(format!("start client thread: {error}")))?;

        Ok(Self { jobs, timeout })
    }
}

type Client = PermissionsServiceClient<
    tonic::service::interceptor::InterceptedService<tonic::transport::Channel, PresharedKey>,
>;

/// Выполняет один запрос в потоке клиента в пределах `timeout`.
async fn serve(mut client: Client, job: Job, timeout: Duration) {
    // Получатель мог уйти по своему deadline, поэтому ошибки отправки
    // ответа игнорируются.
    match job {
        Job::Check(request, reply) => {
            let outcome = tokio::time::timeout(timeout, client.check_permission(request)).await;
            let _ = reply.send(match outcome {
                Ok(Ok(response)) => Ok(allowed(response.get_ref())),
                Ok(Err(status)) => Err(failed("check", &status)),
                Err(_) => Err(format!("spicedb check timed out after {timeout:?}")),
            });
        }
        Job::Write(request, reply) => {
            let outcome = tokio::time::timeout(timeout, client.write_relationships(request)).await;
            let _ = reply.send(match outcome {
                Ok(Ok(_)) => Ok(()),
                Ok(Err(status)) => Err(failed("write relationships", &status)),
                Err(_) => Err(format!(
                    "spicedb write relationships timed out after {timeout:?}"
                )),
            });
        }
        Job::Read(request, reply) => {
            let outcome = tokio::time::timeout(timeout, read_page(&mut client, request)).await;
            let _ = reply.send(match outcome {
                Ok(result) => result,
                Err(_) => Err(format!(
                    "spicedb read relationships timed out after {timeout:?}"
                )),
            });
        }
    }
}

fn failed(operation: &str, status: &tonic::Status) -> String {
    format!(
        "spicedb {operation} failed: {:?}: {}",
        status.code(),
        status.message()
    )
}

/// Читает одну страницу stream `ReadRelationships` целиком.
async fn read_page(
    client: &mut Client,
    request: ReadRelationshipsRequest,
) -> Result<RelationshipPage, String> {
    let mut stream = client
        .read_relationships(request)
        .await
        .map_err(|status| failed("read relationships", &status))?
        .into_inner();
    let mut page = RelationshipPage::default();
    while let Some(response) = stream
        .message()
        .await
        .map_err(|status| failed("read relationships", &status))?
    {
        if let Some(relationship) = response.relationship.as_ref().and_then(from_proto) {
            page.relationships.push(relationship);
        }
        page.cursor = response.after_result_cursor.map(|cursor| cursor.token);
    }
    Ok(page)
}

impl SpiceDbClient {
    /// Отправляет запрос в поток клиента и ждёт ответ не дольше timeout.
    fn call<T>(&self, job: impl FnOnce(Reply<T>) -> Job, operation: &str) -> Result<T, String> {
        let (reply, receiver) = std_mpsc::sync_channel(1);
        self.jobs
            .send(job(reply))
            .map_err(|_| "spicedb client stopped".to_owned())?;

        let wait = || receiver.recv_timeout(self.timeout + REPLY_GRACE);
        let received = match Handle::try_current() {
            Ok(handle) if handle.runtime_flavor() == RuntimeFlavor::MultiThread => {
                tokio::task::block_in_place(wait)
            }
            _ => wait(),
        };
        received.map_err(|_| format!("spicedb {operation} timed out after {:?}", self.timeout))?
    }
}

impl PermissionChecker for SpiceDbClient {
    fn check(&self, check: &PermissionCheck) -> Result<bool, String> {
        self.call(|reply| Job::Check(request(check), reply), "check")
    }
}

impl RelationshipGraph for SpiceDbClient {
    fn write(
        &self,
        operation: RelationshipOperation,
        relationship: &Relationship,
    ) -> Result<(), String> {
        self.call(
            |reply| Job::Write(write_request(operation, relationship), reply),
            "write relationships",
        )
    }

    fn read_page(
        &self,
        resource_type: &str,
        relation: &str,
        cursor: Option<&str>,
        limit: u32,
    ) -> Result<RelationshipPage, String> {
        self.call(
            |reply| Job::Read(read_request(resource_type, relation, cursor, limit), reply),
            "read relationships",
        )
    }
}

fn write_request(
    operation: RelationshipOperation,
    relationship: &Relationship,
) -> WriteRelationshipsRequest {
    let operation = match operation {
        RelationshipOperation::Touch => Operation::Touch,
        RelationshipOperation::Delete => Operation::Delete,
    };
    WriteRelationshipsRequest {
        updates: vec![RelationshipUpdate {
            operation: operation as i32,
            relationship: Some(to_proto(relationship)),
        }],
        optional_preconditions: Vec::new(),
        optional_transaction_metadata: None,
    }
}

/// Reconciliation сравнивает граф с хранилищем, поэтому читает
/// `fully_consistent`, а не кэш `minimize_latency`.
fn read_request(
    resource_type: &str,
    relation: &str,
    cursor: Option<&str>,
    limit: u32,
) -> ReadRelationshipsRequest {
    ReadRelationshipsRequest {
        consistency: Some(Consistency {
            requirement: Some(Requirement::FullyConsistent(true)),
        }),
        relationship_filter: Some(RelationshipFilter {
            resource_type: resource_type.to_owned(),
            optional_relation: relation.to_owned(),
            ..RelationshipFilter::default()
        }),
        optional_limit: limit,
        optional_cursor: cursor.map(|token| Cursor {
            token: token.to_owned(),
        }),
    }
}

fn to_proto(relationship: &Relationship) -> super::v1::Relationship {
    super::v1::Relationship {
        resource: Some(ObjectReference {
            object_type: relationship.resource_type.clone(),
            object_id: relationship.resource_id.clone(),
        }),
        relation: relationship.relation.clone(),
        subject: Some(SubjectReference {
            object: Some(ObjectReference {
                object_type: relationship.subject_type.clone(),
                object_id: relationship.subject_id.clone(),
            }),
            optional_relation: String::new(),
        }),
        optional_caveat: None,
        optional_expires_at: None,
    }
}

/// `None` для relationship без resource или subject.
fn from_proto(relationship: &super::v1::Relationship) -> Option<Relationship> {
    let resource = relationship.resource.as_ref()?;
    let subject = relationship.subject.as_ref()?.object.as_ref()?;
    Some(Relationship {
        resource_type: resource.object_type.clone(),
        resource_id: resource.object_id.clone(),
        relation: relationship.relation.clone(),
        subject_type: subject.object_type.clone(),
        subject_id: subject.object_id.clone(),
    })
}

/// Только `HAS_PERMISSION` разрешает; `NO_PERMISSION`, conditional и
/// неизвестные значения — отказ.
fn allowed(response: &CheckPermissionResponse) -> bool {
    response.permissionship == Permissionship::HasPermission as i32
}

fn request(check: &PermissionCheck) -> CheckPermissionRequest {
    CheckPermissionRequest {
        consistency: Some(Consistency {
            requirement: Some(Requirement::MinimizeLatency(true)),
        }),
        resource: Some(ObjectReference {
            object_type: check.resource_type.to_owned(),
            object_id: check.resource_id.clone(),
        }),
        permission: check.permission.to_owned(),
        subject: Some(SubjectReference {
            object: Some(ObjectReference {
                object_type: check.subject_type.to_owned(),
                object_id: check.subject_id.clone(),
            }),
            optional_relation: String::new(),
        }),
        context: None,
        with_tracing: false,
    }
}

/// `host:port` дополняется схемой по `insecure`; явная схема должна ему
/// соответствовать, чтобы ключ не ушёл plaintext по ошибке.
fn endpoint(config: &SpiceDbConfig) -> Result<Endpoint, SpiceDbConfigError> {
    let raw = config.endpoint.trim();
    let url = match raw.split_once("://") {
        None if config.insecure => format!("http://{raw}"),
        None => format!("https://{raw}"),
        Some(("http", _)) if config.insecure => raw.to_owned(),
        Some(("https", _)) if !config.insecure => raw.to_owned(),
        Some((scheme, _)) => {
            return Err(config_error(format!(
                "endpoint scheme {scheme}:// does not match insecure = {}",
                config.insecure
            )));
        }
    };
    let endpoint = Endpoint::from_shared(url)
        .map_err(|error| config_error(format!("invalid endpoint {raw}: {error}")))?
        .connect_timeout(config.timeout)
        .timeout(config.timeout);
    if config.insecure {
        return Ok(endpoint);
    }
    endpoint
        .tls_config(ClientTlsConfig::new().with_webpki_roots())
        .map_err(|error| config_error(format!("tls: {error}")))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config(endpoint: &str, insecure: bool) -> SpiceDbConfig {
        SpiceDbConfig {
            endpoint: endpoint.to_owned(),
            preshared_key: "devkey".to_owned(),
            insecure,
            timeout: Duration::from_millis(200),
        }
    }

    fn response(permissionship: Permissionship) -> CheckPermissionResponse {
        CheckPermissionResponse {
            permissionship: permissionship as i32,
            ..CheckPermissionResponse::default()
        }
    }

    #[test]
    fn only_has_permission_allows() {
        assert!(allowed(&response(Permissionship::HasPermission)));
        assert!(!allowed(&response(Permissionship::NoPermission)));
        assert!(!allowed(&response(Permissionship::ConditionalPermission)));
        assert!(!allowed(&response(Permissionship::Unspecified)));
        assert!(!allowed(&CheckPermissionResponse {
            permissionship: 42,
            ..CheckPermissionResponse::default()
        }));
    }

    #[test]
    fn request_uses_minimize_latency_and_subject_object() {
        let request = request(&PermissionCheck {
            resource_type: "platform",
            resource_id: "nomnom".to_owned(),
            permission: "coordinate_delivery",
            subject_type: "service",
            subject_id: "orders-worker".to_owned(),
        });

        assert_eq!(
            request.consistency,
            Some(Consistency {
                requirement: Some(Requirement::MinimizeLatency(true))
            })
        );
        assert_eq!(request.permission, "coordinate_delivery");
        let resource = request.resource.expect("resource");
        assert_eq!(
            (resource.object_type.as_str(), resource.object_id.as_str()),
            ("platform", "nomnom")
        );
        let subject = request
            .subject
            .and_then(|subject| subject.object)
            .expect("subject");
        assert_eq!(
            (subject.object_type.as_str(), subject.object_id.as_str()),
            ("service", "orders-worker")
        );
    }

    #[test]
    fn endpoint_scheme_follows_insecure() {
        assert_eq!(
            endpoint(&config("localhost:50051", true))
                .expect("plaintext")
                .uri()
                .to_string(),
            "http://localhost:50051/"
        );
        assert_eq!(
            endpoint(&config("spicedb.example:443", false))
                .expect("tls")
                .uri()
                .to_string(),
            "https://spicedb.example:443/"
        );
    }

    #[test]
    fn rejects_scheme_conflicting_with_insecure() {
        assert!(endpoint(&config("http://spicedb:50051", false)).is_err());
        assert!(endpoint(&config("https://spicedb:50051", true)).is_err());
    }

    #[test]
    fn rejects_missing_key() {
        let mut config = config("localhost:50051", true);
        config.preshared_key.clear();

        assert!(SpiceDbClient::connect(&config).is_err());
    }

    #[test]
    fn unreachable_endpoint_is_an_error_not_a_decision() {
        let client = SpiceDbClient::connect(&config("127.0.0.1:1", true)).expect("client");
        let outcome = client.check(&PermissionCheck {
            resource_type: "platform",
            resource_id: "nomnom".to_owned(),
            permission: "coordinate_delivery",
            subject_type: "service",
            subject_id: "orders-worker".to_owned(),
        });

        assert!(outcome.is_err(), "{outcome:?}");
    }

    #[test]
    fn silent_endpoint_times_out_within_deadline() {
        // Принимает TCP, но не отвечает gRPC: ответа нет в пределах timeout.
        let silent = std::net::TcpListener::bind("127.0.0.1:0").expect("bind");
        let address = silent.local_addr().expect("addr");
        let client = SpiceDbClient::connect(&config(&address.to_string(), true)).expect("client");
        let started = std::time::Instant::now();

        let outcome = client.check(&PermissionCheck {
            resource_type: "platform",
            resource_id: "nomnom".to_owned(),
            permission: "coordinate_delivery",
            subject_type: "service",
            subject_id: "orders-worker".to_owned(),
        });

        assert!(outcome.is_err(), "{outcome:?}");
        assert!(started.elapsed() < Duration::from_millis(200) + REPLY_GRACE);
    }
}
