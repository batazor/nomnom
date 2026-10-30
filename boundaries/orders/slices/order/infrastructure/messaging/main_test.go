package messaging_test

import (
	"context"
	"log"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	ordersv1 "nomnom/orders/infrastructure/grpc/gen/nomnom/orders/v1"
	"nomnom/orders/pkg/testfixture"
	"nomnom/orders/slices/order/infrastructure/repository/repositorytest"

	deliveryv1 "buf.build/gen/go/nomnom/delivery/protocolbuffers/go/nomnom/delivery/v1"
	restaurantsv1 "buf.build/gen/go/nomnom/restaurants/protocolbuffers/go/nomnom/restaurants/v1"
)

// kafka and registry are the broker and Schema Registry of the package,
// started once; PostgreSQL is shared through repositorytest.
var (
	kafka    *testfixture.Kafka
	registry *testfixture.SchemaRegistry
	// schemaIDs are the schema ids of the event files the tests publish.
	schemaIDs = map[string]uint32{}
)

func TestMain(m *testing.M) {
	kafka = testfixture.MustStartKafka()
	defer kafka.Close()
	registry = testfixture.MustStartSchemaRegistry()
	defer registry.Close()
	for _, file := range []protoreflect.FileDescriptor{
		restaurantsv1.File_nomnom_restaurants_v1_events_proto,
		deliveryv1.File_nomnom_delivery_v1_events_proto,
		ordersv1.File_nomnom_orders_v1_events_proto,
		ordersv1.File_nomnom_orders_v1_relationships_proto,
	} {
		id, err := registry.RegisterEvents(context.Background(), file)
		if err != nil {
			log.Fatal(err)
		}
		schemaIDs[file.Path()] = id
	}
	repositorytest.Main(m)
}

// framed is the payload of the event in the wire format the SchemaFraming SMT
// of the connectors writes (FR-714).
func framed(event proto.Message, payload []byte) []byte {
	descriptor := event.ProtoReflect().Descriptor()
	return testfixture.WireFormat(schemaIDs[descriptor.ParentFile().Path()], descriptor.Index(), payload)
}
