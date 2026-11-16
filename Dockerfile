# syntax=docker/dockerfile:1
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
# Optional combined CA bundle for development environments with an HTTPS proxy.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; \
    go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=secret,id=proxy_ca \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; \
    CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/agentlog ./cmd/agentlog
RUN mkdir -p /data && chmod 700 /data

FROM scratch
COPY --from=build /out/agentlog /agentlog
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /data /data
COPY LICENSE THIRD_PARTY_NOTICES.txt /licenses/
USER 65532:65532
WORKDIR /data
EXPOSE 4318
ENTRYPOINT ["/agentlog"]
CMD ["--listen", "0.0.0.0:4318", "--db", "/data/agentlog.db"]
