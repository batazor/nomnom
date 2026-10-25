import socketserver
from http.server import HTTPServer


class FastBindMixin(HTTPServer):
    """HTTPServer.server_bind вызывает socket.getfqdn: на части окружений reverse DNS
    для 0.0.0.0 занимает секунды и задерживает startup. Имя сервера не используется."""

    def server_bind(self) -> None:
        socketserver.TCPServer.server_bind(self)
        host, port = self.server_address[:2]
        self.server_name = str(host)
        self.server_port = int(port)
