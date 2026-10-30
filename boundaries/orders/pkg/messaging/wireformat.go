package messaging

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// The value of an Integration Event record is in the Schema Registry wire
// format (FR-714): magic byte 0, the schema id (4 bytes big-endian), the
// message indexes (a zigzag varint array, the single byte 0 for the first
// message of the file) and the Protobuf bytes of the contract.
const wireMagicByte = 0

// wireFrame is the parsed wire format of a record value.
type wireFrame struct {
	SchemaID uint32
	Payload  []byte
}

// parseWireFrame splits the record value into the schema id and the Protobuf
// payload. Errors are permanent: the record is not in the wire format.
func parseWireFrame(value []byte) (wireFrame, error) {
	if len(value) < 6 {
		return wireFrame{}, fmt.Errorf("value of %d bytes is not in the schema registry wire format", len(value))
	}
	if value[0] != wireMagicByte {
		return wireFrame{}, fmt.Errorf("value starts with %#x, not the schema registry magic byte", value[0])
	}
	id := binary.BigEndian.Uint32(value[1:5])
	rest := value[5:]
	count, n := binary.Varint(rest)
	if n <= 0 || count < 0 {
		return wireFrame{}, errors.New("message indexes of the wire format are malformed")
	}
	rest = rest[n:]
	for range count {
		index, n := binary.Varint(rest)
		if n <= 0 || index < 0 {
			return wireFrame{}, errors.New("message indexes of the wire format are malformed")
		}
		rest = rest[n:]
	}
	return wireFrame{SchemaID: id, Payload: rest}, nil
}
