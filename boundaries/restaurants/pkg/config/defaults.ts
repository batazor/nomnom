import type { Process } from "./database.ts";

// Explicit defaults of the settings of the configuration file
// (pkg/config/file.ts); a setting without a default here is required.
export const DEFAULT_PORT = 8082;
export const DEFAULT_MANAGEMENT_PORT = 9082;
export const DEFAULT_READINESS_DELAY = "5s";
export const DEFAULT_DRAIN_TIMEOUT = "20s";
export const DEFAULT_TELEMETRY_FLUSH_TIMEOUT = "5s";
export const DEFAULT_SPICEDB_TIMEOUT = "2s";
export const DEFAULT_MONGODB_DATABASE = "restaurants";
export const DEFAULT_MONGODB_TIMEOUT = "5s";
export const DEFAULT_MONGODB_MIN_POOL_SIZE = 0;
export const DEFAULT_MONGODB_MAX_IDLE_TIME = "5m";
export const DEFAULT_MONGODB_WAIT_QUEUE_TIMEOUT = "5s";
export const DEFAULT_MONGODB_MAX_CONNECTING = 2;
/** Upper bound of `spec.mongodb.maxPoolSize` per process (and per server). */
export const MONGODB_POOL_SIZE_LIMIT = 100;
/**
 * Default `maxPoolSize` per process: the API serves concurrent RPCs, the
 * consumer processes one message at a time with its Inbox transaction. The
 * startup migration runs sequentially on the same pool.
 */
export const DEFAULT_MONGODB_MAX_POOL_SIZE: Readonly<Record<Process, number>> =
  Object.freeze({ api: 20, consumer: 5 });
export const DEFAULT_RECONCILER_INTERVAL = "5m";
export const DEFAULT_RECONCILER_GRACE = "10m";
export const DEFAULT_RECONCILER_PAGE_SIZE = 500;
export const DEFAULT_KITCHEN_PREPARATION_TIME = "20m";
export const DEFAULT_KITCHEN_REPEAT_POLICY = "succeed-without-event";
export const DEFAULT_KITCHEN_CANCELLATION_DURING_PREPARATION = "reject";
export const DEFAULT_KITCHEN_QUEUE_AGING_FACTOR = 2;
export const DEFAULT_WAIT_TIME_SAMPLE_SIZE = 50;
export const DEFAULT_WAIT_TIME_RECENT_WINDOW = "30m";
export const DEFAULT_WAIT_TIME_ELEVATED_FACTOR = 1.5;
export const DEFAULT_WAIT_TIME_NORMAL_FACTOR = 1.2;
export const DEFAULT_KAFKA_CLIENT_ID = "restaurants-consumer";
export const DEFAULT_KAFKA_CONSUMER_GROUP = "restaurants";
export const DEFAULT_KAFKA_INITIAL_OFFSET = "oldest";
export const DEFAULT_SCHEMA_REGISTRY_TIMEOUT = "5s";
export const DEFAULT_CONSUMER_MAX_RETRIES = 5;
export const DEFAULT_CONSUMER_RETRY_INITIAL_INTERVAL = "100ms";
export const DEFAULT_CONSUMER_RETRY_MAX_INTERVAL = "5s";
export const DEFAULT_VALKEY_COMMAND_TIMEOUT = "50ms";
export const DEFAULT_CACHE_TTL = "1m";
export const DEFAULT_CACHE_REFRESH_GROUP = "restaurants-cache";
export const DEFAULT_TELEMETRY_PROTOCOL = "grpc";
/** OpenTelemetry SDK default of the periodic metrics export. */
export const DEFAULT_METRICS_EXPORT_INTERVAL = "60s";
export const DEFAULT_LOG_LEVEL = "info";
