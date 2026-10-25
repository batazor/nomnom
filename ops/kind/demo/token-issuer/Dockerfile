# syntax=docker/dockerfile:1.7
# Dev token issuer (только kind): выдаёт access tokens тестового identity provider для
# демо-пользователей. Ключ приходит из Secret, в образе только stdlib-скрипт.
FROM python:3.14.9-slim-bookworm
COPY issuer.py /app/issuer.py
USER 65532:65532
EXPOSE 3011
ENTRYPOINT ["python3", "-I", "/app/issuer.py"]
