.PHONY: fmt test race vet build verify

fmt:
	gofmt -w pkg services

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	go build ./...

verify: test race vet build
	go mod verify
