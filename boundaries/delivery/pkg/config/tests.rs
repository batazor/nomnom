use super::*;
use std::cell::RefCell;
use std::fs;
use std::path::PathBuf;

/// Обязательные секции процесса `delivery` тестовых файлов (под `spec:`).
const IDENTITY_YAML: &str = "  identity:
    issuer: https://kubernetes.default.svc.cluster.local
    audience: nomnom.delivery
    jwksFile: /etc/nomnom/identity/jwks.json
    serviceAccounts: nomnom-orders/orders-worker=orders-worker
    userIssuer: nomnom-bff
    userJwksFile: /etc/nomnom/user-identity/jwks.json
    userDelegates: bff
";
const SPICEDB_YAML: &str = "  spicedb:\n    endpoint: spicedb:50051\n";
const TEMPORAL_YAML: &str =
    "  temporal:\n    hostPort: temporal:7233\n    namespace: default\n    connectTimeout: 10s\n";
const SIMULATOR_YAML: &str =
    "  simulator:\n    interval: 5s\n    minDelay: 30s\n    maxDelay: 2m\n    trackInterval: 1s\n";
const TRACKING_YAML: &str = "  tracking:
    cityOriginLatitude: 52.515
    cityOriginLongitude: 13.385
    cityBlocks: 12
    blockMeters: 150.0
    persistInterval: 15s
";
const KAFKA_YAML: &str =
    "  kafka:\n    brokers: kafka:9092\n    consumerGroup: delivery\n    initialOffset: oldest\n";
const SCHEMA_REGISTRY_YAML: &str = "  schemaRegistry:\n    url: http://kafka:8081\n";
const DELIVERY_ESTIMATE_YAML: &str = "  deliveryEstimate:\n    sampleSize: 50\n    baselineCourierLead: 15m\n    baselineTransit: 20m\n";

/// Секции без значений по умолчанию и их начало в файле.
const REQUIRED_SECTIONS: [(&str, &str); 8] = [
    ("  identity:", IDENTITY_YAML),
    ("  spicedb:", SPICEDB_YAML),
    ("  temporal:", TEMPORAL_YAML),
    ("  simulator:", SIMULATOR_YAML),
    ("  tracking:", TRACKING_YAML),
    ("  kafka:", KAFKA_YAML),
    ("  schemaRegistry:", SCHEMA_REGISTRY_YAML),
    ("  deliveryEstimate:", DELIVERY_ESTIMATE_YAML),
];

const DATABASE_URI: &str = "postgresql://u:p@db:5432/delivery";
const VALKEY_URL: &str = "redis://delivery:p@valkey:6379";

/// Секреты и имя pod процесса.
fn process_env(name: &str) -> Option<String> {
    match name {
        DATABASE_URI_ENV => Some(DATABASE_URI),
        VALKEY_URL_ENV => Some(VALKEY_URL),
        SPICEDB_PRESHARED_KEY_ENV => Some("key"),
        POD_NAME_ENV => Some("delivery-7d9f"),
        _ => None,
    }
    .map(str::to_owned)
}

fn header(role: &str) -> String {
    format!("apiVersion: {API_VERSION}\nkind: {KIND}\nmetadata:\n  name: {role}\n")
}

/// Временный файл с уникальным именем; удаляется при drop.
struct ConfigFile(PathBuf);

impl ConfigFile {
    fn new(name: &str, contents: &str) -> Self {
        let path = std::env::temp_dir().join(format!(
            "nomnom-delivery-{name}-{}.yaml",
            std::process::id()
        ));
        fs::write(&path, contents).expect("write config");
        Self(path)
    }
}

impl Drop for ConfigFile {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}

/// Файл роли `delivery` с секциями `sections` под `spec:`; недостающие
/// обязательные секции берутся из [`REQUIRED_SECTIONS`].
fn delivery_file(sections: &str) -> String {
    let mut spec = sections.to_owned();
    for (start, section) in REQUIRED_SECTIONS {
        if !spec.contains(start) {
            spec.push_str(section);
        }
    }
    format!("{}spec:\n{spec}", header(SERVER_ROLE))
}

fn load_yaml(name: &str, sections: &str) -> Result<AppConfig, ConfigError> {
    let file = ConfigFile::new(name, &delivery_file(sections));
    load_from(Some(&file.0), process_env)
}

fn load_error(name: &str, sections: &str) -> String {
    load_yaml(name, sections)
        .expect_err("config must be rejected")
        .to_string()
}

