"""Fast socket bind shared by the HTTP servers of the lifecycle."""

import socketserver
from http.server import HTTPServer


class FastBindMixin(HTTPServer):
    """HTTPServer that binds without resolving its own host name.

    ``HTTPServer.server_bind`` calls ``socket.getfqdn``: in some environments reverse DNS
    for 0.0.0.0 takes seconds and delays startup. The server name is never used.
    """

    def server_bind(self) -> None:
        """Bind the socket and record the raw address as the server name."""
        socketserver.TCPServer.server_bind(self)
        host, port = self.server_address[:2]
        self.server_name = str(host)
        self.server_port = int(port)
