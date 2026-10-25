use super::*;
use config::Config as ConfigLoader;
use std::fs;
use std::time::{SystemTime, UNIX_EPOCH};

#[test]
fn loads_port_from_toml() {
    let name = format!(
        "nomnom-delivery-config-{}-{}.toml",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock before epoch")
            .as_nanos()
    );
    let path = std::env::temp_dir().join(name);
    fs::write(&path, "[server]\nport = 9003\n").expect("write config");

    let config = load_from(Some(&path)).expect("load config");
    fs::remove_file(path).expect("remove config");

    assert_eq!(config.server.port, 9003);
    assert_eq!(config.management.port, DEFAULT_MANAGEMENT_PORT);
    assert_eq!(config.shutdown.drain_timeout, Duration::from_secs(20));
    assert_eq!(config.shutdown.readiness_delay, Duration::from_secs(5));
}

/// Пишет TOML во временный файл с уникальным именем и загружает его.
fn load_toml(name: &str, contents: &str) -> Result<AppConfig, ConfigError> {
    let path = std::env::temp_dir().join(format!(
        "nomnom-delivery-{name}-{}.toml",
        std::process::id()
    ));
    fs::write(&path, contents).expect("write config");
    let result = load_from(Some(&path));
    fs::remove_file(path).expect("remove config");
    result
}

#[test]
fn loads_management_port_from_toml() {
    let config = load_toml("management", "[management]\nport = 9100\n").expect("load config");

    assert_eq!(config.management.port, 9100);
}

#[test]
fn rejects_zero_management_port() {
    assert!(load_toml("zero-management", "[management]\nport = 0\n").is_err());
}

#[test]
fn rejects_management_port_equal_to_server_port() {
    let result = load_toml(
        "same-ports",
        "[server]\nport = 9200\n\n[management]\nport = 9200\n",
    );

    assert!(result.is_err());
}

#[test]
fn loads_readiness_delay_from_toml() {
    let config =
        load_toml("readiness", "[shutdown]\nreadiness_delay = \"500ms\"\n").expect("load config");

    assert_eq!(config.shutdown.readiness_delay, Duration::from_millis(500));
}

#[test]
fn allows_zero_readiness_delay() {
    let config =
        load_toml("zero-readiness", "[shutdown]\nreadiness_delay = \"0s\"\n").expect("load config");

    assert_eq!(config.shutdown.readiness_delay, Duration::ZERO);
}

#[test]
fn rejects_invalid_readiness_delay() {
    assert!(load_toml("bad-readiness", "[shutdown]\nreadiness_delay = \"soon\"\n").is_err());
}

#[test]
fn loads_drain_timeout_from_toml() {
    let name = format!("nomnom-delivery-drain-{}.toml", std::process::id());
    let path = std::env::temp_dir().join(name);
    fs::write(&path, "[shutdown]\ndrain_timeout = \"1500ms\"\n").expect("write config");

    let config = load_from(Some(&path)).expect("load config");
    fs::remove_file(path).expect("remove config");

    assert_eq!(config.shutdown.drain_timeout, Duration::from_millis(1500));
}

#[test]
fn rejects_zero_drain_timeout() {
    let name = format!("nomnom-delivery-zero-drain-{}.toml", std::process::id());
    let path = std::env::temp_dir().join(name);
    fs::write(&path, "[shutdown]\ndrain_timeout = \"0s\"\n").expect("write config");

    let result = load_from(Some(&path));
    fs::remove_file(path).expect("remove config");

    assert!(result.is_err());
}

#[test]
fn identity_is_disabled_by_default() {
    let config = load_toml("identity-default", "").expect("load config");

    assert_eq!(config.identity, IdentityConfig::default());
}

