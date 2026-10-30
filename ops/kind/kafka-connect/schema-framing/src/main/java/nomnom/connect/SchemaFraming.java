package nomnom.connect;

import com.google.protobuf.DynamicMessage;
import com.google.protobuf.InvalidProtocolBufferException;
import java.net.URI;
import java.net.http.HttpClient;
import java.nio.ByteBuffer;
import java.time.Clock;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Set;
import org.apache.kafka.common.config.AbstractConfig;
import org.apache.kafka.common.config.ConfigDef;
import org.apache.kafka.common.config.ConfigException;
import org.apache.kafka.connect.connector.ConnectRecord;
import org.apache.kafka.connect.errors.DataException;
import org.apache.kafka.connect.components.Versioned;
import org.apache.kafka.connect.header.Header;
import org.apache.kafka.connect.transforms.Transformation;

/**
 * Frames the value of an Integration Event record in the Schema Registry wire format (FR-714):
 * {@code 0x00}, the id of the latest version of the subject event_type (4 bytes big-endian), the
 * message indexes and the Protobuf bytes of the Outbox. Records of other topics (Change
 * Notifications, JSON for the Cosmo Router) pass unchanged.
 *
 * <p>A record without event_type, without a subject, with a value that is not that message or
 * while the Registry is unavailable throws {@link DataException}: with {@code
 * errors.tolerance=none} the task fails and no record is skipped (FR-718).
 */
public class SchemaFraming<R extends ConnectRecord<R>> implements Transformation<R>, Versioned {

  /** Version of the plugin; Kafka Connect lists it with plugin discovery service_load. */
  static final String VERSION = "1.0.0";

  public static final String TOPICS_CONFIG = "topics";
  public static final String REGISTRY_URL_CONFIG = "registry.url";
  public static final String CACHE_TTL_MS_CONFIG = "cache.ttl.ms";

  /** Header of the Event Envelope with the full Protobuf message name. */
  static final String EVENT_TYPE_HEADER = "event_type";

  private static final byte MAGIC_BYTE = 0;

  private static final ConfigDef CONFIG_DEF =
      new ConfigDef()
          .define(
              TOPICS_CONFIG,
              ConfigDef.Type.LIST,
              ConfigDef.NO_DEFAULT_VALUE,
              ConfigDef.Importance.HIGH,
              "Integration Event streams of the connector whose values are framed.")
          .define(
              REGISTRY_URL_CONFIG,
              ConfigDef.Type.STRING,
              ConfigDef.NO_DEFAULT_VALUE,
              ConfigDef.Importance.HIGH,
              "Base URL of the Confluent-compatible Schema Registry.")
          .define(
              CACHE_TTL_MS_CONFIG,
              ConfigDef.Type.LONG,
              ConfigDef.NO_DEFAULT_VALUE,
              ConfigDef.Range.atLeast(1),
              ConfigDef.Importance.MEDIUM,
              "How long the latest schema of an event_type is reused before it is read again.");

  private Set<String> topics;
  private EventSchemas schemas;

  @Override
  public void configure(Map<String, ?> props) {
    AbstractConfig config = new AbstractConfig(CONFIG_DEF, props);
    List<String> configured = config.getList(TOPICS_CONFIG);
    if (configured.isEmpty() || configured.stream().anyMatch(String::isBlank)) {
      throw new ConfigException(TOPICS_CONFIG, configured, "must list Integration Event streams");
    }
    String url = config.getString(REGISTRY_URL_CONFIG);
    URI base;
    try {
      base = URI.create(url.endsWith("/") ? url : url + "/");
    } catch (IllegalArgumentException e) {
      throw new ConfigException(REGISTRY_URL_CONFIG, url, e.getMessage());
    }
    if (!"http".equals(base.getScheme()) && !"https".equals(base.getScheme())
        || base.getHost() == null) {
      throw new ConfigException(REGISTRY_URL_CONFIG, url, "must be an absolute http(s) url");
    }
    topics = Set.copyOf(configured);
    schemas =
        new EventSchemas(
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build(),
            base,
            Duration.ofMillis(config.getLong(CACHE_TTL_MS_CONFIG)),
            Clock.systemUTC());
  }

  @Override
  public R apply(R record) {
    if (!topics.contains(record.topic())) {
      return record;
    }
    Header header = record.headers().lastWithName(EVENT_TYPE_HEADER);
    if (header == null || !(header.value() instanceof String eventType) || eventType.isBlank()) {
      throw new DataException("record of " + record.topic() + " has no " + EVENT_TYPE_HEADER);
    }
    byte[] payload = bytes(record);
    EventSchemas.EventSchema schema = schemas.forEventType(eventType);
    try {
      DynamicMessage.parseFrom(schema.message(), payload);
    } catch (InvalidProtocolBufferException e) {
      throw new DataException("value of " + eventType + " is not that message", e);
    }
    ByteBuffer framed =
        ByteBuffer.allocate(1 + Integer.BYTES + schema.messageIndexes().length + payload.length)
            .put(MAGIC_BYTE)
            .putInt(schema.id())
            .put(schema.messageIndexes())
            .put(payload);
    return record.newRecord(
        record.topic(),
        record.kafkaPartition(),
        record.keySchema(),
        record.key(),
        record.valueSchema(),
        framed.array(),
        record.timestamp(),
        record.headers());
  }

  private static byte[] bytes(ConnectRecord<?> record) {
    Object value = record.value();
    if (value instanceof byte[] array) {
      return array;
    }
    if (value instanceof ByteBuffer buffer) {
      byte[] array = new byte[buffer.remaining()];
      buffer.duplicate().get(array);
      return array;
    }
    throw new DataException(
        "value of " + record.topic() + " must be bytes, got "
            + (value == null ? "null" : value.getClass().getName()));
  }

  @Override
  public String version() {
    return VERSION;
  }

  @Override
  public ConfigDef config() {
    return CONFIG_DEF;
  }

  @Override
  public void close() {}
}
