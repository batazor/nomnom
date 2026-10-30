package nomnom.connect;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.ByteBuffer;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.Arrays;
import java.util.List;
import java.util.Map;
import org.apache.kafka.common.config.ConfigException;
import org.apache.kafka.connect.data.Schema;
import org.apache.kafka.connect.errors.DataException;
import org.apache.kafka.connect.header.ConnectHeaders;
import org.apache.kafka.connect.source.SourceRecord;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.Test;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.containers.wait.strategy.Wait;

class SchemaFramingTest {

  /** Broker image of the kind environment (ops/kind/manifests/messaging/infrastructure.yaml). */
  private static final String REDPANDA_IMAGE =
      "docker.io/redpandadata/redpanda:v26.2.6@sha256:8b1076e0d61bfceb66594dc01536427ce0b529f8ead35fdab9763ab6085891ce";

  private static final Path MODULES = Path.of("../../../../boundaries");
  private static final String STREAM = "delivery.v1.delivery-events";

  private static GenericContainer<?> redpanda;
  private static String registryUrl;
  private static int deliveryEventsId;
  private static int paymentEventsId;

  @BeforeAll
  static void startRegistry() throws Exception {
    redpanda =
        new GenericContainer<>(REDPANDA_IMAGE)
            .withCommand(
                "redpanda", "start", "--mode=dev-container", "--smp=1", "--memory=256M",
                "--overprovisioned", "--schema-registry-addr=0.0.0.0:8081")
            .withExposedPorts(8081)
            .waitingFor(Wait.forHttp("/subjects").forPort(8081))
            .withStartupTimeout(Duration.ofMinutes(2));
    redpanda.start();
    registryUrl = "http://127.0.0.1:" + redpanda.getMappedPort(8081);

    String delivery = read("delivery/infrastructure/grpc/nomnom/delivery/v1/events.proto");
    deliveryEventsId = register("nomnom.delivery.v1.DeliveryPickedUp", delivery, List.of());
    String payments = read("payments/infrastructure/grpc/nomnom/payments/v1/payments.proto");
    register("nomnom/payments/v1/payments.proto", payments, List.of());
    paymentEventsId =
        register(
            "nomnom.payments.v1.PaymentAuthorized",
            read("payments/infrastructure/grpc/nomnom/payments/v1/events.proto"),
            List.of(
                Map.of(
                    "name", "nomnom/payments/v1/payments.proto",
                    "subject", "nomnom/payments/v1/payments.proto",
                    "version", 1)));
  }

  @AfterAll
  static void stopRegistry() {
    redpanda.stop();
  }

  private static String read(String path) throws IOException {
    return Files.readString(MODULES.resolve(path));
  }

