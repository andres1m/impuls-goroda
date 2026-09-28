.PHONY: fmt gen test race vet build verify cover-optimizer bench-optimizer up down reset logs ps migrate routing-data

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

verify: test race vet build cover-optimizer
	go mod verify

# The threshold covers the computational core; transport and storage adapters are left out.
OPTIMIZER_CORE := $(addprefix ./services/optimizer/internal/,domain solver pricing validation usecase routing scenic semantic)
OPTIMIZER_MIN_COVERAGE := 85.0

cover-optimizer:
	@mkdir -p bin
	go test -coverprofile=bin/optimizer-core.cov $(OPTIMIZER_CORE) > /dev/null
	@go tool cover -func=bin/optimizer-core.cov | awk -v min=$(OPTIMIZER_MIN_COVERAGE) '/^total:/ { found = 1; sub("%", "", $$3); printf "optimizer core coverage %s%% (minimum %s%%)\n", $$3, min; exit ($$3 + 0 < min + 0) } END { if (!found) exit 1 }'

PROFILES := bin/profiles
OPTIMIZER_BENCH := '^Benchmark(Optimize|Recompute|Search|Repair)$$'

bench-optimizer:
	@mkdir -p $(PROFILES)
	go test -run '^$$' -bench $(OPTIMIZER_BENCH) -benchmem -count 10 ./services/optimizer/internal/usecase/ ./services/optimizer/internal/solver/ | tee $(PROFILES)/bench.txt
	go test -run '^$$' -bench '^BenchmarkOptimize$$/^pool=200$$' -benchtime 200x -cpuprofile $(PROFILES)/cpu.pprof -memprofile $(PROFILES)/mem.pprof -o $(PROFILES)/usecase.test ./services/optimizer/internal/usecase/ > /dev/null
	go tool pprof -top -nodecount 10 $(PROFILES)/usecase.test $(PROFILES)/cpu.pprof > $(PROFILES)/cpu.txt
	go tool pprof -top -nodecount 10 -sample_index alloc_space $(PROFILES)/usecase.test $(PROFILES)/mem.pprof > $(PROFILES)/mem.txt
	go run ./services/optimizer/cmd/benchreport -bench $(PROFILES)/bench.txt -cpu-top $(PROFILES)/cpu.txt -mem-top $(PROFILES)/mem.txt \
		-env "Source=the commit that last changed this file" -env "Go=$$(go env GOVERSION)" \
		-env "CPU=$$(lscpu | sed -n 's/^Model name: *//p')" -env "Cores=$$(nproc)" \
		-env "Search config=beam width 16, parallelism 4" \
		-env "Pools=60, 200 and 500 synthetic candidates; planner pools within about 1.5 km" \
		-env "Caches=none in the measured path" -env "Repeats=10 per benchmark, median shown" \
		> services/optimizer/BENCHMARKS.md

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