#[test]
fn loads_identity_from_toml() {
    let config = load_toml(
        "identity",
        "[identity]\nissuer = \"https://issuer\"\naudience = \"nomnom.delivery\"\n\
         jwks_file = \"/etc/jwks.json\"\nservice_accounts = \"nomnom/orders-worker=orders-worker\"\n",
    )
    .expect("load config");

    assert_eq!(config.identity.issuer, "https://issuer");
    assert_eq!(config.identity.audience, "nomnom.delivery");
    assert_eq!(config.identity.jwks_file, "/etc/jwks.json");
    assert_eq!(
        config.identity.service_accounts,
        "nomnom/orders-worker=orders-worker"
    );
}

#[test]
fn identity_env_overrides_file_values() {
    let env = std::collections::HashMap::from([
        (ISSUER_ENV, "https://env-issuer"),
        (AUDIENCE_ENV, "nomnom.delivery"),
        (JWKS_FILE_ENV, "/etc/nomnom/identity/jwks.json"),
        (SERVICE_ACCOUNTS_ENV, "nomnom/orders-worker=orders-worker"),
    ]);
    let builder = ConfigLoader::builder()
        .set_default("identity.issuer", "https://file-issuer")
        .expect("default");
    let identity: IdentityConfig = configure_identity(builder, |name| {
        env.get(name).map(|value| (*value).to_owned())
    })
    .expect("configure")
    .build()
    .expect("build")
    .get("identity")
    .expect("identity section");

    assert_eq!(
        identity,
        IdentityConfig {
            issuer: "https://env-issuer".to_owned(),
            audience: "nomnom.delivery".to_owned(),
            jwks_file: "/etc/nomnom/identity/jwks.json".to_owned(),
            service_accounts: "nomnom/orders-worker=orders-worker".to_owned(),
            ..IdentityConfig::default()
        }
    );
}

#[test]
fn loads_user_assertions_from_toml() {
    let config = load_toml(
        "identity-user",
        "[identity]\nuser_issuer = \"nomnom-bff\"\n\
         user_jwks_file = \"/etc/bff-jwks.json\"\nuser_delegates = \"bff\"\n",
    )
    .expect("load config");

    assert_eq!(config.identity.user_issuer, "nomnom-bff");
    assert_eq!(config.identity.user_jwks_file, "/etc/bff-jwks.json");
    assert_eq!(config.identity.user_delegates, "bff");
    assert_eq!(config.identity.issuer, "");
}

#[test]
fn user_assertion_env_overrides_file_values() {
    let env = std::collections::HashMap::from([
        (USER_ISSUER_ENV, "nomnom-bff"),
        (USER_JWKS_FILE_ENV, "/etc/nomnom/identity/bff-jwks.json"),
        (USER_DELEGATES_ENV, "bff,admin-bff"),
    ]);
    let builder = ConfigLoader::builder()
        .set_default("identity.user_issuer", "file-issuer")
        .expect("default");
    let identity: IdentityConfig = configure_identity(builder, |name| {
        env.get(name).map(|value| (*value).to_owned())
    })
    .expect("configure")
    .build()
    .expect("build")
    .get("identity")
    .expect("identity section");

    assert_eq!(
        identity,
        IdentityConfig {
            user_issuer: "nomnom-bff".to_owned(),
            user_jwks_file: "/etc/nomnom/identity/bff-jwks.json".to_owned(),
            user_delegates: "bff,admin-bff".to_owned(),
            ..IdentityConfig::default()
        }
    );
}

#[test]
fn spicedb_is_disabled_by_default() {
    let config = load_toml("spicedb-default", "").expect("load config");

    assert_eq!(config.spicedb, SpiceDbConfig::default());
    assert!(!config.spicedb.enabled());
    assert_eq!(config.spicedb.timeout, Duration::from_secs(2));
}

#[test]
fn loads_spicedb_from_toml() {
    let config = load_toml(
        "spicedb",
        "[spicedb]\nendpoint = \"spicedb:50051\"\npreshared_key = \"key\"\n\
         insecure = true\ntimeout = \"500ms\"\n",
    )
    .expect("load config");

    assert!(config.spicedb.enabled());
    assert_eq!(config.spicedb.endpoint, "spicedb:50051");
    assert_eq!(config.spicedb.preshared_key, "key");
    assert!(config.spicedb.insecure);
    assert_eq!(config.spicedb.timeout, Duration::from_millis(500));
}

