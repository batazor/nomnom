//! gRPC-клиент `authzed.api.v1.PermissionsService`: `CheckPermission`,
//! `WriteRelationships` и `ReadRelationships`.
//!
//! Порты авторизации и записи relationships синхронные, а use cases
//! вызываются из async handlers tonic. Поэтому клиент живёт в собственном
//! потоке с current-thread runtime: каждый вызов отправляет запрос в этот поток
//! и ждёт ответ, не вызывая `block_on` в чужом runtime. На multi-thread runtime
//! ожидание выполняется через `block_in_place`, чтобы не занимать async worker.
//!
//! Telemetry (docs/observability.md, «HTTP и gRPC»): вызов несёт OpenTelemetry
//! context вызывающего потока (span use case) в поток клиента; там каждый
//! RPC получает client span `authzed.api.v1.PermissionsService/<Method>`,
//! `traceparent` в metadata и измерение `rpc.client.call.duration`
//! (`pkg/telemetry/rpc`). Preshared key и тело запроса в telemetry не попадают.

mod config;
mod proto;

use std::fmt;
use std::sync::Arc;
use std::sync::mpsc as std_mpsc;
use std::time::{Duration, Instant};

use opentelemetry::Context;
use opentelemetry::global::BoxedTracer;

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
use crate::pkg::telemetry::rpc::{self, ClientCall, RpcMetrics};

pub use config::SpiceDbConfigError;
use config::{config_error, endpoint};
use proto::{allowed, from_proto, read_request, request, write_request};

/// Запас ожидания ответа сверх deadline запроса: deadline соблюдает поток
/// клиента, этот запас лишь страхует от его остановки.
const REPLY_GRACE: Duration = Duration::from_millis(500);

/// Решение SpiceDB по одному check; `Err` — решение не получено.
pub trait PermissionChecker: Send + Sync {
    /// `Ok(true)` только для `PERMISSIONSHIP_HAS_PERMISSION`.
    fn check(&self, check: &PermissionCheck) -> Result<bool, CallFailure>;
}

/// Вызов SpiceDB без ответа.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum CallFailure {
    /// Ответа нет в пределах timeout клиента, включая `DEADLINE_EXCEEDED`.
    Timeout(String),
    /// Ошибка транспорта или сервера.
    Failed(String),
}

impl fmt::Display for CallFailure {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Timeout(reason) | Self::Failed(reason) => formatter.write_str(reason),
        }
    }
}

type Reply<T> = std_mpsc::SyncSender<Result<T, CallFailure>>;

/// Запрос к потоку клиента и канал ответа.
enum Job {
    Check(CheckPermissionRequest, Reply<bool>),
    Write(WriteRelationshipsRequest, Reply<AuthorizationRevision>),
    Read(ReadRelationshipsRequest, Reply<RelationshipPage>),
}

const CHECK_PERMISSION: &str = "authzed.api.v1.PermissionsService/CheckPermission";
const WRITE_RELATIONSHIPS: &str = "authzed.api.v1.PermissionsService/WriteRelationships";
const READ_RELATIONSHIPS: &str = "authzed.api.v1.PermissionsService/ReadRelationships";

/// `DEADLINE_EXCEEDED`: the client deadline expired before a response.
const DEADLINE_EXCEEDED: i32 = 4;

/// Запрос вместе с context вызывающего потока.
struct Traced {
    job: Job,
    parent: Context,
}

/// Spans и метрики вызовов SpiceDB.
#[derive(Clone)]
pub struct ClientTelemetry {
    tracer: Arc<BoxedTracer>,
    metrics: RpcMetrics,
}

impl ClientTelemetry {
    /// Global providers (no-op until the pipeline starts).
    #[must_use]
    pub fn global() -> Self {
        Self::new(
            opentelemetry::global::tracer(rpc::INSTRUMENTATION),
            RpcMetrics::global(),
        )
    }

    #[must_use]
    pub fn new(tracer: BoxedTracer, metrics: RpcMetrics) -> Self {
        Self {
            tracer: Arc::new(tracer),
            metrics,
        }
    }
}

