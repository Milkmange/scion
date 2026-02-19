# Hub Event Publishing & Web Consolidation

## Overview

The Scion Hub is the source of truth for all state changes — agent CRUD, status transitions, grove operations, and broker connectivity. The web frontend's real-time pipeline (M7/M8) consumes events from NATS via an SSE bridge, but nothing currently publishes to NATS.

This document covers two related concerns:

1. **Event publishing** — The `EventPublisher` interface and its implementations for delivering state-change notifications to the web frontend.
2. **Consolidated architecture** — An alternative to the current three-process deployment (Hub + Koa BFF + NATS) that rolls web-serving functionality into the Go binary, eliminating NATS as a runtime dependency for single-node deployments.

The event publishing feature is opt-in: when NATS is not configured, the Hub operates exactly as it does today. When a NATS URL is provided, the Hub connects and publishes state-change events after successful database writes. Under the consolidated architecture, the default implementation uses in-process Go channels instead of NATS.

For the SSE/NATS bridge architecture and client-side subscription model, see `web-frontend-design.md` §12.

---

## Design Principles

1. **Fire-and-forget.** NATS publish failures are logged but never fail the HTTP request. The database write is the commit point; NATS is a best-effort notification layer.
2. **Publish after commit.** Events are published only after the store operation succeeds. This avoids notifying subscribers about changes that were rolled back.
3. **Dual-publish for status.** Agent status changes are published to both the agent-scoped subject (`agent.{id}.status`) and the grove-scoped subject (`grove.{groveId}.agent.status`). This allows grove-level subscribers to receive lightweight updates without per-agent subscriptions.
4. **Subject hierarchy is the filter.** The Hub does not implement weight-class filtering. The subject hierarchy itself controls which subscribers receive which events. Heavy payloads (harness output) are published only to agent-scoped subjects; lightweight/medium events are published to grove-scoped subjects.
5. **No NATS dependency for startup.** The Hub starts and serves API requests even if NATS is unavailable. NATS connection is established asynchronously and reconnects automatically.

---

## Configuration

NATS publishing is controlled by a single enablement gate: if a NATS server URL is provided, publishing is enabled.

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `SCION_SERVER_NATS_URL` | Comma-separated NATS server URLs (e.g., `nats://localhost:4222`) | (empty — disabled) |
| `SCION_SERVER_NATS_TOKEN` | Authentication token for NATS | (empty) |

### YAML Configuration (`settings.yaml` or `server.yaml`)

```yaml
server:
  nats:
    url: "nats://localhost:4222"
    token: ""
```

### CLI Flag

```
scion server start --enable-hub --nats-url nats://localhost:4222
```

### GlobalConfig Addition

```go
// In pkg/config/hub_config.go

type NATSConfig struct {
    // URL is a comma-separated list of NATS server URLs.
    // If empty, NATS event publishing is disabled.
    URL   string `json:"url" yaml:"url" koanf:"url"`
    // Token is the authentication token for NATS.
    Token string `json:"token" yaml:"token" koanf:"token"`
}

// Added to GlobalConfig:
type GlobalConfig struct {
    // ... existing fields ...
    NATS NATSConfig `json:"nats" yaml:"nats" koanf:"nats"`
}
```

### hub.ServerConfig Addition

```go
// In pkg/hub/server.go

type ServerConfig struct {
    // ... existing fields ...

    // NATSUrl is a comma-separated list of NATS server URLs.
    // If non-empty, NATS event publishing is enabled.
    NATSUrl   string
    // NATSToken is the authentication token for NATS.
    NATSToken string
}
```

---

## Architecture

### Component: `EventPublisher`

A new service in `pkg/hub/events.go` that owns the NATS connection and provides typed publish methods. The publisher is injected into the `Server` struct following the same pattern as `storage`, `secretBackend`, etc.

```
┌─────────────┐        ┌──────────────────┐        ┌────────────┐
│  Handler    │──────►  │  EventPublisher  │──────►  │   NATS     │
│ (after DB   │ publish │                  │  pub    │   Server   │
│  commit)    │         │  - nats.Conn     │         │            │
│             │         │  - Publish()     │         │            │
└─────────────┘         └──────────────────┘         └────────────┘
```

### Interface

```go
// EventPublisher publishes state-change events to NATS.
// The zero-value (nil) is safe to call — all methods are no-ops when
// the publisher is nil, allowing handlers to call unconditionally.
type EventPublisher interface {
    // PublishAgentStatus publishes an agent status change to both
    // agent-scoped and grove-scoped subjects (dual-publish).
    PublishAgentStatus(ctx context.Context, agent *store.Agent)

    // PublishAgentCreated publishes an agent-created event.
    PublishAgentCreated(ctx context.Context, agent *store.Agent)

    // PublishAgentDeleted publishes an agent-deleted event.
    PublishAgentDeleted(ctx context.Context, agentID, groveID string)

    // PublishGroveUpdated publishes a grove metadata change.
    PublishGroveUpdated(ctx context.Context, grove *store.Grove)

    // PublishGroveDeleted publishes a grove deletion.
    PublishGroveDeleted(ctx context.Context, groveID string)

    // PublishBrokerStatus publishes a broker status change.
    PublishBrokerStatus(ctx context.Context, brokerID, status string)

    // PublishBrokerConnected publishes a broker-connected event
    // for each grove the broker serves.
    PublishBrokerConnected(ctx context.Context, brokerID, brokerName string, groveIDs []string)

    // PublishBrokerDisconnected publishes a broker-disconnected event
    // for each grove the broker served.
    PublishBrokerDisconnected(ctx context.Context, brokerID string, groveIDs []string)

    // Close drains the NATS connection gracefully.
    Close()
}
```

### Nil-safe Pattern

Handlers call `s.events.PublishAgentStatus(...)` unconditionally. When NATS is disabled, `s.events` is nil and the methods are defined on a nil receiver that returns immediately:

