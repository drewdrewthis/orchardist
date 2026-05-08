# Webhook Setup

Orchard's webhook receiver (`orchard-tui webhook-serve`) accepts GitHub webhook
deliveries and appends normalized events to `events.jsonl`. The watch daemon
tails this file to short-circuit the 60-second poll cycle, reacting to GitHub
activity within 1–2 seconds.

**Important:** `webhook-serve` and `watch` are separate processes that share
only the local `events.jsonl` file. Both must run on the same host.

## Prerequisites

- `orchard-tui` binary built and on your PATH
- A GitHub repository you have admin or webhook-management access to

## Environment

| Variable | Required | Description |
|----------|----------|-------------|
| `ORCHARD_WEBHOOK_SECRET` | **Yes** | Shared secret for HMAC-SHA256 signature validation. Must match the secret configured in GitHub. |
| `ORCHARD_WEBHOOK_PORT` | No | Override the default port (8477). CLI `--port` flag takes precedence. |

The server **refuses to start** if `ORCHARD_WEBHOOK_SECRET` is unset or empty.

## Port resolution

The port is resolved in this order: `--port` flag > `ORCHARD_WEBHOOK_PORT` env
> `config.watch.webhook_port` > default `8477`.

Use `--port 0` to bind an ephemeral port (printed to stderr on startup).

## Running the server

```bash
export ORCHARD_WEBHOOK_SECRET="your-secret-here"
orchard-tui webhook-serve              # binds to 127.0.0.1:8477
orchard-tui webhook-serve --port 9000  # custom port
orchard-tui webhook-serve --port 0     # ephemeral port
```

The server speaks **plain HTTP only**. TLS termination is the operator's
responsibility (see Production below).

## Local development with smee.io

[smee.io](https://smee.io) forwards GitHub webhooks to your local machine
through a WebSocket relay — no public IP or port forwarding required.

1. Visit https://smee.io/new to create a channel. Copy the URL.
2. Install the smee client: `npm install -g smee-client`
3. Start the relay:
   ```bash
   smee --url https://smee.io/YOUR_CHANNEL --target http://127.0.0.1:8477/webhook
   ```
4. In your GitHub repo → Settings → Webhooks → Add webhook:
   - **Payload URL**: your smee.io channel URL
   - **Content type**: `application/json`
   - **Secret**: same value as `ORCHARD_WEBHOOK_SECRET`
   - **Events**: select the events listed below

### Recommended events

- Pull requests
- Pull request reviews
- Pull request review comments
- Issue comments
- Issues
- Pushes
- Check runs
- Check suites
- Workflow runs

### Troubleshooting smee

- **Smee disconnects**: the WebSocket connection is long-lived but will drop
  on network changes or laptop sleep. Restart `smee` after waking.
- **Signature mismatch (401)**: verify the secret in GitHub matches
  `ORCHARD_WEBHOOK_SECRET` exactly (no trailing whitespace). The server
  validates HMAC-SHA256 over the raw request bytes before any JSON parsing.
- **No events appearing**: check GitHub's webhook delivery log (Settings →
  Webhooks → Recent Deliveries) for HTTP status codes. A 204 means the event
  type is intentionally ignored (e.g., `ping`, `star`, `fork`).

## Production setup

For a production deployment, place the webhook server behind a TLS-terminating
reverse proxy (nginx, Caddy, etc.) with a public hostname.

1. Start the server on a local port:
   ```bash
   orchard-tui webhook-serve --port 8477
   ```
2. Configure your reverse proxy to forward `POST /webhook` to
   `http://127.0.0.1:8477/webhook`.
3. In GitHub, set the Payload URL to your public endpoint
   (e.g., `https://hooks.example.com/webhook`).

### Example nginx config

```nginx
location /webhook {
    proxy_pass http://127.0.0.1:8477;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
}
```

## How it works

1. GitHub POSTs a signed JSON payload to `/webhook`.
2. The server validates the HMAC-SHA256 signature against the raw body bytes.
3. The payload is normalized into a compact JSONL line with fields: `ts`,
   `source` ("webhook"), `kind`, `repo`, `pr`, `issue`, `actor`, `data`.
4. The line is appended to `~/.local/state/git-orchard/events.jsonl`.
5. The watch daemon's tailer detects the new line and triggers a refresh.

Unsupported event types (e.g., `ping`, `star`) return 204 and write nothing.
Bodies larger than 30 MB are rejected with 413 before being fully read.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/webhook` | Receives GitHub webhook payloads (requires signature) |
| GET | `/health` | Liveness probe — returns 200, no signature required |

## Setting ORCHARD_WEBHOOK_SECRET

Generate a strong secret:

```bash
openssl rand -hex 32
```

Add it to your shell profile or a `.env` file sourced before running:

```bash
export ORCHARD_WEBHOOK_SECRET="$(cat ~/.orchard/webhook-secret)"
```