/// AC-1067: файл своей роли загружается; отсутствующие секции со значениями
/// по умолчанию получают их, секреты приходят из env.
#[test]
fn loads_own_role_file_with_defaults_and_secrets() {
    let config = load_yaml("defaults", "  server:\n    port: 9003\n").expect("load config");

    assert_eq!(config.server.port, 9003);
    assert_eq!(config.management.port, DEFAULT_MANAGEMENT_PORT);
    assert_eq!(config.shutdown, ShutdownConfig::default());
    assert_eq!(config.shutdown.drain_timeout, Duration::from_secs(20));
    assert_eq!(config.shutdown.readiness_delay, Duration::from_secs(5));
    assert_eq!(config.reconciler, ReconcilerConfig::default());
    assert_eq!(
        config.spicedb,
        SpiceDbConfig {
            endpoint: "spicedb:50051".to_owned(),
            preshared_key: "key".to_owned(),
            insecure: false,
            timeout: DEFAULT_SPICEDB_TIMEOUT,
        }
    );
    assert_eq!(
        config.database,
        database::Config {
            uri: DATABASE_URI.to_owned(),
            ..database::Config::default()
        }
    );
    assert_eq!(config.valkey.url, VALKEY_URL);
    assert_eq!(config.identity.user_delegates, "bff");
    assert_eq!(
        config.telemetry,
        TelemetryConfig {
            service_name: SERVER_ROLE.to_owned(),
            instance_id: "delivery-7d9f".to_owned(),
            endpoint: None,
            protocol: OtlpProtocol::Grpc,
            resource_attributes: std::collections::BTreeMap::new(),
            tracing: false,
            metrics: false,
            metrics_export_interval: DEFAULT_METRICS_EXPORT_INTERVAL,
            log_level: LogLevel::Info,
        }
    );
}

/// The example file of the repository is a valid `delivery` config.
#[test]
fn example_config_loads() {
    let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("config.example.yaml");

    let config = load_from(Some(&path), process_env).expect("config.example.yaml");

    assert_eq!(config.server.port, DEFAULT_PORT);
    assert_eq!(config.temporal.host_port, "localhost:7233");
}

/// AC-1068: чужая версия или kind — ошибка с ожидаемыми значениями.
#[test]
fn foreign_api_version_or_kind_is_rejected() {
    for (name, contents) in [
        (
            "api-version",
            delivery_file("").replace(API_VERSION, "config.nomnom.dev/v1beta1"),
        ),
        (
            "kind",
            delivery_file("").replace("kind: Delivery", "kind: Orders"),
        ),
    ] {
        let file = ConfigFile::new(name, &contents);
        let error = load_from(Some(&file.0), process_env)
            .expect_err(name)
            .to_string();
        assert!(error.contains(API_VERSION), "{name}: {error}");
        assert!(error.contains(KIND), "{name}: {error}");
    }
}

/// AC-1069: файл другой роли того же kind не принимается.
#[test]
fn file_of_another_role_is_rejected() {
    let file = ConfigFile::new(
        "worker-role",
        &delivery_file("").replace("name: delivery", "name: delivery-worker"),
    );
    let error = load_from(Some(&file.0), process_env)
        .expect_err("delivery must reject delivery-worker")
        .to_string();
    assert!(error.contains("delivery-worker"), "{error}");

    let file = ConfigFile::new("delivery-role", &delivery_file(""));
    assert!(load_worker_from(Some(&file.0), process_env).is_err());
}

/// AC-1070: `delivery-admin` принимает файл pod любой роли kind `Delivery`,
/// но не чужой kind.
#[test]
fn admin_accepts_a_file_of_any_role() {
    let file = ConfigFile::new(
        "admin-role",
        &delivery_file("").replace("name: delivery", "name: delivery-worker"),
    );

    let kafka = load_kafka_from(Some(&file.0)).expect("load kafka config");

    assert_eq!(
        kafka,
        KafkaConfig {
            brokers: "kafka:9092".to_owned(),
            consumer_group: "delivery".to_owned(),
            initial_offset: InitialOffset::Oldest,
            schema_registry_url: "http://kafka:8081".to_owned(),
        }
    );

    let file = ConfigFile::new(
        "admin-kind",
        &delivery_file("").replace("kind: Delivery", "kind: Payments"),
    );
    assert!(load_kafka_from(Some(&file.0)).is_err());
}

/// AC-1071: без `CONFIG_FILE` или с путём к несуществующему файлу процесс
/// не стартует.
#[test]
fn missing_config_file_fails_startup() {
    let error = load_from(None, process_env)
        .expect_err("CONFIG_FILE is required")
        .to_string();
    assert!(error.contains(CONFIG_FILE_ENV), "{error}");

    let missing = std::env::temp_dir().join("nomnom-delivery-missing-config.yaml");
    assert!(load_from(Some(&missing), process_env).is_err());
    assert!(load_worker_from(None, process_env).is_err());
    assert!(load_kafka_from(None).is_err());
}