```go
func (p *NATSEventPublisher) PublishAgentStatus(ctx context.Context, agent *store.Agent) {
    if p == nil {
        return
    }
    // ... publish logic
}
```

This avoids `if s.events != nil` guards throughout the handlers.

---

## Subject Hierarchy & Message Formats

All payloads are JSON. Timestamps use RFC 3339.

### Grove-Scoped Subjects

These reach grove-level subscribers (`grove.{groveId}.>`).

| Subject | Trigger | Payload |
|---------|---------|---------|
| `grove.{groveId}.agent.status` | Agent status change | `AgentStatusEvent` |
| `grove.{groveId}.agent.created` | Agent created | `AgentCreatedEvent` |
| `grove.{groveId}.agent.deleted` | Agent deleted | `AgentDeletedEvent` |
| `grove.{groveId}.updated` | Grove metadata change | `GroveUpdatedEvent` |
| `grove.{groveId}.broker.connected` | Broker joined grove | `BrokerGroveEvent` |
| `grove.{groveId}.broker.disconnected` | Broker left grove | `BrokerGroveEvent` |

### Agent-Scoped Subjects

These reach agent-level subscribers (`agent.{agentId}.>`).

| Subject | Trigger | Payload |
|---------|---------|---------|
| `agent.{agentId}.status` | Agent status change | `AgentStatusEvent` |
| `agent.{agentId}.created` | Agent created | `AgentCreatedEvent` |
| `agent.{agentId}.deleted` | Agent deleted | `AgentDeletedEvent` |

### Broker-Scoped Subjects

| Subject | Trigger | Payload |
|---------|---------|---------|
| `broker.{brokerId}.status` | Broker heartbeat / status change | `BrokerStatusEvent` |

### Message Types

```go
// AgentStatusEvent is published on agent status transitions.
// Published to both grove.{groveId}.agent.status and agent.{agentId}.status.
type AgentStatusEvent struct {
    AgentID         string `json:"agentId"`
    Status          string `json:"status"`
    SessionStatus   string `json:"sessionStatus,omitempty"`
    ContainerStatus string `json:"containerStatus,omitempty"`
    Timestamp       string `json:"timestamp"`
}

// AgentCreatedEvent is published when an agent is created.
type AgentCreatedEvent struct {
    AgentID  string `json:"agentId"`
    Name     string `json:"name"`
    Template string `json:"template,omitempty"`
    GroveID  string `json:"groveId"`
    Status   string `json:"status"`
}

// AgentDeletedEvent is published when an agent is deleted.
type AgentDeletedEvent struct {
    AgentID string `json:"agentId"`
}

// GroveUpdatedEvent is published when grove metadata changes.
type GroveUpdatedEvent struct {
    GroveID string            `json:"groveId"`
    Name    string            `json:"name,omitempty"`
    Labels  map[string]string `json:"labels,omitempty"`
}

// BrokerGroveEvent is published when a broker connects/disconnects from a grove.
type BrokerGroveEvent struct {
    BrokerID   string `json:"brokerId"`
    BrokerName string `json:"brokerName,omitempty"`
}

// BrokerStatusEvent is published on broker heartbeat or status changes.
type BrokerStatusEvent struct {
    BrokerID string `json:"brokerId"`
    Status   string `json:"status"`
}
```

---

## Handler Integration Points

Each handler calls the publisher **after** the store operation succeeds. The call is a single line appended to the success path.

### Agent Handlers (`handlers.go`)

| Handler | Line | Publish Call |
|---------|------|-------------|
| `createAgent()` | After `s.store.CreateAgent()` succeeds | `s.events.PublishAgentCreated(ctx, agent)` |
| `createGroveAgent()` | After `s.store.CreateAgent()` succeeds | `s.events.PublishAgentCreated(ctx, agent)` |
| `updateAgentStatus()` | After `s.store.UpdateAgent()` succeeds | `s.events.PublishAgentStatus(ctx, agent)` |
| `handleAgentLifecycle()` | After lifecycle action completes (start/stop/restart) | `s.events.PublishAgentStatus(ctx, agent)` |
| `deleteAgent()` | After `s.store.DeleteAgent()` succeeds | `s.events.PublishAgentDeleted(ctx, agentID, groveID)` |
| `deleteGroveAgent()` | After `s.store.DeleteAgent()` succeeds | `s.events.PublishAgentDeleted(ctx, agentID, groveID)` |

### Grove Handlers (`handlers.go`)

| Handler | Line | Publish Call |
|---------|------|-------------|
| `updateGrove()` | After `s.store.UpdateGrove()` succeeds | `s.events.PublishGroveUpdated(ctx, grove)` |
| `deleteGrove()` | After `s.store.DeleteGrove()` succeeds | `s.events.PublishGroveDeleted(ctx, groveID)` |

### Broker Handlers (`server.go`, `handlers_brokers.go`)

| Handler | Line | Publish Call |
|---------|------|-------------|
| `controlChannel.SetOnDisconnect` callback | After marking broker offline | `s.events.PublishBrokerDisconnected(ctx, brokerID, groveIDs)` |
| `markBrokerOnline()` | After marking broker online | `s.events.PublishBrokerConnected(ctx, brokerID, brokerName, groveIDs)` |
| `handleGroveRegister()` | After registering broker to grove | `s.events.PublishBrokerConnected(ctx, brokerID, brokerName, []string{groveID})` |

---

## Server Integration

### Server Struct

```go
type Server struct {
    // ... existing fields ...
    events EventPublisher // NATS event publisher (nil when NATS disabled)
}
```

### Initialization (in `New()`)

