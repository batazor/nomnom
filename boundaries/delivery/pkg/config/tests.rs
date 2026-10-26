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
    fs::write(
        &path,
        format!("[server]\nport = 9003\n{DATABASE_TOML}{SIMULATOR_TOML}"),
    )
    .expect("write config");

    let config = load_from(Some(&path)).expect("load config");
    fs::remove_file(path).expect("remove config");

    assert_eq!(config.server.port, 9003);
    assert_eq!(config.management.port, DEFAULT_MANAGEMENT_PORT);
    assert_eq!(config.shutdown.drain_timeout, Duration::from_secs(20));
    assert_eq!(config.shutdown.readiness_delay, Duration::from_secs(5));
}

/// Обязательная секция Simulator (FR-606) тестовых конфигов.
const SIMULATOR_TOML: &str =
    "\n[simulator]\ninterval = \"5s\"\nmin_delay = \"30s\"\nmax_delay = \"2m\"\n";

/// Обязательный URI PostgreSQL тестовых конфигов.
const DATABASE_TOML: &str = "\n[database]\nuri = \"postgresql://u:p@db:5432/delivery\"\n";

/// Пишет TOML во временный файл с уникальным именем и загружает его; без
/// своих секций `[database]` и `[simulator]` к нему добавляются
/// [`DATABASE_TOML`] и [`SIMULATOR_TOML`].
fn load_toml(name: &str, contents: &str) -> Result<AppConfig, ConfigError> {
    let path = std::env::temp_dir().join(format!(
        "nomnom-delivery-{name}-{}.toml",
        std::process::id()
    ));
    let mut contents = contents.to_owned();
    for (section, toml) in [
        ("[database]", DATABASE_TOML),
        ("[simulator]", SIMULATOR_TOML),
    ] {
        if !contents.contains(section) {
            contents.push_str(toml);
        }
    }
    fs::write(&path, contents).expect("write config");
    let result = load_from(Some(&path));
    fs::remove_file(path).expect("remove config");
    result
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
fn database_pool_defaults_and_env_overrides() {
    let defaults: database::Config =
        configure_settings(ConfigLoader::builder(), &DATABASE_SETTINGS, |_| None)
            .expect("defaults")
            .build()
            .expect("build")
            .get("database")
            .expect("database");
    assert_eq!(defaults, database::Config::default());
    let error = defaults.validate().expect_err("the URI has no default");
    assert!(error.to_string().contains(DATABASE_URI_ENV), "{error}");

    let builder = configure_settings(ConfigLoader::builder(), &DATABASE_SETTINGS, |name| {
        match name {
            DATABASE_URI_ENV => Some("postgres://u:p@db/delivery"),
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
    let database: database::Config = builder
        .build()
        .expect("build")
        .get("database")
        .expect("database");
    assert_eq!(
        database,
        database::Config {
            uri: "postgres://u:p@db/delivery".to_owned(),
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
            "max_conns",
        ),
        (
            database::Config {
                max_conns: database::MAX_MAX_CONNS + 1,
                ..valid.clone()
            },
            "max_conns",
        ),
        (
            database::Config {
                min_conns: database::MAX_MIN_CONNS + 1,
                ..valid.clone()
            },
            "min_conns",
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
            "acquire_timeout",
        ),
        (
            database::Config {
                max_lifetime: Duration::ZERO,
                ..valid.clone()
            },
            "max_lifetime",
        ),
        (
            database::Config {
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
        "[database]\nuri = \"postgresql://u:p@db/delivery\"\nacquire_timeout = \"soon\"\n",
    )
    .expect_err("malformed duration");
    assert!(error.to_string().contains("acquire_timeout"), "{error}");
}

#[test]
fn database_uri_accepts_postgresql_and_postgres_schemes() {
    for uri in [
        "postgresql://u:p@db:5432/delivery",
        "postgres://u:p@db/delivery?sslmode=require",
    ] {
        let contents = format!("[database]\nuri = \"{uri}\"\n");
        let config = load_toml("database-uri", &contents).expect(uri);
        assert_eq!(config.database.uri, uri);
    }
}

/// Только PostgreSQL connection URI: пустое значение, libpq key=value и
/// чужая схема завершают загрузку конфига с ошибкой про DATABASE__URI.
#[test]
fn database_uri_rejects_empty_and_non_uri_values() {
    for (name, uri) in [
        ("database-uri-empty", ""),
        ("database-uri-blank", "  "),
        (
            "database-uri-key-value",
            "host=db port=5432 user=u password=p dbname=delivery",
        ),
        ("database-uri-no-scheme", "u:p@db:5432/delivery"),
        ("database-uri-mysql", "mysql://u:p@db/delivery"),
        ("database-uri-scheme-only", "postgresql://"),
    ] {
        let contents = format!("[database]\nuri = \"{uri}\"\n");
        let error = load_toml(name, &contents).expect_err(name);
        assert!(
            error.to_string().contains(DATABASE_URI_ENV),
            "{name}: {error}"
        );
        assert!(!error.to_string().contains("password=p"), "{name}: {error}");
    }
}

#[test]
fn database_url_key_is_unknown() {
    let error = load_toml(
        "database-url-key",
        "[database]\nurl = \"postgresql://u:p@db/delivery\"\n",
    )
    .expect_err("database.url is not a key");
    assert!(error.to_string().contains("url"), "{error}");
}

#[test]
fn loads_simulator_from_toml() {
    let config = load_toml("simulator", "").expect("load config");

    assert_eq!(
        config.simulator,
        SimulatorConfig {
            interval: Duration::from_secs(5),
            min_delay: Duration::from_secs(30),
            max_delay: Duration::from_secs(120),
        }
    );
}

/// FR-606: no defaults, a missing value fails startup.
#[test]
fn missing_simulator_settings_fail_startup() {
    assert!(load_toml("simulator-missing", "[simulator]\n").is_err());
    assert!(
        load_toml(
            "simulator-no-max",
            "[simulator]\ninterval = \"5s\"\nmin_delay = \"30s\"\n"
        )
        .is_err()
    );
}

/// AC-765
#[test]
fn min_delay_above_max_delay_fails_startup() {
    let error = load_toml(
        "simulator-inverted",
        "[simulator]\ninterval = \"5s\"\nmin_delay = \"3m\"\nmax_delay = \"1m\"\n",
    )
    .expect_err("MIN_DELAY > MAX_DELAY must fail");

    assert!(error.to_string().contains(SIMULATOR_MIN_DELAY_ENV));
}

#[test]
fn zero_or_malformed_simulator_values_fail_startup() {
    for (name, contents) in [
        (
            "simulator-zero-interval",
            "[simulator]\ninterval = \"0s\"\nmin_delay = \"30s\"\nmax_delay = \"2m\"\n",
        ),
        (
            "simulator-zero-min",
            "[simulator]\ninterval = \"5s\"\nmin_delay = \"0s\"\nmax_delay = \"2m\"\n",
        ),
        (
            "simulator-bad-max",
            "[simulator]\ninterval = \"5s\"\nmin_delay = \"30s\"\nmax_delay = \"soon\"\n",
        ),
    ] {
        assert!(load_toml(name, contents).is_err(), "{name}");
    }
}

#[test]
fn simulator_env_sets_values() {
    let builder = configure_simulator(ConfigLoader::builder(), |name| match name {
        SIMULATOR_INTERVAL_ENV => Some("1s".to_owned()),
        SIMULATOR_MIN_DELAY_ENV => Some("2s".to_owned()),
        SIMULATOR_MAX_DELAY_ENV => Some("3s".to_owned()),
        _ => None,
    })
    .expect("configure simulator");
    let config = builder.build().expect("build config");

    assert_eq!(
        config.get_string("simulator.min_delay").expect("min delay"),
        "2s"
    );
}