/// AC-1072: опечатка в ключе — ошибка с путём ключа.
#[test]
fn unknown_key_error_has_the_key_path() {
    let error = load_error("typo", "  shutdown:\n    drainTimout: 20s\n");

    assert!(error.contains("spec.shutdown.drainTimout"), "{error}");
}

/// AC-1073: неизвестная секция верхнего уровня и неизвестная секция `spec`.
#[test]
fn unknown_top_level_and_spec_sections_are_rejected() {
    let file = ConfigFile::new("status", &format!("{}status: {{}}\n", delivery_file("")));
    let error = load_from(Some(&file.0), process_env)
        .expect_err("status is not a key")
        .to_string();
    assert!(error.contains("status"), "{error}");

    let error = load_error(
        "unknown-section",
        "  fulfillment:\n    acceptanceTimeout: 10m\n",
    );
    assert!(error.contains("spec.fulfillment"), "{error}");
}

/// AC-1075: у секретов нет полей в `spec`, секрет в файле — неизвестный ключ.
#[test]
fn secret_in_file_is_an_unknown_key() {
    for (name, sections, path) in [
        (
            "secret-database",
            "  database:\n    uri: postgresql://u:p@db/delivery\n",
            "spec.database.uri",
        ),
        (
            "secret-spicedb",
            "  spicedb:\n    endpoint: spicedb:50051\n    presharedKey: key\n",
            "spec.spicedb.presharedKey",
        ),
        (
            "secret-valkey",
            "  valkey:\n    url: redis://valkey:6379\n",
            "spec.valkey",
        ),
    ] {
        let error = load_error(name, sections);
        assert!(error.contains(path), "{name}: {error}");
        assert!(!error.contains("p@db"), "{name}: {error}");
    }
}

/// AC-1076: отсутствующий или пустой обязательный секрет — ошибка startup.
#[test]
fn empty_required_secret_fails_startup() {
    let file = ConfigFile::new("secrets", &delivery_file(""));
    for secret in [DATABASE_URI_ENV, VALKEY_URL_ENV, SPICEDB_PRESHARED_KEY_ENV] {
        for value in [None, Some(" ")] {
            let env = |name: &str| {
                if name == secret {
                    value.map(str::to_owned)
                } else {
                    process_env(name)
                }
            };
            let error = load_from(Some(&file.0), env).expect_err(secret).to_string();
            assert!(error.contains(secret), "{secret}: {error}");
        }
    }
}

/// AC-1074, AC-1079: env несекретных настроек, `OTEL_*` и `LOG__LEVEL` не
/// действуют — загрузчик спрашивает у env только секреты и `POD_NAME`.
#[test]
fn only_secrets_and_pod_name_are_read_from_env() {
    let file = ConfigFile::new(
        "env-ignored",
        &delivery_file(
            "  shutdown:\n    drainTimeout: 20s\n  telemetry:\n    serviceName: delivery-api\n",
        ),
    );
    let requested = RefCell::new(Vec::new());
    let env = |name: &str| {
        requested.borrow_mut().push(name.to_owned());
        match name {
            "SHUTDOWN__DRAIN_TIMEOUT" => Some("0ms".to_owned()),
            "SERVER__PORT" | "PORT" => Some("1".to_owned()),
            "OTEL_SERVICE_NAME" => Some("other".to_owned()),
            "OTEL_EXPORTER_OTLP_ENDPOINT" => Some("http://other:4317".to_owned()),
            "LOG__LEVEL" => Some("error".to_owned()),
            other => process_env(other),
        }
    };

    let config = load_from(Some(&file.0), env).expect("load config");

    assert_eq!(config.shutdown.drain_timeout, Duration::from_secs(20));
    assert_eq!(config.server.port, DEFAULT_PORT);
    assert_eq!(config.telemetry.service_name, "delivery-api");
    assert_eq!(config.telemetry.endpoint, None);
    assert_eq!(config.telemetry.log_level, LogLevel::Info);
    let mut requested = requested.into_inner();
    requested.sort();
    assert_eq!(
        requested,
        vec![
            DATABASE_URI_ENV,
            POD_NAME_ENV,
            SPICEDB_PRESHARED_KEY_ENV,
            VALKEY_URL_ENV
        ]
    );
}

/// Fail fast: `spec.shutdown.drainTimeout: 0ms` завершает процесс до портов.
#[test]
fn zero_drain_timeout_fails_startup() {
    let error = load_error("zero-drain", "  shutdown:\n    drainTimeout: 0ms\n");

    assert!(error.contains("spec.shutdown.drainTimeout"), "{error}");
}

