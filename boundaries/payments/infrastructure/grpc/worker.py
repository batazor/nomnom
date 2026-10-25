"""Lifecycle of the payments gRPC server."""

import time
from concurrent import futures

import grpc

from infrastructure.grpc.generated import service_pb2_grpc
from infrastructure.grpc.identity import SubjectResolver
from infrastructure.grpc.servicer import PaymentsHandlers, PaymentsService
from pkg.lifecycle import DrainTimeoutError, LifecycleError


class GrpcWorker:
    """grpc.Server implementation of the shared lifecycle Worker protocol."""

    def __init__(
        self,
        address: tuple[str, int],
        handlers: PaymentsHandlers,
        subjects: SubjectResolver,
    ) -> None:
        self._server = grpc.server(futures.ThreadPoolExecutor(thread_name_prefix="payments-grpc"))
        service_pb2_grpc.add_PaymentsServiceServicer_to_server(
            PaymentsService(handlers, subjects), self._server
        )
        host, port = address
        self._port = self._server.add_insecure_port(f"{host}:{port}")
        if self._port == 0:
            raise LifecycleError(f"listen payments gRPC {host}:{port}: address unavailable")

    @property
    def port(self) -> int:
        """Port actually bound; differs from the configured one when it was ``0``."""
        return self._port

    def start(self) -> None:
        """Start serving requests in the server's thread pool."""
        self._server.start()

    def run(self) -> None:
        """Block until the server terminates."""
        self._server.wait_for_termination()

    def shutdown(self, deadline: float) -> None:
        """Stop accepting calls and drain in-flight ones until ``deadline`` (monotonic).

        Raises:
            DrainTimeoutError: requests were still running at the deadline and were cancelled.
        """
        remaining = max(0.0, deadline - time.monotonic())
        stopped = self._server.stop(remaining)
        if not stopped.wait(remaining):
            self._server.stop(0)
            raise DrainTimeoutError("payments gRPC requests exceeded drain timeout")
