FROM golang:1.27.1-alpine3.24 AS build

RUN apk add --no-cache gcc musl-dev ca-certificates tzdata \
    && echo 'app:x:65532:65532::/nonexistent:/sbin/nologin' > /tmp/passwd \
    && echo 'app:x:65532:' > /tmp/group

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY pkg ./pkg
COPY proto ./proto
COPY services/optimizer ./services/optimizer

RUN CGO_ENABLED=1 GOOS=linux go build -tags netgo,osusergo -trimpath \
        -ldflags "-s -w -linkmode external -extldflags -static" \
        -o /out/optimizer ./services/optimizer/cmd/optimizer

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /tmp/passwd /etc/passwd
COPY --from=build /tmp/group /etc/group
COPY --from=build /out/optimizer /app/optimizer

WORKDIR /app
USER 65532:65532

EXPOSE 9090 50051
ENTRYPOINT ["/app/optimizer"]