```go
// Initialize NATS event publisher if configured
if cfg.NATSUrl != "" {
    publisher, err := NewNATSEventPublisher(cfg.NATSUrl, cfg.NATSToken)
    if err != nil {
        slog.Warn("Failed to connect to NATS — events disabled", "error", err)
    } else {
        srv.events = publisher
        slog.Info("NATS event publisher enabled", "servers", cfg.NATSUrl)
    }
}
```

### Shutdown (in `Shutdown()`)

```go
// Close NATS event publisher
if s.events != nil {
    s.events.Close()
}
```

### Setter (for `cmd/server.go` initialization)

```go
func (s *Server) SetEventPublisher(ep EventPublisher) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.events = ep
}
```

---

## NATS Connection Management

The `NATSEventPublisher` implementation wraps `nats.Conn` with:

- **Connect options:** `nats.MaxReconnects(-1)` (unlimited reconnect), `nats.ReconnectWait(2s)`, `nats.Name("scion-hub")`.
- **Token auth:** `nats.Token(token)` when token is configured.
- **Disconnect handler:** Logs warnings on disconnect, info on reconnect.
- **Graceful drain:** `conn.Drain()` on `Close()` — flushes pending publishes before disconnecting.
- **No connection blocking:** `nats.Connect()` is called with `nats.RetryOnFailedConnect(true)` so the Hub starts even if NATS is unreachable. The nats.go client will keep retrying in the background.

```go
type NATSEventPublisher struct {
    conn *nats.Conn
}

func NewNATSEventPublisher(url, token string) (*NATSEventPublisher, error) {
    opts := []nats.Option{
        nats.Name("scion-hub"),
        nats.MaxReconnects(-1),
        nats.ReconnectWait(2 * time.Second),
        nats.RetryOnFailedConnect(true),
        nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
            if err != nil {
                slog.Warn("NATS disconnected", "error", err)
            }
        }),
        nats.ReconnectHandler(func(nc *nats.Conn) {
            slog.Info("NATS reconnected", "server", nc.ConnectedUrl())
        }),
    }
    if token != "" {
        opts = append(opts, nats.Token(token))
    }

    nc, err := nats.Connect(url, opts...)
    if err != nil {
        return nil, fmt.Errorf("nats connect: %w", err)
    }

    return &NATSEventPublisher{conn: nc}, nil
}
```

### Publish Helper

Each typed publish method serializes the event to JSON and calls `conn.Publish()`. Errors are logged but not returned.

```go
func (p *NATSEventPublisher) publish(subject string, event interface{}) {
    data, err := json.Marshal(event)
    if err != nil {
        slog.Error("Failed to marshal NATS event", "subject", subject, "error", err)
        return
    }
    if err := p.conn.Publish(subject, data); err != nil {
        slog.Error("Failed to publish NATS event", "subject", subject, "error", err)
    }
}
```

---

## `cmd/server.go` Integration

The server command creates the publisher and injects it into the Hub.

```go
// After creating Hub server
if enableHub && hubSrv != nil {
    // Initialize NATS event publisher if configured
    natsURL := cfg.NATS.URL
    if envURL := os.Getenv("SCION_SERVER_NATS_URL"); envURL != "" {
        natsURL = envURL
    }
    if natsURL != "" {
        natsToken := cfg.NATS.Token
        if envToken := os.Getenv("SCION_SERVER_NATS_TOKEN"); envToken != "" {
            natsToken = envToken
        }
        publisher, err := hub.NewNATSEventPublisher(natsURL, natsToken)
        if err != nil {
            log.Printf("Warning: NATS event publisher failed to initialize: %v", err)
        } else {
            hubSrv.SetEventPublisher(publisher)
            log.Printf("NATS event publisher enabled: %s", natsURL)
        }
    }
}
```

---

## Health Check Integration

The `/readyz` endpoint should report NATS connectivity when publishing is enabled.

```json
{
  "status": "healthy",
  "uptime": "1h2m3s",
  "nats": {
    "enabled": true,
    "connected": true
  }
}
```

When NATS is enabled but disconnected, `/readyz` continues to return 200 (NATS is best-effort), but the status field reflects the current state. This differs from the web frontend where NATS disconnection returns 503, because the Hub's primary function is the API — NATS is supplementary.

---

## Runtime Broker Agent Status Updates

When a Runtime Broker reports an agent status change via `PUT /api/v1/agents/{id}/status`, the Hub's `updateAgentStatus()` handler writes to the database and then calls `s.events.PublishAgentStatus()`. This is the primary real-time update path.

**Consolidated architecture (in-process):**

```
Runtime Broker → Hub API (updateAgentStatus) → Database → ChannelEventPublisher → SSE → Browser
```

**NATS architecture (multi-node):**

```
Runtime Broker → Hub API (updateAgentStatus) → Database → NATSEventPublisher → NATS → Web BFF SSE → Browser
```

In both cases, the broker does not publish events directly. All event publishing is centralized in the Hub. The `EventPublisher` interface abstracts the delivery mechanism.

---

## Testing

### Unit Tests

- `TestEventPublisherNil` — Verify nil publisher methods don't panic.
- `TestPublishAgentStatus` — Verify correct subjects and payload for dual-publish.
- `TestPublishAgentCreated` — Verify grove-scoped and agent-scoped subjects.
- `TestPublishAgentDeleted` — Verify both subjects receive the event.
- `TestPublishGroveUpdated` — Verify grove subject and payload.
- `TestPublishBrokerConnected` — Verify per-grove broker events.

### Integration Tests

Use an embedded NATS server (`github.com/nats-io/nats-server/v2/server`) for testing:

```go
func startTestNATS(t *testing.T) (*server.Server, string) {
    opts := &server.Options{Port: -1} // Random port
    ns, err := server.NewServer(opts)
    require.NoError(t, err)
    ns.Start()
    t.Cleanup(ns.Shutdown)
    return ns, ns.ClientURL()
}
```

### Manual Testing

