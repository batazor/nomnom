package nomnom.connect;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.google.protobuf.AnyProto;
import com.google.protobuf.ApiProto;
import com.google.protobuf.DescriptorProtos.FileDescriptorProto;
import com.google.protobuf.Descriptors.Descriptor;
import com.google.protobuf.Descriptors.DescriptorValidationException;
import com.google.protobuf.Descriptors.FileDescriptor;
import com.google.protobuf.DurationProto;
import com.google.protobuf.EmptyProto;
import com.google.protobuf.FieldMaskProto;
import com.google.protobuf.InvalidProtocolBufferException;
import com.google.protobuf.SourceContextProto;
import com.google.protobuf.StructProto;
import com.google.protobuf.TimestampProto;
import com.google.protobuf.TypeProto;
import com.google.protobuf.WrappersProto;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import org.apache.kafka.connect.errors.DataException;

/**
 * Latest schemas of Integration Events by event_type, read from the Schema Registry with
 * {@code ?format=serialized}: the Registry returns a FileDescriptorProto, so no .proto parser is
 * needed. The subject of an event is its event_type (FR-717); imported files of the modules are
 * references, the well-known types come from protobuf-java.
 */
final class EventSchemas {

  /** Schema of an event: the id of the latest version and the message it describes. */
  record EventSchema(int id, byte[] messageIndexes, Descriptor message) {}

  private record Cached(EventSchema schema, Instant expiresAt) {}

  private static final Map<String, FileDescriptor> WELL_KNOWN = wellKnown();

  private final HttpClient client;
  private final URI base;
  private final Duration ttl;
  private final Clock clock;
  private final ObjectMapper json = new ObjectMapper();
  private final Map<String, Cached> cache = new ConcurrentHashMap<>();

  EventSchemas(HttpClient client, URI base, Duration ttl, Clock clock) {
    this.client = client;
    this.base = base;
    this.ttl = ttl;
    this.clock = clock;
  }

  /**
   * The schema of the event_type; a new version registered by a deploy is picked up once the
   * cached one is older than the TTL.
   */
  EventSchema forEventType(String eventType) {
    Instant now = clock.instant();
    Cached cached = cache.get(eventType);
    if (cached != null && now.isBefore(cached.expiresAt())) {
      return cached.schema();
    }
    EventSchema schema = load(eventType);
    cache.put(eventType, new Cached(schema, now.plus(ttl)));
    return schema;
  }

  private EventSchema load(String eventType) {
    JsonNode latest = get("subjects/" + encode(eventType) + "/versions/latest");
    FileDescriptor file = build(eventType, latest, new HashMap<>());
    List<Descriptor> messages = file.getMessageTypes();
    for (int index = 0; index < messages.size(); index++) {
      Descriptor message = messages.get(index);
      if (message.getFullName().equals(eventType)) {
        return new EventSchema(latest.get("id").asInt(), messageIndexes(index), message);
      }
    }
    throw new DataException("schema of subject " + eventType + " has no top-level message of that name");
  }

  private FileDescriptor build(String fileName, JsonNode version, Map<String, FileDescriptor> built) {
    FileDescriptorProto proto;
    try {
      proto =
          FileDescriptorProto.parseFrom(Base64.getDecoder().decode(version.get("schema").asText()))
              .toBuilder()
              .setName(fileName)
              .build();
    } catch (InvalidProtocolBufferException e) {
      throw new DataException("schema of " + fileName + " is not a serialized FileDescriptorProto", e);
    }
    Map<String, FileDescriptor> references = new HashMap<>();
    for (JsonNode reference : version.path("references")) {
      String name = reference.get("name").asText();
      FileDescriptor dependency = built.get(name);
      if (dependency == null) {
        JsonNode referenced =
            get(
                "subjects/"
                    + encode(reference.get("subject").asText())
                    + "/versions/"
                    + reference.get("version").asInt());
        dependency = build(name, referenced, built);
        built.put(name, dependency);
      }
      references.put(name, dependency);
    }
    List<FileDescriptor> dependencies = new ArrayList<>();
    for (String dependency : proto.getDependencyList()) {
      FileDescriptor resolved = references.getOrDefault(dependency, WELL_KNOWN.get(dependency));
      if (resolved == null) {
        throw new DataException(fileName + " imports " + dependency + " without a reference");
      }
      dependencies.add(resolved);
    }
    try {
      return FileDescriptor.buildFrom(proto, dependencies.toArray(FileDescriptor[]::new));
    } catch (DescriptorValidationException e) {
      throw new DataException("schema of " + fileName + " is invalid", e);
    }
  }

  private JsonNode get(String path) {
    URI uri = base.resolve(path + "?format=serialized");
    HttpRequest request = HttpRequest.newBuilder(uri).timeout(Duration.ofSeconds(10)).GET().build();
    HttpResponse<String> response;
    try {
      response = client.send(request, HttpResponse.BodyHandlers.ofString());
    } catch (IOException e) {
      throw new DataException("schema registry " + uri + " is unavailable", e);
    } catch (InterruptedException e) {
      Thread.currentThread().interrupt();
      throw new DataException("schema registry request " + uri + " interrupted", e);
    }
    if (response.statusCode() != 200) {
      throw new DataException(
          "schema registry " + uri + ": status " + response.statusCode() + ": " + response.body());
    }
    try {
      return json.readTree(response.body());
    } catch (IOException e) {
      throw new DataException("schema registry " + uri + " returned invalid JSON", e);
    }
  }

  private static String encode(String subject) {
    return URLEncoder.encode(subject, StandardCharsets.UTF_8);
  }

  /**
   * Message indexes of the Confluent wire format for a top-level message: {@code [index]} as a
   * zigzag varint array, and the single byte 0 for the first message of the file.
   */
  static byte[] messageIndexes(int index) {
    if (index == 0) {
      return new byte[] {0};
    }
    ByteArrayOutputStream out = new ByteArrayOutputStream();
    writeVarint(out, 1);
    writeVarint(out, index);
    return out.toByteArray();
  }

  private static void writeVarint(ByteArrayOutputStream out, int value) {
    int zigzag = (value << 1) ^ (value >> 31);
    while ((zigzag & ~0x7F) != 0) {
      out.write((zigzag & 0x7F) | 0x80);
      zigzag >>>= 7;
    }
    out.write(zigzag);
  }

  private static Map<String, FileDescriptor> wellKnown() {
    Map<String, FileDescriptor> files = new HashMap<>();
    for (FileDescriptor file :
        List.of(
            AnyProto.getDescriptor(),
            ApiProto.getDescriptor(),
            DurationProto.getDescriptor(),
            EmptyProto.getDescriptor(),
            FieldMaskProto.getDescriptor(),
            SourceContextProto.getDescriptor(),
            StructProto.getDescriptor(),
            TimestampProto.getDescriptor(),
            TypeProto.getDescriptor(),
            WrappersProto.getDescriptor())) {
      files.put(file.getName(), file);
    }
    return Map.copyOf(files);
  }
}
