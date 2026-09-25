# Impuls Goroda

Shared Go infrastructure for three independently deployed services:

```text
services/
  gateway/{cmd/gateway,internal}/
  optimizer/{cmd/optimizer,internal}/
  syncer/{cmd/syncer,internal}/
pkg/
  config/       strict YAML configuration and environment substitution
  db/           PostgreSQL connection pool
  logger/       structured Zap logging
  redis/        standalone and cluster clients
  router/       HTTP route registration
  rpc/          gRPC client/server with optional mutual TLS
  server/       echo HTTP server, health and metrics endpoints, probe
  svc/          component lifecycle and dependency ordering
  temporal/     Temporal client and worker registration
  zapadapter/   Temporal logging adapter
docker/         images and configuration of the local stand
proto/          shared service contracts
migrations/     database migrations
web/            Mini App
```

Go 1.27.1 or newer is required. One root module covers all backend services.
Each service has an entry point that wires its infrastructure and serves
`/healthz` and `/metrics`; business handlers are not implemented yet. Packages
must not import another service's `internal` directory.

## Development

```sh
go mod download
make verify
```

`make verify` runs unit and local gRPC integration tests, race detection, vet,
build, and module checksum verification. Tests do not require external services.
Live PostgreSQL, Redis Cluster and Temporal integration is not covered yet.

## Local stand

```sh
cp .env.example .env   # then replace every value
make up                # build images, start everything, wait until healthy
```

`make up` starts PostgreSQL with PostGIS and pgvector, applies migrations,
creates service login users, a three-master Redis Cluster, single-node Kafka
(KRaft), Temporal with its UI, the three services, an nginx edge, Prometheus and
Jaeger. Host ports are bound to 127.0.0.1 only:

| Port  | Service |
|-------|---------|
| 8080  | edge (proxies to gateway) |
| 5432  | PostgreSQL |
| 8233  | Temporal UI |
| 9091  | Prometheus |
| 16686 | Jaeger UI |

`make down` stops the stand and keeps data; `make reset` also deletes volumes.
PostgreSQL passwords are fixed when its volume is first initialized, so after
changing them in `.env` run `make reset`. `make logs`, `make ps` and
`make migrate` are shortcuts for the corresponding compose commands.

Service images are built in Alpine and copied onto `scratch`: a static binary,
CA certificates, time zones and an unprivileged user, without a shell. Container
healthchecks therefore call the binary itself: `<service> healthcheck`.
Configuration is mounted from `docker/<service>/config.yaml`; secrets come from
the environment and are never baked into images.

## Configuration

Each service owns its configuration struct, using only the component types it
needs from `pkg/config`. `config.Load(path, &cfg)` rejects unknown fields and
multiple YAML documents. String values may contain `${VARIABLE}`; unset variables
are errors. Expansion occurs after YAML parsing. Environment variables must
already be exported; the loader does not read `.env` files automatically.

`config.example.yaml` illustrates all component sections. `.env.example` contains
local placeholders only. Use a separate YAML file per service with its own fields.
The example gRPC connection is plaintext for local development; enabling
`use_tls` requires `tls.ca_cert_path`, `tls.client_cert_path` and
`tls.client_key_path` on clients, and the corresponding `server_cert_path` and
`server_key_path` on servers. Both peers verify certificates against the CA.

## Component lifecycle

```go
var cfg struct {
    Logger   config.Logger   `yaml:"logger"`
    Database config.Database `yaml:"database"`
}
if err := config.Load("config.local.yaml", &cfg); err != nil {
    return err
}
log, err := logger.New(
    logger.WithLevel(cfg.Logger.Level),
    logger.WithStdOut(cfg.Logger.StdOut),
)
if err != nil {
    return err
}
pool, err := db.NewDb(log.Log, cfg.Database)
if err != nil {
    _ = log.Stop(context.Background())
    return err
}
return svc.Run(ctx, log.Log, []svc.Service{log, pool})
```

Imports in this example are `context` and `pkg/{config,db,logger,svc}` under
`github.com/andres1m/impuls-goroda`. The example YAML for this snippet must contain
only `logger` and `database` sections.

`svc.Run` checks dependency names and cycles, initializes and health-checks each
component before its dependents, then runs components concurrently. Startup
failure rolls back partial initialization. Cancellation, SIGINT, SIGTERM or a
run error initiates reverse-order shutdown. The default shutdown budget is ten
seconds; use `svc.RunWithOptions` to override it. A completed resource-only `Run`
does not terminate the process. Components must honor contexts and implement
`Stop` safely after partial initialization; the runner bounds its wait but cannot
forcibly kill arbitrary Go goroutines. Instances are single-use. Finish all
registration/configuration before calling `svc.Run`; do not mutate component
fields or call lifecycle methods concurrently yourself.

PostgreSQL and Redis expose `Pool`; Temporal exposes `TemporalClient`. Access these
after `Init` and before shutdown. PostgreSQL `AddAfterRun` callbacks actually run
during initialization and can wire repositories; they should not execute domain
migrations implicitly. Database readiness is checked separately with `Ping`.

For gRPC, register generated handlers in `Server.OnInit` and construct generated
clients in `Client.OnInit`. Client health checks wait for transport readiness;
they do not establish application-level readiness. Server health checks confirm
listener initialization. Domain readiness endpoints remain a service concern.

Temporal SDK client options are preserved. Register workflows and activities
through the callback passed to `temporal.NewWorker`; the callback receives a
`worker.Registry`. The worker depends on `temporal-client` and `logger`. If its
registration needs a database or other resources, compose an additional service
adapter that declares those dependencies. Workflow execution, retry policies and
idempotency belong to service code using the exposed SDK client. Worker health
checks confirm initialization, not active polling or domain readiness. Activity
concurrency follows `worker-count`; workflow-task concurrency has a minimum of
two, as required by the SDK. Fatal worker errors propagate to the lifecycle.

## Provenance

Infrastructure packages were adapted from
[AI-HR-Platform](https://github.com/PluxuryPascal/AI-HR-Platform/tree/66d0a7212af75526c9c419afc332e3dbd06729ec/backend)
at commit `66d0a7212af75526c9c419afc332e3dbd06729ec`, with the source owner's permission.
Adaptations include startup cleanup, log-level handling, independent configuration,
Redis Cluster support, gRPC TLS/shutdown fixes and generic Temporal registration.

The HTTP server, router, service entry-point structure and the Docker/compose
layout come from the same source. Adaptations: echo v5, the port is bound during
initialization, servers are named and declare their dependencies so a process can
run an API and an ops server, health and metrics are mounted separately, CGO static
builds with time zones, no configuration or secrets inside images, pinned image
versions, healthchecks for every component and isolated networks.
