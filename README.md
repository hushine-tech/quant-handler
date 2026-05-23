# quant-handler

HTTP BFF for the quant portal: JWT login, CORS for the React app, and gRPC fan-out to account-service, the account-service-owned order.v1 API, strategy-service, and control-panel-service. Responses use JSON only and do not include exchange API secrets.

## Environment

| Variable | Required | Description |
|----------|----------|-------------|
| `ACCOUNT_SERVICE_GRPC_ADDR` | yes | gRPC address for account-service (e.g. `127.0.0.1:50051`) |
| `QUANT_HANDLER_JWT_SECRET` | yes | HMAC secret for signing portal JWTs |
| `HTTP_ADDR` | no | Listen address (default `:8090`) |
| `HANDLER_CORS_ORIGINS` | no | Comma-separated allowed `Origin` values (default `http://localhost:5173`) |
| `STRATEGY_SERVICE_GRPC_ADDR` | no | Legacy/default gRPC address for strategy-service (default `127.0.0.1:50053`). |
| `ORDER_SERVICE_GRPC_ADDR` | no | Compatibility env var for the order.v1 API gRPC address (default `127.0.0.1:50051`; currently served by account-service). |
| `CONTROL_PANEL_SERVICE_GRPC_ADDR` | no | gRPC address for control-panel-service (default `127.0.0.1:50054`). Required for D1/D2/D3 runtime, credential, and market-data control-plane paths. |
| `FEATURES_CONTROL_PANEL_ROUTE_RESOLUTION` | no | `1`/`true` to route strategy run/preview/stop/status through control-panel-service. Hosted and self-hosted runtimes both use RuntimeChannel proxy. |

## Run locally

1. Start **account-service** (gRPC on `:50051`) with TimescaleDB available.
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
- `GET /api/accounts` — Bearer JWT → JSON array of accounts.
- `POST /api/accounts` — Bearer JWT → create. Body: `name`, `mode`, optional `api_key`, `api_secret`, `initial_balance`, and for **backtest** optional `spot` / `futures` objects (see `account-service/docs/wallet-bootstrap-defaults.md`). If `spot` or `futures` is present, the handler runs `UpdateAccountWalletState` after `CreateAccount`.
- `GET /api/accounts/{id}` — Bearer JWT → registry JSON.
- `GET /api/accounts/{id}/wallet` — Bearer JWT → wallet JSON from `GetOnlineAccountInfo`, including `spot_estimated_value`, `futures_position_equity`, and `metrics_authoritative` from **account-service** when reconciled; the handler recomputes aggregates only when the server marks metrics non-authoritative or components do not match `total_value`.
- `GET /api/symbols?market=spot|usdm_futures&q=&limit=` — Bearer JWT → `{ "symbols": [], "stale": bool }`. **`market` is required** (returns `400` if omitted).

## Runtime And Market-Data Control Plane

`control-panel-service` owns runtime routing/provisioning, self-hosted
RuntimeChannel proxying, runtime credentials, and the D2 market-data control
plane. With `features.control_panel_route_resolution=true`, strategy
run/preview/stop/status use control-panel routing. Handler resolves the explicit
`runtime_id` for authorization/health/owner checks, then calls the control-panel
strategy proxy; the proxy sends REQUEST frames over the runtime's outbound
`RuntimeChannel` for both hosted and self-hosted runtimes.

The old name-based debug endpoint `GET /api/_debug/runtime-route` remains
behind the same feature flag but returns `410 Gone`; routing now uses
`runtime_id` only.

## Manual stack check (wallet wizard)

1. Run **account-service** (Binance public `exchangeInfo` used for symbol cache; set `SYMBOL_CACHE_TTL` if needed).
2. Run **quant-handler** and **quant-frontend** (`VITE_API_BASE_URL` pointing at handler).
3. Log in, create a **backtest** account with spot and/or futures selections; open account detail and confirm banner (回测/测试网/实盘) and portfolio numbers.

Automated coverage: `go test ./...` in this repo; `go test -tags=integration ./tests/integration/...` in **account-service** (includes multi-symbol wallet bootstrap and `ListSymbols`).