  private static int register(String subject, String schema, List<Map<String, Object>> references)
      throws Exception {
    ObjectMapper json = new ObjectMapper();
    String body =
        json.writeValueAsString(
            Map.of("schemaType", "PROTOBUF", "schema", schema, "references", references));
    HttpRequest request =
        HttpRequest.newBuilder(
                URI.create(
                    registryUrl
                        + "/subjects/"
                        + java.net.URLEncoder.encode(subject, StandardCharsets.UTF_8)
                        + "/versions"))
            .header("Content-Type", "application/vnd.schemaregistry.v1+json")
            .POST(HttpRequest.BodyPublishers.ofString(body))
            .build();
    HttpResponse<String> response =
        HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString());
    assertEquals(200, response.statusCode(), response.body());
    return json.readTree(response.body()).get("id").asInt();
  }

  private static SchemaFraming<SourceRecord> framing(String url) {
    SchemaFraming<SourceRecord> framing = new SchemaFraming<>();
    framing.configure(
        Map.of(
            SchemaFraming.TOPICS_CONFIG, STREAM + ",payments.v1.payment-events",
            SchemaFraming.REGISTRY_URL_CONFIG, url,
            SchemaFraming.CACHE_TTL_MS_CONFIG, "60000"));
    return framing;
  }

  private static SourceRecord record(String topic, String eventType, Object value) {
    ConnectHeaders headers = new ConnectHeaders();
    if (eventType != null) {
      headers.addString(SchemaFraming.EVENT_TYPE_HEADER, eventType);
    }
    return new SourceRecord(
        null, null, topic, 0, Schema.STRING_SCHEMA, "d-1", Schema.BYTES_SCHEMA, value, 1L, headers);
  }

  /** DeliveryPickedUp{order_id: "o-1"}: field 1, length-delimited. */
  private static final byte[] PICKED_UP = {0x0A, 0x03, 'o', '-', '1'};

  // AC-974: DeliveryPickedUp is the second message of delivery/v1/events.proto.
  @Test
  void framesEventOfStream() {
    SourceRecord framed =
        framing(registryUrl).apply(record(STREAM, "nomnom.delivery.v1.DeliveryPickedUp", PICKED_UP));

    ByteBuffer value = ByteBuffer.wrap((byte[]) framed.value());
    assertEquals(0, value.get());
    assertEquals(deliveryEventsId, value.getInt());
    assertEquals(0x02, value.get(), "indexes array length 1, zigzag");
    assertEquals(0x02, value.get(), "message index 1, zigzag");
    byte[] payload = new byte[value.remaining()];
    value.get(payload);
    assertArrayEquals(PICKED_UP, payload);
    assertEquals("d-1", framed.key());
    assertEquals(
        "nomnom.delivery.v1.DeliveryPickedUp",
        framed.headers().lastWithName(SchemaFraming.EVENT_TYPE_HEADER).value());
  }

  @Test
  void framesFirstMessageWithImportedReference() {
    byte[] authorized = {0x0A, 0x03, 'p', '-', '1'};
    SourceRecord framed =
        framing(registryUrl)
            .apply(
                record(
                    "payments.v1.payment-events",
                    "nomnom.payments.v1.PaymentAuthorized",
                    ByteBuffer.wrap(authorized)));

    byte[] value = (byte[]) framed.value();
    assertEquals(paymentEventsId, ByteBuffer.wrap(value, 1, 4).getInt());
    assertEquals(0, value[5], "first message of the file: single 0 byte");
    assertArrayEquals(authorized, Arrays.copyOfRange(value, 6, value.length));
  }

  // AC-976: Change Notifications are not Integration Event streams.
  @Test
  void passesOtherTopicsUnchanged() {
    SourceRecord notification =
        record("delivery.v1.courier-changes", "CourierChanged", "{\"__typename\":\"Courier\"}".getBytes());
    assertSame(notification, framing(registryUrl).apply(notification));
  }

  // AC-975: an event_type without subject fails the task.
  @Test
  void failsWithoutSubject() {
    assertThrows(
        DataException.class,
        () -> framing(registryUrl).apply(record(STREAM, "nomnom.delivery.v1.Unregistered", PICKED_UP)));
  }

  @Test
  void failsWithoutEventType() {
    assertThrows(DataException.class, () -> framing(registryUrl).apply(record(STREAM, null, PICKED_UP)));
  }

  @Test
  void failsOnValueThatIsNotTheMessage() {
    byte[] broken = {0x0A, 0x7F, 'x'};
    assertThrows(
        DataException.class,
        () -> framing(registryUrl).apply(record(STREAM, "nomnom.delivery.v1.DeliveryPickedUp", broken)));
  }

  @Test
  void failsWhileRegistryIsUnavailable() {
    assertThrows(
        DataException.class,
        () ->
            framing("http://127.0.0.1:1")
                .apply(record(STREAM, "nomnom.delivery.v1.DeliveryPickedUp", PICKED_UP)));
  }

  @Test
  void refusesEmptyTopics() {
    SchemaFraming<SourceRecord> framing = new SchemaFraming<>();
    assertThrows(
        ConfigException.class,
        () ->
            framing.configure(
                Map.of(
                    SchemaFraming.TOPICS_CONFIG, "",
                    SchemaFraming.REGISTRY_URL_CONFIG, registryUrl,
                    SchemaFraming.CACHE_TTL_MS_CONFIG, "60000")));
  }

  @Test
  void refusesMissingRegistryUrl() {
    SchemaFraming<SourceRecord> framing = new SchemaFraming<>();
    assertThrows(
        ConfigException.class,
        () ->
            framing.configure(
                Map.of(SchemaFraming.TOPICS_CONFIG, STREAM, SchemaFraming.CACHE_TTL_MS_CONFIG, "60000")));
  }

  @Test
  void encodesMessageIndexes() {
    assertArrayEquals(new byte[] {0}, EventSchemas.messageIndexes(0));
    assertArrayEquals(new byte[] {2, 6}, EventSchemas.messageIndexes(3));
    assertArrayEquals(new byte[] {2, (byte) 0x80, 0x01}, EventSchemas.messageIndexes(64));
  }
}
