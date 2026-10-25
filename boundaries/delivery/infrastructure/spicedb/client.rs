//! gRPC-клиент `authzed.api.v1.PermissionsService`: `CheckPermission`,
//! `WriteRelationships` и `ReadRelationships`.
//!
//! Порты авторизации и записи relationships синхронные, а use cases
//! вызываются из async handlers tonic. Поэтому клиент живёт в собственном
//! потоке с current-thread runtime: каждый вызов отправляет запрос в этот поток
//! и ждёт ответ, не вызывая `block_on` в чужом runtime. На multi-thread runtime
//! ожидание выполняется через `block_in_place`, чтобы не занимать async worker.

mod config;
mod proto;

use std::fmt;
use std::sync::mpsc as std_mpsc;
use std::time::Duration;

use tokio::runtime::{Handle, RuntimeFlavor};
use tokio::sync::mpsc;
use tonic::Request;
use tonic::metadata::{Ascii, MetadataValue};
use tonic::service::Interceptor;

use super::mapping::PermissionCheck;
use super::relationships::{
    Relationship, RelationshipGraph, RelationshipOperation, RelationshipPage,
};
use super::v1::permissions_service_client::PermissionsServiceClient;
use super::v1::{
    CheckPermissionRequest, ReadRelationshipsRequest, WriteRelationshipsRequest,
    WriteRelationshipsResponse,
};
use crate::pkg::config::SpiceDbConfig;
use crate::pkg::persistence::AuthorizationRevision;

pub use config::SpiceDbConfigError;
use config::{config_error, endpoint};
use proto::{allowed, from_proto, read_request, request, write_request};

/// Запас ожидания ответа сверх deadline запроса: deadline соблюдает поток
/// клиента, этот запас лишь страхует от его остановки.
const REPLY_GRACE: Duration = Duration::from_millis(500);

/// Решение SpiceDB по одному check; `Err` — решение не получено.
pub trait PermissionChecker: Send + Sync {
    /// `Ok(true)` только для `PERMISSIONSHIP_HAS_PERMISSION`.
    fn check(&self, check: &PermissionCheck) -> Result<bool, String>;
}

type Reply<T> = std_mpsc::SyncSender<Result<T, String>>;

/// Запрос к потоку клиента и канал ответа.
enum Job {
    Check(CheckPermissionRequest, Reply<bool>),
    Write(WriteRelationshipsRequest, Reply<AuthorizationRevision>),
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
                Ok(Ok(response)) => written_at(response.get_ref()),
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

/// Revision of a write: `written_at` ZedToken; a response without it is an
/// error, because the use case could not store a revision to check against.
fn written_at(response: &WriteRelationshipsResponse) -> Result<AuthorizationRevision, String> {
    response
        .written_at
        .as_ref()
        .map(|token| token.token.as_str())
        .filter(|token| !token.is_empty())
        .map(AuthorizationRevision::new)
        .ok_or_else(|| "spicedb write relationships returned no written_at".to_owned())
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
    ) -> Result<AuthorizationRevision, String> {
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

#[cfg(test)]
mod tests;
