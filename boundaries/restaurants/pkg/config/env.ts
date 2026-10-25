export const DEFAULT_PORT = 8082;
export const PORT_ENV = "SERVER__PORT";
export const DEFAULT_MANAGEMENT_PORT = 9082;
export const MANAGEMENT_PORT_ENV = "MANAGEMENT__PORT";
export const DEFAULT_READINESS_DELAY = "5s";
export const READINESS_DELAY_ENV = "SHUTDOWN__READINESS_DELAY";
export const DEFAULT_DRAIN_TIMEOUT = "20s";
export const DRAIN_TIMEOUT_ENV = "SHUTDOWN__DRAIN_TIMEOUT";
export const IDENTITY_ISSUER_ENV = "IDENTITY__ISSUER";
export const IDENTITY_AUDIENCE_ENV = "IDENTITY__AUDIENCE";
export const IDENTITY_JWKS_FILE_ENV = "IDENTITY__JWKS_FILE";
export const IDENTITY_SERVICE_ACCOUNTS_ENV = "IDENTITY__SERVICE_ACCOUNTS";
export const IDENTITY_USER_ISSUER_ENV = "IDENTITY__USER_ISSUER";
export const IDENTITY_USER_JWKS_FILE_ENV = "IDENTITY__USER_JWKS_FILE";
export const IDENTITY_USER_DELEGATES_ENV = "IDENTITY__USER_DELEGATES";
export const SPICEDB_ENDPOINT_ENV = "SPICEDB__ENDPOINT";
export const SPICEDB_PRESHARED_KEY_ENV = "SPICEDB__PRESHARED_KEY";
export const SPICEDB_INSECURE_ENV = "SPICEDB__INSECURE";
export const SPICEDB_TIMEOUT_ENV = "SPICEDB__TIMEOUT";
export const DEFAULT_SPICEDB_TIMEOUT = "2s";
export const MONGODB_URI_ENV = "MONGODB__URI";
export const MONGODB_DATABASE_ENV = "MONGODB__DATABASE";
export const DEFAULT_MONGODB_DATABASE = "restaurants";
export const MONGODB_TIMEOUT_ENV = "MONGODB__TIMEOUT";
export const DEFAULT_MONGODB_TIMEOUT = "5s";
export const MONGODB_MAX_POOL_SIZE_ENV = "MONGODB__MAX_POOL_SIZE";
export const MONGODB_MIN_POOL_SIZE_ENV = "MONGODB__MIN_POOL_SIZE";
export const DEFAULT_MONGODB_MIN_POOL_SIZE = 0;
export const MONGODB_MAX_IDLE_TIME_ENV = "MONGODB__MAX_IDLE_TIME";
export const DEFAULT_MONGODB_MAX_IDLE_TIME = "5m";
export const MONGODB_WAIT_QUEUE_TIMEOUT_ENV = "MONGODB__WAIT_QUEUE_TIMEOUT";
export const DEFAULT_MONGODB_WAIT_QUEUE_TIMEOUT = "5s";
export const MONGODB_MAX_CONNECTING_ENV = "MONGODB__MAX_CONNECTING";
export const DEFAULT_MONGODB_MAX_CONNECTING = 2;
/** Upper bound of `MONGODB__MAX_POOL_SIZE` per process (and per server). */
export const MONGODB_POOL_SIZE_LIMIT = 100;
/**
 * Processes of restaurants that open a MongoClient; each sizes its own pool
 * to its concurrency.
 */
export type MongoDbProcess = "api" | "relay" | "consumer" | "migrate";
/**
 * Default `maxPoolSize` per process: the API serves concurrent RPCs, the
 * relay polls one batch at a time (plus lease renewal and cleanup), the
 * consumer processes one message at a time with its Inbox transaction, and
 * migrate runs sequential DDL.
 */
export const DEFAULT_MONGODB_MAX_POOL_SIZE: Readonly<
  Record<MongoDbProcess, number>
> = Object.freeze({ api: 20, relay: 3, consumer: 5, migrate: 2 });
export const RECONCILER_INTERVAL_ENV = "RECONCILER__INTERVAL";
export const DEFAULT_RECONCILER_INTERVAL = "5m";
export const RECONCILER_GRACE_ENV = "RECONCILER__GRACE";
export const DEFAULT_RECONCILER_GRACE = "10m";
export const RECONCILER_PAGE_SIZE_ENV = "RECONCILER__PAGE_SIZE";
export const DEFAULT_RECONCILER_PAGE_SIZE = 500;
export const KITCHEN_PREPARATION_TIME_ENV = "KITCHEN__PREPARATION_TIME";
export const DEFAULT_KITCHEN_PREPARATION_TIME = "20m";
export const KITCHEN_REPEAT_POLICY_ENV = "KITCHEN__REPEAT_POLICY";
export const DEFAULT_KITCHEN_REPEAT_POLICY = "succeed-without-event";
export const KITCHEN_CANCELLATION_DURING_PREPARATION_ENV =
  "KITCHEN__CANCELLATION_DURING_PREPARATION";
export const DEFAULT_KITCHEN_CANCELLATION_DURING_PREPARATION = "reject";

export const KAFKA_BROKERS_ENV = "KAFKA__BROKERS";
export const KAFKA_CLIENT_ID_ENV = "KAFKA__CLIENT_ID";
export const DEFAULT_KAFKA_CLIENT_ID = "restaurants-relay";
export const OUTBOX_RETENTION_ENV = "OUTBOX__RETENTION";
export const DEFAULT_OUTBOX_RETENTION = "7d";
export const OUTBOX_POLL_INTERVAL_ENV = "OUTBOX__POLL_INTERVAL";
export const DEFAULT_OUTBOX_POLL_INTERVAL = "1s";
export const OUTBOX_BATCH_SIZE_ENV = "OUTBOX__BATCH_SIZE";
export const DEFAULT_OUTBOX_BATCH_SIZE = 100;
export const OUTBOX_LEASE_TTL_ENV = "OUTBOX__LEASE_TTL";
export const DEFAULT_OUTBOX_LEASE_TTL = "15s";
export const KAFKA_CONSUMER_GROUP_ENV = "KAFKA__CONSUMER_GROUP";
export const DEFAULT_KAFKA_CONSUMER_GROUP = "restaurants";
export const KAFKA_INITIAL_OFFSET_ENV = "KAFKA__INITIAL_OFFSET";
export const DEFAULT_KAFKA_INITIAL_OFFSET = "oldest";
export const CONSUMER_MAX_RETRIES_ENV = "CONSUMER__MAX_RETRIES";
export const DEFAULT_CONSUMER_MAX_RETRIES = 5;
export const CONSUMER_RETRY_INITIAL_INTERVAL_ENV =
  "CONSUMER__RETRY_INITIAL_INTERVAL";
export const DEFAULT_CONSUMER_RETRY_INITIAL_INTERVAL = "100ms";
export const CONSUMER_RETRY_MAX_INTERVAL_ENV = "CONSUMER__RETRY_MAX_INTERVAL";
export const DEFAULT_CONSUMER_RETRY_MAX_INTERVAL = "5s";
