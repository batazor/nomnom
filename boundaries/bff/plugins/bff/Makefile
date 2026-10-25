
.PHONY: build generate publish test

make: build

test:
	npm --prefix ../.. test

generate:
	npm --prefix ../.. run generate

publish: generate
	npm --prefix ../.. exec -- wgc router plugin publish plugins/bff

build:
	npm --prefix ../.. run build
