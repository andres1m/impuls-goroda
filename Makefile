.PHONY: fmt gen test race vet build verify up down reset logs ps migrate routing-data

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

# The routing engine starts only once its graphs are built, so a fresh stand still comes up.
ROUTING := docker compose --profile routing
ROUTING_SERVICES := osrm-foot osrm-car

up:
	docker compose up -d --build --wait
	docker compose rm -f $(INIT_JOBS)
	@if docker compose run --rm --no-deps --entrypoint test osrm-foot -f /data/current/.complete; then \
		$(ROUTING) up -d --wait $(ROUTING_SERVICES); \
	else \
		echo "routing engine not started: no built graphs found, run make routing-data"; \
	fi

down:
	$(ROUTING) down

# Routing graphs take long to rebuild and do not depend on the stand's data, so reset keeps them.
reset:
	$(ROUTING) rm -fsv
	$(ROUTING) down
	docker volume rm -f $$(docker compose config --volumes | grep -vx routing-data | sed 's/^/impuls-goroda_/')

routing-data:
	docker compose --profile routing-build build routing-fetch scenic-grid
	docker compose --profile routing-build run --rm routing-fetch
	docker compose --profile routing-build run --rm scenic-grid
	docker compose --profile routing-build run --rm routing-build
	$(ROUTING) up -d --wait --force-recreate $(ROUTING_SERVICES)

logs:
	$(ROUTING) logs -f

ps:
	$(ROUTING) ps -a

migrate:
	docker compose run --rm migrate
