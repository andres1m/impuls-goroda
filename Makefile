.PHONY: fmt gen test race vet build verify up down reset logs ps migrate

gen:
	buf lint
	buf generate

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

INIT_JOBS := migrate db-users redis-init temporal-schema temporal-namespace

up:
	docker compose up -d --build --wait
	docker compose rm -f $(INIT_JOBS)

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