#[test]
fn telemetry_settings_are_read_from_the_file() {
    let config = load_yaml(
        "telemetry",
        "  telemetry:
    endpoint: http://alloy:4318
    protocol: http/protobuf
    serviceName: delivery-api
    resourceAttributes:
      deployment.environment.name: kind
      service.version: abc123
  tracing:
    enabled: true
  metrics:
    enabled: true
    exportInterval: 15s
  logging:
    level: debug
",
    )
    .expect("load config");

    assert_eq!(
        config.telemetry,
        TelemetryConfig {
            service_name: "delivery-api".to_owned(),
            instance_id: "delivery-7d9f".to_owned(),
            endpoint: Some("http://alloy:4318".to_owned()),
            protocol: OtlpProtocol::HttpProtobuf,
            resource_attributes: [
                ("deployment.environment.name", "kind"),
                ("service.version", "abc123")
            ]
            .into_iter()
            .map(|(key, value)| (key.to_owned(), value.to_owned()))
            .collect(),
            tracing: true,
            metrics: true,
            metrics_export_interval: Duration::from_secs(15),
            log_level: LogLevel::Debug,
        }
    );
}

/// AC-1077: экспорт включён без endpoint.
#[test]
fn export_without_endpoint_fails_startup() {
    for (name, sections) in [
        ("tracing-no-endpoint", "  tracing:\n    enabled: true\n"),
        (
            "tracing-empty-endpoint",
            "  telemetry:\n    endpoint: \"\"\n  tracing:\n    enabled: true\n",
        ),
        ("metrics-no-endpoint", "  metrics:\n    enabled: true\n"),
    ] {
        let error = load_error(name, sections);
        assert!(error.contains("spec.telemetry.endpoint"), "{name}: {error}");
    }
}

/// AC-1078: неизвестный протокол — ошибка с обоими допустимыми значениями.
#[test]
fn unknown_protocol_lists_the_known_ones() {
    let error = load_error(
        "protocol",
        "  telemetry:\n    endpoint: http://alloy:4318\n    protocol: http/json\n",
    );

    assert!(error.contains("spec.telemetry.protocol"), "{error}");
    assert!(error.contains("grpc"), "{error}");
    assert!(error.contains("http/protobuf"), "{error}");
}

/// AC-1080: endpoint без флагов не включает экспорт.
#[test]
fn endpoint_without_flags_exports_nothing() {
    let config = load_yaml(
        "endpoint-only",
        "  telemetry:\n    endpoint: http://alloy:4317\n",
    )
    .expect("load config");

    assert_eq!(
        config.telemetry.endpoint.as_deref(),
        Some("http://alloy:4317")
    );
    assert!(!config.telemetry.tracing);
    assert!(!config.telemetry.metrics);
}

/// AC-1081: `resourceAttributes` не переопределяют идентичность процесса.
#[test]
fn resource_attributes_must_not_override_process_identity() {
    for attribute in ["service.name", "service.instance.id"] {
        let error = load_error(
            "identity-attribute",
            &format!("  telemetry:\n    resourceAttributes:\n      {attribute}: other\n"),
        );
        assert!(error.contains(attribute), "{attribute}: {error}");
    }
}

/// AC-1082: без `POD_NAME` процесс не стартует.
#[test]
fn missing_pod_name_fails_startup() {
    let file = ConfigFile::new("pod-name", &delivery_file(""));
    let env = |name: &str| {
        if name == POD_NAME_ENV {
            None
        } else {
            process_env(name)
        }
    };

    let error = load_from(Some(&file.0), env)
        .expect_err("POD_NAME is required")
        .to_string();

    assert!(error.contains(POD_NAME_ENV), "{error}");
}

#[test]
fn invalid_telemetry_values_fail_startup() {
    for (name, sections, needle) in [
        (
            "zero-interval",
            "  metrics:\n    exportInterval: 0s\n",
            "spec.metrics.exportInterval",
        ),
        (
            "log-level",
            "  logging:\n    level: trace\n",
            "spec.logging.level",
        ),
        (
            "empty-service-name",
            "  telemetry:\n    serviceName: \" \"\n",
            "spec.telemetry.serviceName",
        ),
    ] {
        let error = load_error(name, sections);
        assert!(error.contains(needle), "{name}: {error}");
    }
}

#[test]
fn rejects_invalid_ports() {
    for (name, sections) in [
        ("zero-port", "  server:\n    port: 0\n"),
        ("zero-management", "  management:\n    port: 0\n"),
        (
            "same-ports",
            "  server:\n    port: 9200\n  management:\n    port: 9200\n",
        ),
    ] {
        assert!(load_yaml(name, sections).is_err(), "{name}");
    }
}

