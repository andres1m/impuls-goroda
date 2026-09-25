.PHONY: fmt test race vet build verify up down reset logs ps migrate

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

up:
	docker compose up -d --build --wait

down:
	docker compose down

reset:
	docker compose down -v

logs:
	docker compose logs -f

ps:
	docker compose ps -a

migrate:
	docker compose run --rm migrate