```bash
# Terminal 1: Start NATS
docker run -p 4222:4222 nats:latest

# Terminal 2: Subscribe to all events
nats sub ">"

# Terminal 3: Start Hub with NATS enabled
scion server start --enable-hub --enable-runtime-broker --dev-auth \
  --nats-url nats://localhost:4222

# Terminal 4: Create an agent and observe events
export SCION_DEV_TOKEN=<token>
scion agent start --name test-agent
# → NATS subscriber should show grove.{id}.agent.created
# → NATS subscriber should show grove.{id}.agent.status (status=running)
```

---

## Implementation Milestones

### Phase 1: Core Publisher

1. Add `NATSConfig` to `GlobalConfig` and `ServerConfig`.
2. Add `--nats-url` and `--nats-token` flags to `cmd/server.go`.
3. Add koanf env mappings for `SCION_SERVER_NATS_URL` and `SCION_SERVER_NATS_TOKEN`.
4. Implement `EventPublisher` interface and `NATSEventPublisher` in `pkg/hub/events.go`.
5. Add `events` field to `Server` struct with `SetEventPublisher()` setter.
6. Initialize publisher in `cmd/server.go` and inject into Hub.
7. Add `Close()` to shutdown path.
8. Unit tests with nil publisher and mock NATS.

### Phase 2: Handler Integration

1. Add publish calls to agent handlers: `createAgent`, `createGroveAgent`, `updateAgentStatus`, `handleAgentLifecycle`, `deleteAgent`, `deleteGroveAgent`.
2. Add publish calls to grove handlers: `updateGrove`, `deleteGrove`.
3. Add publish calls to broker handlers: `markBrokerOnline`, `controlChannel.SetOnDisconnect`, `handleGroveRegister`.
4. Integration tests with embedded NATS server.

### Phase 3: Health & Observability

1. Add NATS status to `/readyz` response.
2. Add structured logging for publish activity at debug level.
3. End-to-end manual testing with web frontend SSE.

---

## Dependencies

### Consolidated architecture (recommended)

- `github.com/gorilla/sessions` — Session cookie management for browser auth.
- No NATS dependencies. The `ChannelEventPublisher` uses only the standard library.

### NATS architecture (multi-node, if needed later)

- `github.com/nats-io/nats.go` — Go NATS client. Adds 3 modules, ~5 MB to binary.
- `github.com/nats-io/nats-server/v2` — Embedded NATS server for tests (or optional embedded mode). Adds 11 modules, ~15 MB to binary.

---

## Alternative: Consolidated Go Binary

### Motivation

The current architecture requires three runtime processes for real-time web updates:

```
Browser → Koa BFF (Node.js) → Hub API (Go) → NATS Server → Koa BFF → Browser
           session/SSR         state/API      pub/sub        SSE bridge
```

The Koa BFF exists to bridge browser concerns (cookies, sessions, SSR) to the Hub API (JWT tokens, REST). NATS exists solely to connect the Hub's state changes to the BFF's SSE endpoint. This is a significant amount of infrastructure for what is fundamentally "notify the browser when something changes."

Rolling the BFF's functionality into the Go binary eliminates two runtime dependencies (Node.js, NATS) and three network hops (API proxy, NATS pub/sub, PTY proxy) for single-node deployments.

### What the Koa BFF Does Today

| Function | Lines | Go Equivalent |
|----------|-------|---------------|
| **API proxy** — forwards `/api/*` to Hub with auth headers | ~200 | **Eliminated.** Go server IS the API. |
| **Session management** — wraps Hub JWTs in signed httpOnly cookies | ~200 | `gorilla/sessions` + `securecookie` |
| **OAuth browser flow** — redirects, CSRF state, callback, domain check | ~480 | Same logic in Go handlers (~300 lines) |
| **SSE/NATS bridge** — subscribes to NATS, streams to EventSource | ~220 | **Eliminated for single-node.** In-process channel. |
| **WebSocket PTY proxy** — proxies browser WS to Hub PTY endpoint | ~220 | **Eliminated.** Hub already has PTY handlers. |
| **SSR** — `@lit-labs/ssr` renders Lit components server-side | ~500 | See SSR options below |
| **Static assets** — serves Vite build output from `/assets/` | ~10 | `http.FileServer` or `//go:embed` |
| **Security headers** — CSP, HSTS, X-Frame-Options | ~70 | Middleware (~30 lines) |
| **Health checks** — `/healthz`, `/readyz` | ~80 | Already exists in Hub |
| **Dev auth** — reads dev token, creates dev user session | ~200 | Already exists in Hub |
| **Request logging** — structured JSON, request IDs | ~210 | Already exists in Hub (`slog`) |

**Total Koa server:** ~2,400 lines across 15 files.
**Eliminated by consolidation:** ~650 lines (API proxy, SSE bridge, PTY proxy).
**Already exists in Hub:** ~560 lines (health, dev auth, logging).
**Needs porting:** ~700 lines (session, OAuth browser flow, security headers).
**SSR:** ~500 lines — depends on approach chosen (see below).

### Consolidated Architecture

```
scion server start --enable-hub --enable-runtime-broker --enable-web
```

One binary. One process. Three capabilities toggled by flags.

```
Browser (cookie auth)
  ↕
Go Server
  ├── /assets/*              → static file serving (embedded or from disk)
  ├── /auth/*                → OAuth browser flow (sessions, cookies)
  ├── /events?sub=...        → SSE (in-process event bus — no NATS)
  ├── /api/v1/*              → Hub API (direct handler, no proxy)
  ├── /api/agents/*/pty      → WebSocket PTY (direct, no proxy)
  ├── /healthz, /readyz      → health checks
  └── /*                     → SPA shell (Go template or static HTML)
```

**Network hops eliminated:** 3 (API proxy, SSE/NATS bridge, PTY proxy).
**External runtime dependencies eliminated:** 2 (Node.js, NATS server).
**Separate processes eliminated:** 1 (Koa web server).