#[test]
fn readiness_delay_is_read_and_may_be_zero() {
    let config =
        load_yaml("readiness", "  shutdown:\n    readinessDelay: 500ms\n").expect("load config");
    assert_eq!(config.shutdown.readiness_delay, Duration::from_millis(500));

    let config =
        load_yaml("zero-readiness", "  shutdown:\n    readinessDelay: 0s\n").expect("load config");
    assert_eq!(config.shutdown.readiness_delay, Duration::ZERO);

    let error = load_error("bad-readiness", "  shutdown:\n    readinessDelay: soon\n");
    assert!(error.contains("spec.shutdown.readinessDelay"), "{error}");
}

/// Identity обязательна процессу с gRPC API: секция и каждый ключ.
#[test]
fn identity_is_required() {
    let file = ConfigFile::new(
        "identity-missing",
        &delivery_file("").replace(IDENTITY_YAML, ""),
    );
    let error = load_from(Some(&file.0), process_env)
        .expect_err("identity is required")
        .to_string();
    assert!(error.contains("spec.identity"), "{error}");

    let error = load_error(
        "identity-partial",
        "  identity:\n    issuer: https://kubernetes.default.svc.cluster.local\n",
    );
    assert!(error.contains("spec.identity"), "{error}");
}

#[test]
fn loads_spicedb_settings() {
    let config = load_yaml(
        "spicedb",
        "  spicedb:\n    endpoint: spicedb:50051\n    insecure: true\n    timeout: 500ms\n",
    )
    .expect("load config");

    assert!(config.spicedb.enabled());
    assert!(config.spicedb.insecure);
    assert_eq!(config.spicedb.timeout, Duration::from_millis(500));
    assert_eq!(config.spicedb.preshared_key, "key");
}

#[test]
fn missing_or_invalid_spicedb_fails_startup() {
    for (name, sections, needle) in [
        (
            "spicedb-empty",
            "  spicedb:\n    endpoint: \"\"\n",
            SPICEDB_ENDPOINT_KEY,
        ),
        (
            "spicedb-no-endpoint",
            "  spicedb:\n    insecure: true\n",
            "endpoint",
        ),
        (
            "spicedb-zero-timeout",
            "  spicedb:\n    endpoint: spicedb:50051\n    timeout: 0s\n",
            "spec.spicedb.timeout",
        ),
    ] {
        let error = load_error(name, sections);
        assert!(error.contains(needle), "{name}: {error}");
    }
}

#[test]
fn spicedb_debug_redacts_preshared_key() {
    let config = SpiceDbConfig {
        preshared_key: "secret-key".to_owned(),
        ..SpiceDbConfig::default()
    };

    assert!(!format!("{config:?}").contains("secret-key"));
}

/// FR-708, AC-967: Temporal is required, without defaults.
#[test]
fn missing_or_invalid_temporal_fails_startup() {
    let config = load_yaml("temporal", "").expect("load config");
    assert_eq!(
        config.temporal,
        TemporalConfig {
            host_port: "temporal:7233".to_owned(),
            namespace: "default".to_owned(),
            connect_timeout: Duration::from_secs(10),
        }
    );

    for (name, sections, needle) in [
        (
            "temporal-missing",
            "  temporal:\n    namespace: default\n",
            "hostPort",
        ),
        (
            "temporal-empty-host",
            "  temporal:\n    hostPort: \"\"\n    namespace: default\n    connectTimeout: 10s\n",
            TEMPORAL_HOST_PORT_KEY,
        ),
        (
            "temporal-empty-namespace",
            "  temporal:\n    hostPort: temporal:7233\n    namespace: \" \"\n    connectTimeout: 10s\n",
            TEMPORAL_NAMESPACE_KEY,
        ),
        (
            "temporal-zero-timeout",
            "  temporal:\n    hostPort: temporal:7233\n    namespace: default\n    connectTimeout: 0s\n",
            TEMPORAL_CONNECT_TIMEOUT_KEY,
        ),
    ] {
        let error = load_error(name, sections);
        assert!(error.contains(needle), "{name}: {error}");
    }
}

