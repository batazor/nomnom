"""Generated grpcio stubs of ``nomnom.payments.v1``, loaded from the private ``gen`` root."""

import sys
from pathlib import Path

# Generated grpcio code uses absolute ``nomnom.payments.v1`` imports. Keep its
# root private to the transport package instead of leaking it into application code.
_GEN_ROOT = Path(__file__).with_name("gen")
if str(_GEN_ROOT) not in sys.path:
    sys.path.insert(0, str(_GEN_ROOT))

from nomnom.payments.v1 import payments_pb2, service_pb2, service_pb2_grpc  # noqa: E402

__all__ = ["payments_pb2", "service_pb2", "service_pb2_grpc"]
