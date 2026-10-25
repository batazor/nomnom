"""grpcio adapter for ``nomnom.payments.v1.PaymentsService``.

Kept for the tests that import the transport from here: the servicer lives in
``servicer.py``, the error mapping in ``errors.py`` and the server lifecycle in
``worker.py``.
"""

from infrastructure.grpc.generated import payments_pb2, service_pb2, service_pb2_grpc
from infrastructure.grpc.identity import SubjectResolver
from infrastructure.grpc.servicer import PaymentsHandlers, PaymentsService
from infrastructure.grpc.worker import GrpcWorker

__all__ = [
    "GrpcWorker",
    "PaymentsHandlers",
    "PaymentsService",
    "SubjectResolver",
    "payments_pb2",
    "service_pb2",
    "service_pb2_grpc",
]