/// FR-708: `delivery-worker` reads only its sections of the shared kind and
/// accepts the sections of `delivery`; it needs neither identity, SpiceDB,
/// Valkey nor Kafka.
#[test]
fn worker_loads_its_sections_only() {
    let file = ConfigFile::new(
        "worker",
        &format!(
            "{}spec:\n  management:\n    port: 9084\n  database:\n    maxConns: 5\n{TEMPORAL_YAML}{IDENTITY_YAML}{KAFKA_YAML}",
            header(WORKER_ROLE)
        ),
    );
    let env = |name: &str| match name {
        DATABASE_URI_ENV | POD_NAME_ENV => process_env(name),
        _ => None,
    };

    let config = load_worker_from(Some(&file.0), env).expect("load worker config");

    assert_eq!(config.management.port, 9084);
    assert_eq!(config.database.max_conns, 5);
    assert_eq!(config.database.uri, DATABASE_URI);
    assert_eq!(config.temporal.namespace, "default");
    assert_eq!(config.shutdown.drain_timeout, Duration::from_secs(20));
    assert_eq!(config.telemetry.service_name, WORKER_ROLE);

    let file = ConfigFile::new(
        "worker-no-temporal",
        &format!(
            "{}spec:\n  management:\n    port: 9084\n",
            header(WORKER_ROLE)
        ),
    );
    let error = load_worker_from(Some(&file.0), env)
        .expect_err("a worker without Temporal must fail startup")
        .to_string();
    assert!(error.contains("spec.temporal"), "{error}");

    let file = ConfigFile::new(
        "worker-unknown",
        &format!(
            "{}spec:\n{TEMPORAL_YAML}  temporal2: {{}}\n",
            header(WORKER_ROLE)
        ),
    );
    assert!(load_worker_from(Some(&file.0), env).is_err());
}

/// `delivery-admin` needs the Kafka and Schema Registry sections.
#[test]
fn admin_requires_kafka_sections() {
    let file = ConfigFile::new(
        "admin-incomplete",
        &format!("{}spec:\n{KAFKA_YAML}", header(SERVER_ROLE)),
    );

    let error = load_kafka_from(Some(&file.0))
        .expect_err("an incomplete config must fail the command")
        .to_string();

    assert!(error.contains("spec.schemaRegistry"), "{error}");
}

#[test]
fn loads_reconciler_and_validates() {
    let config = load_yaml(
        "reconciler",
        "  reconciler:\n    interval: 30s\n    grace: 1m\n    pageSize: 100\n",
    )
    .expect("load config");
    assert_eq!(
        config.reconciler,
        ReconcilerConfig {
            interval: Duration::from_secs(30),
            grace: Duration::from_secs(60),
            page_size: 100,
        }
    );

    for (name, sections) in [
        ("reconciler-zero", "  reconciler:\n    interval: 0s\n"),
        ("reconciler-page", "  reconciler:\n    pageSize: 0\n"),
        ("reconciler-big", "  reconciler:\n    pageSize: 5000\n"),
    ] {
        assert!(load_yaml(name, sections).is_err(), "{name}");
    }
}

#[test]
fn loads_database_pool_settings() {
    let config = load_yaml(
        "database-pool",
        "  database:
    maxConns: 2
    minConns: 1
    maxLifetime: 10m
    idleTimeout: 1m
    acquireTimeout: 750ms
",
    )
    .expect("load config");

    assert_eq!(
        config.database,
        database::Config {
            uri: DATABASE_URI.to_owned(),
            max_conns: 2,
            min_conns: 1,
            max_lifetime: Duration::from_secs(600),
            idle_timeout: Duration::from_secs(60),
            acquire_timeout: Duration::from_millis(750),
        }
    );
}

#[test]
fn invalid_database_pool_limits_fail_startup() {
    let valid = database::Config {
        uri: "postgresql://u:p@db/delivery".to_owned(),
        ..database::Config::default()
    };
    for (invalid, needle) in [
        (
            database::Config {
                max_conns: 0,
                ..valid.clone()
            },
            "maxConns",
        ),
        (
            database::Config {
                max_conns: database::MAX_MAX_CONNS + 1,
                ..valid.clone()
            },
            "maxConns",
        ),
        (
            database::Config {
                min_conns: database::MAX_MIN_CONNS + 1,
                ..valid.clone()
            },
            "minConns",
        ),
        (
            database::Config {
                max_conns: 1,
                min_conns: 1,
                ..valid.clone()
            },
            "",
        ),
        (
            database::Config {
                acquire_timeout: Duration::ZERO,
                ..valid.clone()
            },
            "acquireTimeout",
        ),
        (
            database::Config {
                max_lifetime: Duration::ZERO,
                ..valid.clone()
            },
            "maxLifetime",
        ),
        (
            database::Config {
                idle_timeout: Duration::from_secs(3600),
                ..valid.clone()
            },
            "idleTimeout",
        ),
    ] {
        match invalid.validate() {
            Ok(()) => assert!(needle.is_empty(), "{invalid:?} must be rejected"),
            Err(error) => {
                assert!(!needle.is_empty(), "{invalid:?} is valid: {error}");
                assert!(error.to_string().contains(needle), "{error}");
            }
        }
    }
}

#[test]
fn malformed_database_duration_fails_startup() {
    let error = load_error(
        "database-duration",
        "  database:\n    acquireTimeout: soon\n",
    );
    assert!(error.contains("spec.database.acquireTimeout"), "{error}");
}

