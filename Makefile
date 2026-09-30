GO ?= go

.PHONY: build test vet fmt demo

build:
	$(GO) build -buildvcs=false -trimpath -o bin/asteria ./cmd/asteria

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal

demo: build
	./bin/asteria --offline init --schema examples/traffic.yaml
	./bin/asteria --offline init --schema examples/tourism.yaml
	./bin/asteria --offline write --library traffic --domain parking --source demo://parking --file examples/parking.md
	./bin/asteria --offline write --library tourism --domain shuttle --source demo://shuttle --file examples/shuttle.md
	./bin/asteria --offline query '停车分流与接驳运力有哪些约束？' --scope traffic,tourism
