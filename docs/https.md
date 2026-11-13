# HTTPS with Docker Compose

## Public domain

`.env`:

```dotenv
AGENTLOG_TOKEN=YOUR_TOKEN
AGENTLOG_DOMAIN=agents.example.com
```

DNS A/AAAA records must reach this server, and TCP ports 80/443 must be reachable from the internet for certificate validation. Caddy uses these host ports; avoid conflicts with an existing proxy.

```sh
docker compose -f compose.yaml -f compose.https.yaml up -d --build
```

Copilot managed settings → `telemetry.endpoint`: `https://agents.example.com`

Caddy proxies to `agentlog:4318` on the Docker network and manages certificate acquisition/renewal. The existing collector port remains bound to host loopback. Certificates and Caddy state persist in `caddy-data` / `caddy-config`; keep using both Compose files for upgrades, logs and shutdown. Removing these volumes also removes certificate state.

## Localhost

`.env`:

```dotenv
AGENTLOG_DOMAIN=localhost
```

```sh
docker compose -f compose.yaml -f compose.https.yaml up -d --build
docker compose -f compose.yaml -f compose.https.yaml cp caddy:/data/caddy/pki/authorities/local/root.crt ./agentlog-local-ca.crt
```

Caddy uses its local CA for `localhost`. The CLI runs outside Docker and must trust that CA. Copilot's `telemetry.endpoint` is `https://localhost`; launch with the certificate's absolute path:

```sh
OTEL_EXPORTER_OTLP_CERTIFICATE=/absolute/path/agentlog-local-ca.crt copilot
```

Use the root certificate, not the private CA key. Do not disable TLS verification. The browser also needs the CA installed in its trust store. `localhost` refers to the CLI's machine; for other machines use the server's public domain instead.

[Caddy automatic HTTPS](https://caddyserver.com/docs/automatic-https) · [Copilot certificate configuration](https://docs.github.com/en/copilot/reference/enterprise-administrators/opentelemetry-reference#transport-security)
