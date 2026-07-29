.PHONY: build test vet run

build:
	mkdir -p bin
	go build -trimpath -o bin/control ./cmd/control

test:
	go test ./...

vet:
	go vet ./...

run:
	go run ./cmd/control