#[test]
fn database_uri_accepts_postgresql_and_postgres_schemes() {
    let file = ConfigFile::new("database-uri", &delivery_file(""));
    for uri in [
        "postgresql://u:p@db:5432/delivery",
        "postgres://u:p@db/delivery?sslmode=require",
    ] {
        let env = |name: &str| {
            if name == DATABASE_URI_ENV {
                Some(uri.to_owned())
            } else {
                process_env(name)
            }
        };
        let config = load_from(Some(&file.0), env).expect(uri);
        assert_eq!(config.database.uri, uri);
    }
}

/// Только PostgreSQL connection URI: libpq key=value и чужая схема
/// завершают загрузку конфига с ошибкой про DATABASE__URI без его значения.
#[test]
fn database_uri_rejects_non_uri_values() {
    let file = ConfigFile::new("database-uri-invalid", &delivery_file(""));
    for uri in [
        "host=db port=5432 user=u password=p dbname=delivery",
        "u:p@db:5432/delivery",
        "mysql://u:p@db/delivery",
        "postgresql://",
    ] {
        let env = |name: &str| {
            if name == DATABASE_URI_ENV {
                Some(uri.to_owned())
            } else {
                process_env(name)
            }
        };
        let error = load_from(Some(&file.0), env).expect_err(uri).to_string();
        assert!(error.contains(DATABASE_URI_ENV), "{uri}: {error}");
        assert!(!error.contains("password=p"), "{uri}: {error}");
    }
}

#[test]
fn invalid_valkey_url_fails_startup() {
    let file = ConfigFile::new("valkey-url", &delivery_file(""));
    let env = |name: &str| {
        if name == VALKEY_URL_ENV {
            Some("valkey:6379".to_owned())
        } else {
            process_env(name)
        }
    };

    let error = load_from(Some(&file.0), env)
        .expect_err("not a redis URL")
        .to_string();

    assert!(error.contains(VALKEY_URL_ENV), "{error}");
}

#[test]
fn loads_simulator() {
    let config = load_yaml("simulator", "").expect("load config");

    assert_eq!(
        config.simulator,
        SimulatorConfig {
            interval: Duration::from_secs(5),
            min_delay: Duration::from_secs(30),
            max_delay: Duration::from_secs(120),
            track_interval: Duration::from_secs(1),
        }
    );
}

/// FR-606: no defaults, a missing value fails startup.
#[test]
fn missing_simulator_settings_fail_startup() {
    let file = ConfigFile::new(
        "simulator-missing",
        &delivery_file("").replace(SIMULATOR_YAML, ""),
    );
    assert!(load_from(Some(&file.0), process_env).is_err());
    assert!(
        load_yaml(
            "simulator-no-max",
            "  simulator:\n    interval: 5s\n    minDelay: 30s\n"
        )
        .is_err()
    );
}

/// AC-765
#[test]
fn min_delay_above_max_delay_fails_startup() {
    let error = load_error(
        "simulator-inverted",
        "  simulator:\n    interval: 5s\n    minDelay: 3m\n    maxDelay: 1m\n    trackInterval: 1s\n",
    );

    assert!(error.contains(SIMULATOR_MIN_DELAY_KEY), "{error}");
}

#[test]
fn zero_or_malformed_simulator_values_fail_startup() {
    for (name, sections) in [
        (
            "simulator-zero-interval",
            "  simulator:\n    interval: 0s\n    minDelay: 30s\n    maxDelay: 2m\n    trackInterval: 0s\n",
        ),
        (
            "simulator-zero-min",
            "  simulator:\n    interval: 5s\n    minDelay: 0s\n    maxDelay: 2m\n    trackInterval: 1s\n",
        ),
        (
            "simulator-bad-max",
            "  simulator:\n    interval: 5s\n    minDelay: 30s\n    maxDelay: soon\n    trackInterval: 1s\n",
        ),
    ] {
        assert!(load_yaml(name, sections).is_err(), "{name}");
    }
}

/// AC-890
#[test]
fn track_interval_above_interval_fails_startup() {
    let error = load_error(
        "simulator-slow-track",
        "  simulator:\n    interval: 5s\n    minDelay: 30s\n    maxDelay: 2m\n    trackInterval: 6s\n",
    );

    assert!(error.contains(SIMULATOR_TRACK_INTERVAL_KEY), "{error}");
}

#[test]
fn loads_tracking() {
    let config = load_yaml("tracking", "").expect("load config");

    assert_eq!(
        config.tracking,
        TrackingConfig {
            city_origin_latitude: 52.515,
            city_origin_longitude: 13.385,
            city_blocks: 12,
            block_meters: 150.0,
            persist_interval: Duration::from_secs(15),
        }
    );
}