#[test]
fn rejects_spicedb_endpoint_without_preshared_key() {
    let error = load_toml(
        "spicedb-no-key",
        "[spicedb]\nendpoint = \"spicedb:50051\"\n",
    )
    .expect_err("key is required");

    assert!(error.to_string().contains(SPICEDB_PRESHARED_KEY_ENV));
}

#[test]
fn rejects_zero_spicedb_timeout() {
    assert!(
        load_toml(
            "spicedb-zero-timeout",
            "[spicedb]\nendpoint = \"spicedb:50051\"\npreshared_key = \"key\"\ntimeout = \"0s\"\n",
        )
        .is_err()
    );
}

#[test]
fn spicedb_env_overrides_file_values() {
    let env = std::collections::HashMap::from([
        (SPICEDB_ENDPOINT_ENV, "localhost:50051"),
        (SPICEDB_PRESHARED_KEY_ENV, "devkey"),
        (SPICEDB_INSECURE_ENV, "true"),
        (SPICEDB_TIMEOUT_ENV, "250ms"),
    ]);
    let builder = ConfigLoader::builder()
        .set_default("spicedb.endpoint", "file:50051")
        .expect("default");
    let spicedb: SpiceDbConfig = configure_spicedb(builder, |name| {
        env.get(name).map(|value| (*value).to_owned())
    })
    .expect("configure")
    .build()
    .expect("build")
    .get("spicedb")
    .expect("spicedb section");

    assert_eq!(
        spicedb,
        SpiceDbConfig {
            endpoint: "localhost:50051".to_owned(),
            preshared_key: "devkey".to_owned(),
            insecure: true,
            timeout: Duration::from_millis(250),
        }
    );
}

#[test]
fn spicedb_debug_redacts_preshared_key() {
    let config = SpiceDbConfig {
        preshared_key: "secret-key".to_owned(),
        ..SpiceDbConfig::default()
    };

    assert!(!format!("{config:?}").contains("secret-key"));
}

#[test]
fn offers_expiry_defaults() {
    let config = load_toml("offers-default", "").expect("load config");

    assert_eq!(
        config.offers,
        OffersConfig {
            expiry_interval: Duration::from_secs(15),
            expiry_batch: 100,
            dispatch_interval: Duration::from_secs(2),
            dispatch_batch: 100,
        }
    );
    assert_eq!(config.offers, OffersConfig::default());
}

#[test]
fn loads_offers_expiry_from_toml_and_validates() {
    let config = load_toml(
        "offers",
        "[offers]\nexpiry_interval = \"5s\"\nexpiry_batch = 20\n",
    )
    .expect("load config");
    assert_eq!(config.offers.expiry_interval, Duration::from_secs(5));
    assert_eq!(config.offers.expiry_batch, 20);

    assert!(load_toml("offers-zero", "[offers]\nexpiry_interval = \"0s\"\n").is_err());
    assert!(load_toml("offers-bad", "[offers]\nexpiry_interval = \"soon\"\n").is_err());
    assert!(load_toml("offers-empty-batch", "[offers]\nexpiry_batch = 0\n").is_err());
    assert!(load_toml("offers-big-batch", "[offers]\nexpiry_batch = 5000\n").is_err());
}

#[test]
fn loads_offers_dispatch_from_toml_and_validates() {
    let config = load_toml(
        "offers-dispatch",
        "[offers]\ndispatch_interval = \"500ms\"\ndispatch_batch = 10\n",
    )
    .expect("load config");
    assert_eq!(config.offers.dispatch_interval, Duration::from_millis(500));
    assert_eq!(config.offers.dispatch_batch, 10);

    assert!(load_toml("dispatch-zero", "[offers]\ndispatch_interval = \"0s\"\n").is_err());
    assert!(load_toml("dispatch-empty-batch", "[offers]\ndispatch_batch = 0\n").is_err());
    assert!(load_toml("dispatch-big-batch", "[offers]\ndispatch_batch = 5000\n").is_err());
}