### In-Process Event Bus (`ChannelEventPublisher`)

For single-node deployments, the `EventPublisher` interface gets a second implementation that uses Go channels instead of NATS. The SSE endpoint subscribes to the same event bus in-process — no serialization, no network, no external dependencies.

```go
// ChannelEventPublisher fans out events to in-process SSE subscribers.
// Implements EventPublisher. Zero external dependencies.
type ChannelEventPublisher struct {
    mu          sync.RWMutex
    subscribers map[string][]chan Event  // subject pattern → subscriber channels
}

// Event is the in-process representation of a published event.
type Event struct {
    Subject string
    Data    []byte  // JSON-encoded payload
}

// Subscribe returns a channel that receives events matching the given
// subject pattern. Supports NATS-style wildcards (* and >).
// The caller must call Unsubscribe when done.
func (p *ChannelEventPublisher) Subscribe(pattern string) (<-chan Event, func()) {
    ch := make(chan Event, 64)
    p.mu.Lock()
    p.subscribers[pattern] = append(p.subscribers[pattern], ch)
    p.mu.Unlock()

    unsubscribe := func() {
        p.mu.Lock()
        defer p.mu.Unlock()
        subs := p.subscribers[pattern]
        for i, s := range subs {
            if s == ch {
                p.subscribers[pattern] = append(subs[:i], subs[i+1:]...)
                close(ch)
                break
            }
        }
    }
    return ch, unsubscribe
}
```

The SSE HTTP handler creates a subscription, reads from the channel, and writes SSE frames:

```go
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
    flusher, ok := w.(http.Flusher)
    if !ok {
        http.Error(w, "streaming not supported", http.StatusInternalServerError)
        return
    }

    subjects := r.URL.Query()["sub"]
    // ... validate subjects, check auth ...

    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")
    w.Header().Set("X-Accel-Buffering", "no")
    flusher.Flush()

    // Subscribe to requested subjects
    ch, unsubscribe := s.events.(*ChannelEventPublisher).Subscribe(subjects...)
    defer unsubscribe()

    eventID := 0
    for {
        select {
        case event := <-ch:
            eventID++
            fmt.Fprintf(w, "id: %d\nevent: update\ndata: %s\n\n",
                eventID, buildSSEPayload(event))
            flusher.Flush()
        case <-r.Context().Done():
            return
        case <-time.After(30 * time.Second):
            fmt.Fprintf(w, ":heartbeat %d\n\n", time.Now().UnixMilli())
            flusher.Flush()
        }
    }
}
```

### Publisher Selection

The publisher implementation is chosen at startup based on configuration:

| Configuration | Publisher | SSE Delivery |
|---------------|----------|-------------|
| No NATS URL, `--enable-web` | `ChannelEventPublisher` | In-process (default) |
| `--nats-url` provided | `NATSEventPublisher` | Via NATS (multi-node or external BFF) |
| Neither | `nil` (no-op) | No real-time updates |

```go
// In cmd/server.go initialization
if natsURL != "" {
    // Multi-node: use NATS for cross-process fan-out
    publisher, _ := hub.NewNATSEventPublisher(natsURL, natsToken)
    hubSrv.SetEventPublisher(publisher)
} else if enableWeb {
    // Single-node: use in-process channels (no NATS needed)
    publisher := hub.NewChannelEventPublisher()
    hubSrv.SetEventPublisher(publisher)
}
```

The `EventPublisher` interface from the main design is unchanged. Handlers call `s.events.PublishAgentStatus(...)` without knowing which implementation is active.

### Is NATS Even Needed?

In the consolidated architecture, the Hub is a **single process** through which **all** state changes flow. Every agent create, status update, and delete goes through Hub handlers. The SSE endpoint lives in the same process and sees every event directly via Go channels. There is no second process to notify.

This means NATS is not needed for the common deployment — and may never be needed for Scion's architecture, where the Hub is a single centralized server (not a horizontally-scaled cluster).

#### When NATS Would Be Needed

NATS (or any cross-process pub/sub) is only required if:

1. **Multiple Hub instances** behind a load balancer — an SSE client connected to Hub A needs to see events from Hub B. Scion doesn't support this today.
2. **Separate web frontend process** — the Koa BFF or a future alternative runs on a different machine from the Hub. The consolidated architecture eliminates this.
3. **External event consumers** — third-party tools want to tap into the Scion event stream. Not a current requirement.

None of these apply to Scion at its current stage.

#### Alternatives to NATS

If cross-process event delivery is needed in the future, or a more structured in-process pub/sub is desired, there are several options. Ranked by complexity:

| Approach | Binary Delta | New Deps | In-Process | Cross-Process | Topic Filtering |
|----------|-------------|----------|-----------|--------------|----------------|
| **Go channels** | 0 | 0 | Yes | No | Custom (~40 lines) |
| **mangos/v3** (nanomsg) | ~2 MB | 2 | Yes (`inproc://`) | Yes (`tcp://`, `ipc://`) | Prefix match (built-in) |
| **Hub serves SSE directly** | 0 | 0 | Yes | N/A | Custom |
| **PostgreSQL `LISTEN/NOTIFY`** | 0 | Postgres | No | Yes | Channel name only |
| **Redis Pub/Sub** | varies | Redis | No | Yes | Pattern match |
| **nats.go client** | ~5 MB | 3 | No | Yes (needs server) | Wildcards (`*`, `>`) |
| **Embedded NATS server** | ~15 MB | 11 | Yes | Yes | Wildcards (`*`, `>`) |

##### mangos/v3 (nanomsg Scalability Protocols)