/// Telemetry одного RPC в потоке клиента.
struct CallTelemetry {
    telemetry: ClientTelemetry,
    call: ClientCall,
    context: Context,
    started: Instant,
}

impl CallTelemetry {
    fn start(
        telemetry: &ClientTelemetry,
        target: &(String, u16),
        method: &'static str,
        parent: &Context,
    ) -> Self {
        let call = ClientCall {
            method,
            server_address: target.0.clone(),
            server_port: target.1,
        };
        let context = call.start(&telemetry.tracer, parent);
        Self {
            telemetry: telemetry.clone(),
            call,
            context,
            started: Instant::now(),
        }
    }

    /// Request с `traceparent` client span в metadata.
    fn request<T>(&self, message: T) -> Request<T> {
        let mut request = Request::new(message);
        rpc::inject(&self.context, request.metadata_mut());
        request
    }

    fn finish(self, code: i32) {
        rpc::end_client_span(&self.context, code);
        self.telemetry
            .metrics
            .client_call(&self.call, code, self.started.elapsed());
    }
}

fn code_of<T>(outcome: &Result<Result<T, tonic::Status>, tokio::time::error::Elapsed>) -> i32 {
    match outcome {
        Ok(Ok(_)) => 0,
        Ok(Err(status)) => status.code() as i32,
        Err(_) => DEADLINE_EXCEEDED,
    }
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
    jobs: mpsc::UnboundedSender<Traced>,
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
        Self::connect_with_telemetry(config, ClientTelemetry::global())
    }

    /// [`Self::connect`] с заданными tracer и метриками (тесты).
    pub fn connect_with_telemetry(
        config: &SpiceDbConfig,
        telemetry: ClientTelemetry,
    ) -> Result<Self, SpiceDbConfigError> {
        if config.preshared_key.is_empty() {
            return Err(config_error("preshared key is required"));
        }
        if config.timeout.is_zero() {
            return Err(config_error("timeout must be positive"));
        }
        let endpoint = endpoint(config)?;
        let target = server_target(endpoint.uri())?;
        let key = PresharedKey(
            format!("Bearer {}", config.preshared_key)
                .parse()
                .map_err(|_| config_error("preshared key must be printable ASCII"))?,
        );
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|error| config_error(format!("start client runtime: {error}")))?;
        let (jobs, mut queue) = mpsc::unbounded_channel::<Traced>();
        let timeout = config.timeout;

        std::thread::Builder::new()
            .name("spicedb-client".to_owned())
            .spawn(move || {
                runtime.block_on(async move {
                    let channel = endpoint.connect_lazy();
                    let client = PermissionsServiceClient::with_interceptor(channel, key);
                    while let Some(Traced { job, parent }) = queue.recv().await {
                        let client = client.clone();
                        let call = Call {
                            telemetry: telemetry.clone(),
                            target: target.clone(),
                            parent,
                        };
                        tokio::spawn(serve(client, job, timeout, call));
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

/// `server.address` / `server.port` endpoint'а: хост и порт задаются явно.
fn server_target(uri: &tonic::codegen::http::Uri) -> Result<(String, u16), SpiceDbConfigError> {
    let host = uri
        .host()
        .ok_or_else(|| config_error(format!("SPICEDB__ENDPOINT {uri} has no host")))?;
    let port = uri
        .port_u16()
        .ok_or_else(|| config_error(format!("SPICEDB__ENDPOINT {uri} has no port")))?;
    Ok((host.to_owned(), port))
}

/// Telemetry context одного запроса.
struct Call {
    telemetry: ClientTelemetry,
    target: (String, u16),
    parent: Context,
}

impl Call {
    fn start(&self, method: &'static str) -> CallTelemetry {
        CallTelemetry::start(&self.telemetry, &self.target, method, &self.parent)
    }
}

/// Выполняет один запрос в потоке клиента в пределах `timeout`.
async fn serve(mut client: Client, job: Job, timeout: Duration, call: Call) {
    // Получатель мог уйти по своему deadline, поэтому ошибки отправки
    // ответа игнорируются.
    match job {
        Job::Check(request, reply) => {
            let telemetry = call.start(CHECK_PERMISSION);
            let outcome =
                tokio::time::timeout(timeout, client.check_permission(telemetry.request(request)))
                    .await;
            telemetry.finish(code_of(&outcome));
            let _ = reply.send(match outcome {
                Ok(Ok(response)) => Ok(allowed(response.get_ref())),
                Ok(Err(status)) => Err(failed("check", &status)),
                Err(_) => Err(CallFailure::Timeout(format!(
                    "spicedb check timed out after {timeout:?}"
                ))),
            });
        }
        Job::Write(request, reply) => {
            let telemetry = call.start(WRITE_RELATIONSHIPS);
            let outcome = tokio::time::timeout(
                timeout,
                client.write_relationships(telemetry.request(request)),
            )
            .await;
            telemetry.finish(code_of(&outcome));
            let _ = reply.send(match outcome {
                Ok(Ok(response)) => written_at(response.get_ref()).map_err(CallFailure::Failed),
                Ok(Err(status)) => Err(failed("write relationships", &status)),
                Err(_) => Err(CallFailure::Timeout(format!(
                    "spicedb write relationships timed out after {timeout:?}"
                ))),
            });
        }
        Job::Read(request, reply) => {
            let telemetry = call.start(READ_RELATIONSHIPS);
            let outcome =
                tokio::time::timeout(timeout, read_page(&mut client, telemetry.request(request)))
                    .await;
            telemetry.finish(code_of(&outcome));
            let _ = reply.send(match outcome {
                Ok(result) => result.map_err(|status| failed("read relationships", &status)),
                Err(_) => Err(CallFailure::Timeout(format!(
                    "spicedb read relationships timed out after {timeout:?}"
                ))),
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

fn failed(operation: &str, status: &tonic::Status) -> CallFailure {
    let reason = format!(
        "spicedb {operation} failed: {:?}: {}",
        status.code(),
        status.message()
    );
    // The request timeout of the channel (`Endpoint::timeout`) surfaces as
    // `CANCELLED` with the text of `TimeoutExpired`, without a source.
    let timed_out = status.code() == tonic::Code::DeadlineExceeded
        || (status.code() == tonic::Code::Cancelled
            && status.message() == tonic::TimeoutExpired(()).to_string());
    if timed_out {
        CallFailure::Timeout(reason)
    } else {
        CallFailure::Failed(reason)
    }
}

/// Читает одну страницу stream `ReadRelationships` целиком.
async fn read_page(
    client: &mut Client,
    request: Request<ReadRelationshipsRequest>,
) -> Result<RelationshipPage, tonic::Status> {
    let mut stream = client.read_relationships(request).await?.into_inner();
    let mut page = RelationshipPage::default();
    while let Some(response) = stream.message().await? {
        if let Some(relationship) = response.relationship.as_ref().and_then(from_proto) {
            page.relationships.push(relationship);
        }
        page.cursor = response.after_result_cursor.map(|cursor| cursor.token);
    }
    Ok(page)
}

impl SpiceDbClient {
    /// Отправляет запрос в поток клиента и ждёт ответ не дольше timeout.
    fn call<T>(
        &self,
        job: impl FnOnce(Reply<T>) -> Job,
        operation: &str,
    ) -> Result<T, CallFailure> {
        let (reply, receiver) = std_mpsc::sync_channel(1);
        self.jobs
            .send(Traced {
                job: job(reply),
                parent: Context::current(),
            })
            .map_err(|_| CallFailure::Failed("spicedb client stopped".to_owned()))?;

        let wait = || receiver.recv_timeout(self.timeout + REPLY_GRACE);
        let received = match Handle::try_current() {
            Ok(handle) if handle.runtime_flavor() == RuntimeFlavor::MultiThread => {
                tokio::task::block_in_place(wait)
            }
            _ => wait(),
        };
        received.map_err(|_| {
            CallFailure::Timeout(format!(
                "spicedb {operation} timed out after {:?}",
                self.timeout
            ))
        })?
    }
}

impl PermissionChecker for SpiceDbClient {
    fn check(&self, check: &PermissionCheck) -> Result<bool, CallFailure> {
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
        .map_err(|failure| failure.to_string())
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
        .map_err(|failure| failure.to_string())
    }
}

#[cfg(test)]
mod tests;
