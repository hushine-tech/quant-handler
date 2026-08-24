# quant-handler

HTTP BFF for the quant portal: JWT login, CORS for the React app, and gRPC fan-out to core-service, the core-service-owned order.v1 API, and control-panel-service. Strategy traffic is proxied through control-panel-service RuntimeChannel; quant-handler does not dial strategy-service directly. Responses use JSON only and do not include exchange API secrets.

## Environment

| Variable | Required | Description |
|----------|----------|-------------|
| `DEPENDENCIES_CORE_SERVICE_GRPC` | yes | gRPC address for the core-service portfolio API (default `127.0.0.1:50051`) |
| `DEPENDENCIES_ORDER_SERVICE_GRPC` | yes | gRPC address for the core-service-owned order API (default `127.0.0.1:50051`) |
| `DEPENDENCIES_CONTROL_PANEL_SERVICE_GRPC` | yes | gRPC address for control-panel-service (default `127.0.0.1:50054`). Required for runtime, credential, strategy proxy, and market-data control-plane paths. |
| `AUTH_JWT_SECRET` | yes | HMAC secret for signing portal JWTs |
| `SERVER_HTTP_ADDR` | no | Listen address (default `:8090`) |
| `AUTH_CORS_ORIGINS` | no | Comma-separated allowed `Origin` values (defaults to the local frontend at both `localhost:5173` and `127.0.0.1:5173`) |

## Run locally

1. Start **core-service** (gRPC on `:50051`) with TimescaleDB available.
2. Export the variables above (use a strong secret in production).
3. (Optional) provide `config.yaml`; when absent, handler uses built-in defaults and then applies env overrides.
4. From this directory:

```bash
go run ./cmd/quant-handler
# or
go run ./cmd/quant-handler -config ./config.yaml
```

## API

- `GET /healthz` — no auth.
- `POST /api/auth/signup` — JSON `{"username":"...", "password":"..."}` → user.
- `POST /api/auth/login` — JSON `{"username":"...", "password":"..."}` → `{ "token", "expires_in", "user" }`.
- `GET /api/portfolios` — Bearer JWT → JSON array of portfolios.
- `POST /api/portfolios` — Bearer JWT → create an portfolio context. Body: `name`, `environment`, optional `description`. Exchange credentials and simulated wallet state are managed as venues and then bound to portfolios; portfolio wallet display is read from `GetPortfolioSnapshot`.
- `GET /api/portfolios/{id}` — Bearer JWT → registry JSON.
- `GET /api/portfolios/{id}/portfolio-snapshot` — Bearer JWT → portfolio aggregate plus venue snapshots from `GetPortfolioSnapshot`.
- `GET /api/symbols?market=spot|usdm_futures&q=&limit=` — Bearer JWT → `{ "symbols": [], "stale": bool }`. **`market` is required** (returns `400` if omitted).

## Runtime And Market-Data Control Plane

`control-panel-service` owns runtime routing/provisioning, self-hosted
RuntimeChannel proxying, runtime credentials, and the D2 market-data control
plane. Strategy run/preview/stop/status always use control-panel routing.
Handler resolves the explicit
`runtime_id` for authorization/health/owner checks, then calls the control-panel
strategy proxy; the proxy sends REQUEST frames over the runtime's outbound
`RuntimeChannel` for both hosted and self-hosted runtimes.

Session status is DB-authoritative after creation. Backtest status/detail reads
the persisted `strategy_sessions` row directly; demo/live status still attempts
a runtime refresh, but `DeadlineExceeded` / `Unavailable` falls back to the
persisted row and returns `status_stale` plus `status_refresh_error` instead of
surfacing a 504 to the page.

## Manual stack check (wallet wizard)

1. Run **core-service** (Binance public `exchangeInfo` supplies the symbol cache).
2. Run **quant-handler** and **quant-frontend** (`VITE_API_BASE_URL` pointing at handler).
3. Log in, create an portfolio; open portfolio detail and confirm banner (回测/测试网/实盘) and portfolio numbers are read from the portfolio snapshot.

Automated coverage: `go test ./...` in this repo; `go test -tags=integration ./tests/integration/...` in **core-service** (includes multi-symbol wallet bootstrap and `ListSymbols`).