[mangos/v3](https://github.com/nanomsg/mangos) is a pure-Go implementation of the Scalability Protocols (nanomsg/nng). It provides idiomatic Go pub/sub sockets with multiple transport options.

**How it would work:**

```go
import (
    "go.nanomsg.org/mangos/v3/protocol/pub"
    "go.nanomsg.org/mangos/v3/protocol/sub"
    _ "go.nanomsg.org/mangos/v3/transport/inproc"
)

// Publisher (in Hub)
pubSock, _ := pub.NewSocket()
pubSock.Listen("inproc://events")        // In-process: zero-copy
// pubSock.Listen("ipc:///tmp/scion.sock") // Cross-process: same code
// pubSock.Listen("tcp://0.0.0.0:5555")    // Network: same code

// Subscriber (in SSE handler)
subSock, _ := sub.NewSocket()
subSock.Dial("inproc://events")
subSock.SetOption(mangos.OptionSubscribe, []byte("grove.abc."))  // Prefix filter
```

**Advantages over Go channels:**
- Built-in prefix-based topic filtering (no custom matching code).
- Transparent transport upgrade: switch from `inproc://` to `ipc://` or `tcp://` by changing the URL string alone. No code changes, no separate server process. This enables cross-process pub/sub (e.g., a separate web frontend process on the same machine) without introducing NATS.
- Built-in flow control and backpressure handling.
- Small footprint: 2 new modules, ~2 MB binary delta.

**Limitations:**
- Topic filtering is **byte-prefix matching only**. Subscribing to `grove.abc.` matches all events under that grove, which covers the primary use case (`grove.{id}.>`). However, single-token wildcards like `grove.*.summary` (dashboard view) require subscriber-side filtering — mangos doesn't support NATS-style `*` or `>` wildcards.
- No clustering or federation. For true multi-node with multiple Hub instances, you'd still need NATS or similar.
- Adds a dependency for a problem that Go channels solve in ~50 lines for the single-process case.

**When mangos makes sense:** If there's a concrete need for cross-process pub/sub without an external server — for example, running the web frontend as a separate process on the same host connected via `ipc://` unix domain socket. It provides a middle ground between "everything in one process" and "deploy a full NATS server."

##### Go channels vs. mangos: assessment

For Scion's current architecture (single Hub process, SSE endpoint co-located), Go channels are sufficient. The `EventPublisher` interface already provides the abstraction boundary for swapping implementations later. mangos would be a reasonable choice if the project outgrows in-process channels but doesn't warrant a full NATS deployment — the `inproc://` → `ipc://` transport upgrade path is genuinely useful for incremental scaling.

For Scion, the "Hub serves SSE directly" row is the key insight: since the Hub is the single source of truth and serves the SSE endpoint, there is no need for a pub/sub intermediary at all. The `ChannelEventPublisher` (in-process Go channels) is sufficient for the current stage.

#### Recommendation

**Start with `ChannelEventPublisher` only.** Do not add NATS as a dependency until there is a concrete multi-node requirement. The `EventPublisher` interface allows swapping in `NATSEventPublisher` later without changing any handler code. YAGNI applies here — the interface abstraction provides the escape hatch; the dependency can wait.

### SSR Decision

Server-side rendering is the only Koa function that doesn't port straightforwardly. The current renderer uses `@lit-labs/ssr`, which requires a Node.js runtime. Options:

**Option A: SPA shell — no SSR (recommended).**

The Go server returns a minimal HTML page with `<script>` tags. Lit components render entirely client-side. The `__SCION_DATA__` hydration pattern still works — embed initial JSON in the HTML, the client reads it on load.

```go
// Go html/template for the SPA shell
const spaShell = `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Scion</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/...shoelace...">
  <link rel="stylesheet" href="/assets/main.css">
</head>
<body>
  <scion-app></scion-app>
  <script id="__SCION_DATA__" type="application/json">{{.InitialData}}</script>
  <script type="module" src="/assets/main.js"></script>
</body>
</html>`
```

- Pro: Zero Node.js runtime dependency. Simplest path.
- Con: Brief flash of unstyled content on first load (mitigated with inline critical CSS).
- Note: The app requires authentication, so search engines can't see content anyway. SSR provides no SEO benefit for this use case.

**Option B: Go `html/template` for the layout shell.**

Render the full page layout (sidebar, header, breadcrumbs) as static HTML via Go templates. Lit components hydrate only the dynamic content areas. This prevents the FOUC that Option A has.

- Pro: No flash of content. No Node.js.
- Con: Layout logic is duplicated between Go templates and Lit components. Changes to navigation require updating both.

**Option C: Keep a thin Node sidecar for SSR.**

A small Node process runs `@lit-labs/ssr` and is called by the Go server via HTTP or stdio for rendering. Only used for initial page loads; subsequent navigations are client-side.

- Pro: Full SSR fidelity.
- Con: Reintroduces Node.js as a runtime dependency. Adds process management complexity. Partially defeats the consolidation goal.

**Recommendation:** Option A. For an authenticated internal tool, SSR provides no meaningful benefit. The FOUC can be addressed with inline critical CSS (which the current Koa template already includes — that CSS moves to the Go template unchanged).

### Client Assets: Build-Time vs Runtime

The client-side code (Lit components, xterm.js, CSS) still needs Node.js tooling to **build**. This is a build-time dependency, not a runtime one:

```bash
# Build step (CI or developer machine)
cd web && npm run build    # → dist/client/assets/main.js + chunks

# The Go binary serves the built assets
scion server start --enable-web --web-assets-dir ./web/dist/client
```

Alternatively, assets can be embedded in the binary via `//go:embed`:

```go
//go:embed web/dist/client
var clientAssets embed.FS
```

This makes the scion binary fully self-contained — a single file that includes the API server, runtime broker, and web UI with all client assets.

### Web-Specific Configuration

The `--enable-web` flag adds web-serving configuration to the Hub:

```go
type ServerConfig struct {
    // ... existing fields ...

    // Web frontend settings (when --enable-web is set)
    WebEnabled    bool
    WebAssetsDir  string   // Path to client assets (default: embedded)
    SessionSecret string   // HMAC secret for session cookies
    BaseURL       string   // Public URL for OAuth redirects
}
```

```
scion server start --enable-hub --enable-runtime-broker --enable-web \
  --session-secret "$(openssl rand -hex 32)" \
  --base-url https://scion.example.com
```

Environment variables: `SCION_SERVER_WEB_ENABLED`, `SCION_SERVER_SESSION_SECRET`, `SCION_SERVER_BASE_URL`.

### Session Management in Go

The Koa BFF uses `koa-session` to store Hub JWTs in signed httpOnly cookies. The Go equivalent uses `gorilla/sessions`:

```go
import "github.com/gorilla/sessions"

var sessionStore = sessions.NewCookieStore([]byte(sessionSecret))

func init() {
    sessionStore.Options = &sessions.Options{
        Path:     "/",
        MaxAge:   86400, // 24 hours
        HttpOnly: true,
        Secure:   true,  // HTTPS only in production
        SameSite: http.SameSiteLaxMode,
    }
}

// In OAuth callback handler:
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
    session, _ := sessionStore.Get(r, "scion_sess")
    // Exchange code for tokens via internal Hub call (no HTTP — direct function call)
    tokens, user, err := s.exchangeOAuthCode(r.Context(), code, provider)
    session.Values["user"] = user
    session.Values["accessToken"] = tokens.AccessToken
    session.Values["refreshToken"] = tokens.RefreshToken
    session.Save(r, w)
    http.Redirect(w, r, returnTo, http.StatusFound)
}
```

Key difference from the current architecture: the OAuth callback calls the Hub's token exchange logic **directly as a function call**, not via an HTTP proxy. No network hop, no serialization overhead.

### Auth Middleware Layering

The consolidated server needs two auth paths on the same mux:

| Path prefix | Auth method | Consumer |
|-------------|------------|----------|
| `/api/v1/*` | Bearer JWT / API key / dev token | CLI, brokers, external API clients |
| `/auth/*`, `/*` (pages), `/events` | Session cookie | Browser |
| `/healthz`, `/readyz` | None | Load balancers |

The existing `UnifiedAuthMiddleware` handles API auth. A new `SessionAuthMiddleware` handles browser auth. Route registration determines which applies:

```go
// API routes — existing JWT/API key auth
apiMux := http.NewServeMux()
apiMux.Handle("/api/v1/", UnifiedAuthMiddleware(s.authConfig)(s.apiHandler()))

// Web routes — session cookie auth
webMux := http.NewServeMux()
webMux.Handle("/auth/", s.oauthRoutes())
webMux.Handle("/events", SessionAuthMiddleware(sessionStore)(s.sseHandler()))
webMux.Handle("/assets/", http.FileServer(http.FS(clientAssets)))
webMux.Handle("/", SessionAuthMiddleware(sessionStore)(s.spaHandler()))

// Combined
mainMux := http.NewServeMux()
mainMux.Handle("/api/", apiMux)
mainMux.Handle("/", webMux)
```

### Impact on Open Questions

Under the consolidated architecture, several open questions simplify or become irrelevant:

| Open Question | Impact |
|---------------|--------|
| **1. Dashboard summaries** | Unchanged — still needs a periodic publisher or reactive approach. |
| **2. Harness event relay** | Simplified for co-located broker. The broker's status monitor can publish directly to the `ChannelEventPublisher` in-process. No NATS needed. |
| **3. NATS deployment topology** | **Irrelevant for single-node.** NATS is only needed for multi-node deployments where the web frontend or broker runs on a different machine. |
| **4. Missing `grove.created`** | Unchanged — should still be added. |
| **5. Config naming** | **Irrelevant.** No separate BFF process means no config naming conflict. `SCION_SERVER_NATS_URL` is the only env var, used only when multi-node NATS is needed. |

### Migration Path

The consolidation doesn't need to happen all at once. The `EventPublisher` interface is the stable contract that enables incremental migration:

1. **Phase 1 (this design):** Implement `EventPublisher` interface + `NATSEventPublisher`. The existing Koa BFF continues to work. Hub publishes to NATS, BFF subscribes.

2. **Phase 2:** Implement `ChannelEventPublisher` + Go SSE endpoint. Add `--enable-web` flag. The Go server can serve the SPA shell and SSE alongside the API. The Koa BFF is still available but optional.

3. **Phase 3:** Port OAuth browser flow and session management to Go. Add cookie-based auth middleware. At this point the Koa BFF is fully redundant for single-node deployments.

4. **Phase 4:** Remove Koa BFF from the default deployment. Keep it as an option for custom frontends or multi-node setups where the web server runs separately.

### What This Does NOT Change

- **Client-side code** — Lit components, xterm.js, Vite build, `StateManager`, `SSEClient` — all remain TypeScript. The browser code is unchanged.
- **`EventPublisher` interface** — Same interface, same handler integration points, same message formats. Only the implementation behind the interface changes.
- **Multi-node support** — `NATSEventPublisher` remains available for deployments where the web frontend runs on a different machine from the Hub.
- **Build tooling** — Node.js is still required at build time to compile client assets. Only the runtime dependency is removed.

---

## Non-Goals

- **NATS as a required dependency.** Under the consolidated architecture (recommended), NATS is not needed. The `ChannelEventPublisher` delivers events in-process via Go channels. NATS support is retained as an optional `NATSEventPublisher` behind the same interface, available if multi-node requirements emerge.
- **NATS JetStream / persistence.** The publisher uses fire-and-forget delivery. Message persistence is not needed because the web frontend fetches the full state snapshot on page load and SSE reconnects restart from the current state, not from a historical offset.
- **Agent harness event relay.** Heavy events (`agent.{id}.event`) from the harness status stream are not part of this design. Under the consolidated architecture, these can flow in-process from a co-located broker's status monitor to the `ChannelEventPublisher`. For remote brokers, they would flow through the existing WebSocket control channel to the Hub, then to subscribers. See Open Question 2 below.
- **NATS as a message bus for inter-service communication.** The Hub-to-Broker communication continues to use the existing HTTP/WebSocket control channel. The event publisher is strictly for fan-out notifications to browser clients.

---

## Open Questions

### 1. Dashboard grove summaries (`grove.*.summary`)

The client `StateManager` subscribes to `grove.*.summary` for the dashboard view, but this design only covers event-driven publishes triggered by state changes. Periodic grove summary aggregation (agent counts per grove, overall status rollups) requires a separate publishing mechanism — likely a timer loop in the Hub that queries the store and publishes a summary for each grove at a fixed interval (e.g., every 30s).

**Options:**
- **(a)** Add a periodic summary publisher goroutine to this design.
- **(b)** Defer summaries; the dashboard can use the existing REST API for initial load and rely on `grove.{id}.agent.*` events for incremental updates (requires client-side aggregation logic).
- **(c)** Publish summaries reactively — recompute and publish a grove summary whenever any agent event occurs in that grove, debounced to avoid flooding.

### 2. Harness event relay (`agent.{id}.event`)

The frontend's `state.ts` handles `agent.{agentId}.event` subjects for agent detail views (tool use, thinking, harness output — heavy events up to 10 KB). These originate from the Runtime Broker's status monitor watching container output. The question is how they reach NATS:

**Options:**
- **(a) Broker publishes to NATS directly.** The Runtime Broker gets its own NATS client and publishes `agent.{id}.event` to NATS. Simple, but means the broker also needs NATS configuration, and there are now two NATS publishers to coordinate.
- **(b) Broker → Hub → NATS.** The broker relays heavy events to the Hub via the existing WebSocket control channel or a new HTTP endpoint, and the Hub publishes them to NATS. Centralizes all NATS publishing but adds latency and Hub load for high-volume harness output.
- **(c) Defer to a separate design.** This design covers Hub-originated state changes only. Harness event relay is a distinct pipeline with different performance characteristics (high volume, large payloads, low latency requirements) and warrants its own design document.

### 3. NATS server deployment topology

The web BFF subscribes to NATS, the Hub publishes to NATS — both must connect to the same server. The design doesn't specify where the NATS server runs.

**Options:**
- **(a) External NATS server.** Deployed separately via Docker, systemd, or managed service. Simplest operationally for production; requires an extra process for local dev (`docker run nats:latest`).
- **(b) Embedded NATS server in the Hub.** Using `github.com/nats-io/nats-server/v2/server` as a library, the Hub starts an in-process NATS server when `--nats-embedded` is set. Eliminates an external dependency for single-node deployments at the cost of additional binary size and operational coupling.
- **(c) Both.** Embedded for single-node / dev mode, external for production. The `--nats-url` flag points to an external server; `--nats-embedded` starts one in-process and ignores `--nats-url`.

#### Binary size impact of embedded NATS

Measured against the current scion binary:

| Component | Standalone size | New module deps for scion |
|-----------|----------------|---------------------------|
| Current scion binary | 113 MB | — |
| `nats.go` client only | 8 MB | 3 (`nats.go`, `nkeys`, `nuid`) |
| `nats-server` embedded | 20 MB | 11 (adds `jwt/v2`, `go-tpm`, `go-tpm-tools`, `highwayhash`, `automaxprocs`, etc.) |

The actual delta on the scion binary would be smaller than the standalone numbers because shared dependencies like `golang.org/x/crypto` and `golang.org/x/sys` are already in the dependency tree. Estimates:

- **Client only:** ~5–6 MB added (~5% increase)
- **Embedded server:** ~15–17 MB added (~14% increase)

The embedded server pulls in heavier transitive dependencies (`go-tpm` for TPM hardware auth, `antithesis-sdk-go` for fault injection) that aren't needed for a simple embedded use case, but Go links them regardless. At 113 MB baseline, the ~15% increase is proportionally modest.

### 4. Missing `grove.{groveId}.created` event

The design includes `grove.updated` and `grove.deleted` but not `grove.created`. If a user is on the dashboard and a new grove is created, they wouldn't see it without a page refresh. This should likely be added for consistency:

| Subject | Trigger | Payload |
|---------|---------|---------|
| `grove.{groveId}.created` | Grove created | `GroveCreatedEvent` |

The dashboard subscription (`grove.*.summary`) would not match `grove.{id}.created` since `*` only matches single tokens, not multi-level. A dashboard subscriber would need `grove.>` to catch grove creation events, or the client could poll groves on a timer as a simpler alternative.

### 5. Config naming: `SCION_SERVER_NATS_URL` vs `SCION_NATS_URL`

The web frontend uses `SCION_NATS_URL` / `NATS_URL` for its subscriber connection. The Hub design uses `SCION_SERVER_NATS_URL` following the `SCION_SERVER_*` prefix convention for Hub configuration. Both typically point at the same NATS server.

**Options:**
- **(a) Keep separate prefixes.** `SCION_SERVER_NATS_URL` for the Hub, `SCION_NATS_URL` for the web BFF. They're different processes with different config namespaces, even though the value is usually the same. Explicit and consistent with existing patterns.
- **(b) Shared `SCION_NATS_URL`.** Both the Hub and web BFF read `SCION_NATS_URL`. Simpler for deployment (one env var), but breaks the `SCION_SERVER_*` convention for Hub config and could cause confusion if the Hub and BFF need different NATS servers in the future.
- **(c) Hub reads both.** The Hub checks `SCION_SERVER_NATS_URL` first, then falls back to `SCION_NATS_URL`. Pragmatic for single-node deployments while preserving the ability to override per-component.
