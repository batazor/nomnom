
.PHONY: build check compose download generate proto-generate start test

make: check

check:
	npm run check

start:
	npm start

compose:
	npm run compose

download:
	npm run router:download

build:
	npm run build

generate:
	npm run generate

# gRPC-клиенты orders, restaurants и delivery для слайсов BFF (plugins/bff/infrastructure/grpc/buf.gen.yaml).
proto-generate:
	cd plugins/bff/infrastructure/grpc && buf generate --template buf.gen.yaml

test:
	npm test