#[test]
fn offers_env_overrides_defaults() {
    let env = std::collections::HashMap::from([
        (OFFERS_EXPIRY_INTERVAL_ENV, "1m"),
        (OFFERS_EXPIRY_BATCH_ENV, "7"),
        (OFFERS_DISPATCH_INTERVAL_ENV, "5s"),
        (OFFERS_DISPATCH_BATCH_ENV, "3"),
    ]);
    let builder = configure_settings(ConfigLoader::builder(), &OFFERS_SETTINGS, |name| {
        env.get(name).map(|value| (*value).to_owned())
    })
    .expect("configure");
    let offers: OffersConfig = builder
        .build()
        .expect("build")
        .get("offers")
        .expect("offers");
    assert_eq!(offers.expiry_interval, Duration::from_secs(60));
    assert_eq!(offers.expiry_batch, 7);
    assert_eq!(offers.dispatch_interval, Duration::from_secs(5));
    assert_eq!(offers.dispatch_batch, 3);
}

#[test]
fn reconciler_defaults() {
    let config = load_toml("reconciler-default", "").expect("load config");

    assert_eq!(
        config.reconciler,
        ReconcilerConfig {
            interval: Duration::from_secs(300),
            grace: Duration::from_secs(600),
            page_size: 500,
        }
    );
}

#[test]
fn loads_reconciler_from_toml_and_validates() {
    let config = load_toml(
        "reconciler",
        "[reconciler]\ninterval = \"30s\"\ngrace = \"1m\"\npage_size = 100\n",
    )
    .expect("load config");
    assert_eq!(config.reconciler.interval, Duration::from_secs(30));
    assert_eq!(config.reconciler.grace, Duration::from_secs(60));
    assert_eq!(config.reconciler.page_size, 100);

    assert!(load_toml("reconciler-zero", "[reconciler]\ninterval = \"0s\"\n").is_err());
    assert!(load_toml("reconciler-page", "[reconciler]\npage_size = 0\n").is_err());
    assert!(load_toml("reconciler-big", "[reconciler]\npage_size = 5000\n").is_err());
}

#[test]
fn reconciler_env_overrides_file_values() {
    let env = std::collections::HashMap::from([
        (RECONCILER_INTERVAL_ENV, "1m"),
        (RECONCILER_GRACE_ENV, "2m"),
        (RECONCILER_PAGE_SIZE_ENV, "50"),
    ]);
    let reconciler: ReconcilerConfig = configure_reconciler(ConfigLoader::builder(), |name| {
        env.get(name).map(|value| (*value).to_owned())
    })
    .expect("configure")
    .build()
    .expect("build")
    .get("reconciler")
    .expect("reconciler section");

    assert_eq!(
        reconciler,
        ReconcilerConfig {
            interval: Duration::from_secs(60),
            grace: Duration::from_secs(120),
            page_size: 50,
        }
    );
}

#[test]
fn rejects_zero_port() {
    let name = format!("nomnom-delivery-invalid-{}.toml", std::process::id());
    let path = std::env::temp_dir().join(name);
    fs::write(&path, "[server]\nport = 0\n").expect("write config");

    let result = load_from(Some(&path));
    fs::remove_file(path).expect("remove config");

    assert!(result.is_err());
}

#[test]
fn outbox_and_kafka_defaults() {
    let config = load_toml("outbox-defaults", "").expect("load config");
    assert_eq!(config.outbox, OutboxConfig::default());
    assert_eq!(
        config.outbox.retention,
        Duration::from_secs(7 * 24 * 60 * 60)
    );
    assert_eq!(config.kafka.client_id, DEFAULT_KAFKA_CLIENT_ID);
    // Основной процесс стартует без Kafka; relay требует brokers.
    assert!(config.kafka.require().is_err());
}

