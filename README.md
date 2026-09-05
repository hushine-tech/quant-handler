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
| `DOCS_ROOT` | no | Read-only path to the verified document package `current` symlink. When absent or invalid, only document endpoints return `DOCS_UNAVAILABLE`; health and business APIs remain available. |
| `DOCS_PRIVILEGED_USER_IDS` | no | Comma-separated positive user IDs allowed to read privileged architecture and operations documents. Every other authenticated user receives only public documents. |
| `DOCS_CHAT_ENABLED` | no | Enables the document assistant endpoints. Disabled by default; document reading and browser search remain available. |
| `DOCS_CODEX_BINARY` | no | Codex CLI executable (default `codex`); run `codex login` using ChatGPT as the OS user running this service. No API key is used. |
| `DOCS_CHAT_STATE_DIR` | no | Persistent, private conversation metadata and verified display history (default `./.docs-chat`). Keep this directory and the CLI's own session storage across service restarts. |
| `DOCS_CODEX_MODEL` | no | Optional CLI model override; empty uses the CLI default. Personal config, plugins and hooks are not loaded. |
| `DOCS_CHAT_TIMEOUT_SECONDS` | no | Per-CLI invocation timeout, default 180 seconds (1–600). |
| `DOCS_CHAT_REQUESTS_PER_MINUTE` | when chat enabled | Positive per-user question limit. Invalid, stale, or inaccessible-document requests do not consume a token. |

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

### Read-only documents

All document endpoints require the same Bearer JWT as the portal. Authorization is enforced independently for the manifest, search index, Markdown content, and referenced assets; a hidden document or asset returns `404`.

- `GET /api/docs/manifest` — the ordered manifest filtered for the current user.
- `GET /api/docs/search-index` — the browser search index filtered for the current user.
- `GET /api/docs/documents/{document_id}` — verified Markdown with `Content-Type: text/markdown`.
- `GET /api/docs/assets/{asset_path}` — a verified asset referenced by an authorized document.

Responses include an `ETag` and honor `If-None-Match`. The browser never receives a filesystem path and cannot request files that are not registered in the verified package.

### Document assistant

The assistant invokes the locally installed Codex CLI with its saved ChatGPT login. A server-generated conversation locator is bound to the JWT user, exact document commit, and access scope before history is read or a question is appended. The first model turn runs `codex exec`; follow-ups run `codex exec resume` with that conversation's exact CLI thread ID, never `--last`. The browser stores only the opaque locator; verified messages and the thread mapping are private local files, not an extra database table.

Only application-controlled `search_docs` / `search_source` results reach the model. CLI shell, browser, apps, plugins, hooks, subagents and image/file tools are disabled, with read-only sandbox and no approval prompts. The subprocess receives an allowlisted OS/proxy environment, not service or exchange credentials. API-key configuration and the former HTTP model client have been removed.

Run `codex login status` as the service OS user before enabling `DOCS_CHAT_ENABLED=true`. CLI failure affects only Ask Codex, not document reading or trading APIs. Same-conversation requests cannot overlap; at most two CLI invocations run concurrently. `DOCS_CHAT_STATE_DIR` must be private, durable and owned by a single handler instance. Do not share this file-backed state between handler replicas. Clicking “开始新会话” creates a new locator and does not delete past CLI session files.

Verification: `go test ./...`; explicitly opt into real account usage with `DOCS_REAL_CLI_TEST=1 go test ./internal/docsassistant -run TestRealCodexCLIConversation -v -count=1 -timeout 8m`.

- `POST /api/docs/conversations` — create a Conversation for the current document release.
- `GET /api/docs/conversations/{conversation_id}` — restore authorized display messages and verified citations.
- `POST /api/docs/conversations/{conversation_id}/messages` — ask a question with `{"question":"...","current_document_id":"..."}`.

The question route accepts at most 8,000 Unicode code points. It exposes only read-only document retrieval and, for privileged users, exact deployed-source retrieval. A stale locator returns `DOCS_CONVERSATION_STALE`; local or upstream limits return `DOCS_RATE_LIMITED` with `Retry-After`; a model answer whose citations do not match retrieved evidence returns `DOCS_ANSWER_UNVERIFIED`. These failures do not disable the Phase 1 document endpoints.

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