/// FR-665: no defaults; a missing or malformed value or a zero persist
/// interval fails startup. The grid rules belong to VirtualCity (AC-871,
/// di::server).
#[test]
fn missing_or_invalid_tracking_settings_fail_startup() {
    for (name, sections) in [
        ("tracking-missing", "  tracking:\n    cityBlocks: 12\n"),
        (
            "tracking-malformed-blocks",
            "  tracking:
    cityOriginLatitude: 52.5
    cityOriginLongitude: 13.4
    cityBlocks: many
    blockMeters: 150.0
    persistInterval: 15s
",
        ),
        (
            "tracking-zero-persist",
            "  tracking:
    cityOriginLatitude: 52.5
    cityOriginLongitude: 13.4
    cityBlocks: 12
    blockMeters: 150.0
    persistInterval: 0s
",
        ),
    ] {
        assert!(load_yaml(name, sections).is_err(), "{name}");
    }
}

#[test]
fn loads_kafka_and_delivery_estimate() {
    let config = load_yaml("estimate", "").expect("load config");

    assert_eq!(
        config.kafka,
        KafkaConfig {
            brokers: "kafka:9092".to_owned(),
            consumer_group: "delivery".to_owned(),
            initial_offset: InitialOffset::Oldest,
            schema_registry_url: "http://kafka:8081".to_owned(),
        }
    );
    assert_eq!(
        config.delivery_estimate,
        DeliveryEstimateConfig {
            sample_size: 50,
            baseline_courier_lead: Duration::from_secs(15 * 60),
            baseline_transit: Duration::from_secs(20 * 60),
        }
    );
}

/// FR-691: an empty or invalid `spec.deliveryEstimate` stops the process at
/// startup; a zero value is rejected by the estimate policy when the process
/// is composed (`di::server::estimate_policy`).
#[test]
fn missing_or_invalid_delivery_estimate_fails_startup() {
    for (name, sections) in [
        ("estimate-missing", "  deliveryEstimate: {}\n"),
        (
            "estimate-no-transit",
            "  deliveryEstimate:\n    sampleSize: 50\n    baselineCourierLead: 15m\n",
        ),
        (
            "estimate-negative-sample",
            "  deliveryEstimate:\n    sampleSize: -1\n    baselineCourierLead: 15m\n    baselineTransit: 20m\n",
        ),
        (
            "estimate-malformed-sample",
            "  deliveryEstimate:\n    sampleSize: many\n    baselineCourierLead: 15m\n    baselineTransit: 20m\n",
        ),
        (
            "estimate-malformed-lead",
            "  deliveryEstimate:\n    sampleSize: 50\n    baselineCourierLead: soon\n    baselineTransit: 20m\n",
        ),
        (
            "estimate-empty-transit",
            "  deliveryEstimate:\n    sampleSize: 50\n    baselineCourierLead: 15m\n    baselineTransit: \"\"\n",
        ),
    ] {
        assert!(load_yaml(name, sections).is_err(), "{name}");
    }
}

#[test]
fn missing_or_invalid_kafka_fails_startup() {
    for (name, sections, needle) in [
        (
            "kafka-missing",
            "  kafka:\n    brokers: kafka:9092\n",
            "consumerGroup",
        ),
        (
            "kafka-empty-brokers",
            "  kafka:\n    brokers: \" \"\n    consumerGroup: delivery\n    initialOffset: oldest\n",
            KAFKA_BROKERS_KEY,
        ),
        (
            "kafka-empty-group",
            "  kafka:\n    brokers: kafka:9092\n    consumerGroup: \"\"\n    initialOffset: oldest\n",
            KAFKA_CONSUMER_GROUP_KEY,
        ),
        (
            "kafka-unknown-offset",
            "  kafka:\n    brokers: kafka:9092\n    consumerGroup: delivery\n    initialOffset: middle\n",
            "spec.kafka.initialOffset",
        ),
        // FR-719: the Schema Registry is required and must be an http(s) URL.
        (
            "kafka-no-schema-registry",
            "  schemaRegistry:\n    url: \"\"\n",
            SCHEMA_REGISTRY_URL_KEY,
        ),
        (
            "kafka-schema-registry-not-http",
            "  schemaRegistry:\n    url: kafka:8081\n",
            SCHEMA_REGISTRY_URL_KEY,
        ),
        (
            "kafka-schema-registry-old-key",
            "  kafka:\n    brokers: kafka:9092\n    consumerGroup: delivery\n    initialOffset: oldest\n    schemaRegistryUrl: http://kafka:8081\n",
            "spec.kafka.schemaRegistryUrl",
        ),
    ] {
        let error = load_error(name, sections);
        assert!(error.contains(needle), "{name}: {error}");
    }
}