#[test]
fn loads_outbox_and_kafka_from_toml_and_validates() {
    let config = load_toml(
        "outbox-toml",
        "[kafka]\nbrokers = \"kafka:9092\"\n[outbox]\nretention = \"1d\"\nbatch_size = 10\n",
    )
    .expect("load config");
    assert_eq!(config.kafka.brokers, "kafka:9092");
    assert!(config.kafka.require().is_ok());
    assert_eq!(config.outbox.retention, Duration::from_secs(24 * 60 * 60));
    assert_eq!(config.outbox.batch_size, 10);

    let error =
        load_toml("outbox-zero-batch", "[outbox]\nbatch_size = 0\n").expect_err("zero batch");
    assert!(error.to_string().contains("batch_size"));
}

#[test]
fn outbox_env_overrides_file_values() {
    let builder = configure_settings(ConfigLoader::builder(), &OUTBOX_SETTINGS, |name| {
        (name == OUTBOX_RETENTION_ENV).then(|| "2d".to_owned())
    })
    .expect("configure");
    let config: OutboxConfig = builder
        .build()
        .expect("build")
        .get("outbox")
        .expect("outbox");
    assert_eq!(config.retention, Duration::from_secs(2 * 24 * 60 * 60));
}

#[test]
fn database_pool_defaults_and_env_overrides() {
    let defaults: DatabaseConfig =
        configure_settings(ConfigLoader::builder(), &DATABASE_SETTINGS, |_| None)
            .expect("defaults")
            .build()
            .expect("build")
            .get("database")
            .expect("database");
    assert_eq!(defaults, DatabaseConfig::default());
    defaults.validate().expect("defaults are valid");

    let builder = configure_settings(ConfigLoader::builder(), &DATABASE_SETTINGS, |name| {
        match name {
            DATABASE_URL_ENV => Some("postgres://u:p@db/delivery"),
            DATABASE_MAX_CONNS_ENV => Some("2"),
            DATABASE_MIN_CONNS_ENV => Some("1"),
            DATABASE_MAX_LIFETIME_ENV => Some("10m"),
            DATABASE_IDLE_TIMEOUT_ENV => Some("1m"),
            DATABASE_ACQUIRE_TIMEOUT_ENV => Some("750ms"),
            _ => None,
        }
        .map(str::to_owned)
    })
    .expect("env");
    let database: DatabaseConfig = builder
        .build()
        .expect("build")
        .get("database")
        .expect("database");
    assert_eq!(
        database,
        DatabaseConfig {
            url: "postgres://u:p@db/delivery".to_owned(),
            max_conns: 2,
            min_conns: 1,
            max_lifetime: Duration::from_secs(600),
            idle_timeout: Duration::from_secs(60),
            acquire_timeout: Duration::from_millis(750),
        }
    );
    database.validate().expect("valid");
}

#[test]
fn invalid_database_pool_limits_fail_startup() {
    let valid = DatabaseConfig::default();
    for (invalid, needle) in [
        (
            DatabaseConfig {
                max_conns: 0,
                ..valid.clone()
            },
            "max_conns",
        ),
        (
            DatabaseConfig {
                max_conns: MAX_DATABASE_MAX_CONNS + 1,
                ..valid.clone()
            },
            "max_conns",
        ),
        (
            DatabaseConfig {
                min_conns: MAX_DATABASE_MIN_CONNS + 1,
                ..valid.clone()
            },
            "min_conns",
        ),
        (
            DatabaseConfig {
                max_conns: 1,
                min_conns: 1,
                ..valid.clone()
            },
            "",
        ),
        (
            DatabaseConfig {
                acquire_timeout: Duration::ZERO,
                ..valid.clone()
            },
            "acquire_timeout",
        ),
        (
            DatabaseConfig {
                max_lifetime: Duration::ZERO,
                ..valid.clone()
            },
            "max_lifetime",
        ),
        (
            DatabaseConfig {
                idle_timeout: Duration::from_secs(3600),
                ..valid.clone()
            },
            "idle_timeout",
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
    let error = load_toml(
        "database-duration",
        "[database]\nacquire_timeout = \"soon\"\n",
    )
    .expect_err("malformed duration");
    assert!(error.to_string().contains("acquire_timeout"), "{error}");
}
