# Scion Hosted Mode Status Analysis

**Date:** January 26, 2026
**Scope:** Review of `@.design/hosted/**` against current codebase.

## 1. Executive Summary

The Scion Hosted architecture is partially implemented. The core "backend" components (Hub API, Runtime Host API, Client Libraries) have a solid foundation in Go. The "frontend" (Web Dashboard) has a parallel implementation in Node.js (Koa + Lit SSR) but is **not yet integrated** into the main `scion` Go binary as intended by the single-binary design goal.

## 2. Component Status Review

### 2.1 Hub API (`pkg/hub`)
*   **Status:** ✅ **Partially Complete**
*   **Implemented:**
    *   Basic server structure (`server.go`).
    *   Development Authentication (`devauth.go`, `dev-auth.md`).
    *   Database connection (SQLite).
*   **Missing / Gaps:**
    *   **NATS / Real-time Events:** No implementation found for NATS broadcasting or event loops in `pkg/hub`. This is critical for the "Snapshot + Delta" pattern described in `web-frontend-design.md`.
    *   **Secrets Management:** `pkg/hubclient` has secrets logic, but need to confirm server-side encryption/storage implementation.
    *   **WebSocket Control Plane:** While `pkg/runtimehost` exists, the complex WebSocket control channel for NAT traversal (Hub <-> Host) needs verification of full implementation beyond simple HTTP.

### 2.2 Runtime Host API (`pkg/runtimehost`)
*   **Status:** ✅ **Partially Complete**
*   **Implemented:**
    *   Server structure and mode switching (Connected vs Read-Only).
    *   Agent Manager adaptation (`agent.Manager`).
    *   **Co-location Dispatcher:** `cmd/server.go` contains logic (`registerGlobalGroveAndHost`, `newAgentDispatcherAdapter`) to automatically register the local host with the Hub, enabling a seamless "out of the box" experience.
*   **Missing:**
    *   Robust status reporting loop (dependent on NATS/Events).

### 2.3 Client Libraries (`pkg/hubclient`, `pkg/hostclient`)
*   **Status:** ✅ **Complete**
*   **Implemented:**
    *   Full CRUD for Agents, Groves, Users.
    *   Authentication helpers.
    *   Design matches `client-design.md`.

### 2.4 Web Frontend (`web/`)
*   **Status:** ⚠️ **Diverged / In Progress**
*   **Implemented:**
    *   Node.js Server (Koa) with SSR (`web/src/server`).
    *   Lit Components Structure.
    *   Milestones M1 & M2 (Server Foundation, Lit SSR) appear implemented in the `web/` directory.
*   **Critical Gap:**
    *   **Integration:** The `scion` CLI (`cmd/server.go`) **does not** have the `web` server component wired up. The flags `--enable-web` are defined in design but missing in `serverCmd`.
    *   **Architecture Conflict:** `web-frontend-design.md` relies on Node.js for SSR. `server-implementation-design.md` calls for a single Go binary.
        *   *Current Reality:* We have a Node.js app. To run as a single Go binary, we must either:
            1.  Drop SSR and serve as a static SPA (Client-Side Rendering only) embedded in Go.
            2.  Embed a JS runtime (heavy).
            3.  Accept that "Web" requires a separate process (Node.js) for full SSR features.

## 3. Inconsistencies & Edge Case Risks

### 3.1 The SSR vs. Single Binary Conflict
The biggest architectural risk is the requirement for `@lit-labs/ssr` (Node.js) vs. the "Single Binary" deployment model.
*   **Risk:** Users expecting `scion server start --enable-web` to just work will be disappointed if it requires a separate `npm start` process.
*   **Mitigation:** Define the Go binary's web role as "Static SPA Server" (Read-Only/Dashboard) and keep the Node.js server for the full "Hosted" experience, OR migrate the frontend to a pure Client-Side Rendering (CSR) model if single-binary is the priority.

### 3.2 Event Propagation (The "Live Wire")
The design relies heavily on NATS for real-time updates (`web-frontend-design.md`).
*   **Risk:** `pkg/hub` does not seem to have NATS embedded. If the Hub is supposed to be self-contained (SQLite + Go), introducing an external NATS dependency complicates the "Solo/Local" deployment significantly.
*   **Recommendation:** For local/solo mode, implement an in-memory event bus that mimics the NATS interface, so `scion server` doesn't require a Dockerized NATS sidecar.

### 3.3 Dev Auth vs. Production Auth
*   **Risk:** `dev-auth` is implemented, but the transition to Production Auth (OAuth) in the single binary is complex. The Go binary needs to handle OAuth callbacks if it's serving the frontend.
*   **Note:** `cmd/server.go` has `enableDevAuth`, which is good.

## 4. Next Logical Milestones

### M1: Bridge the Gap (Go <-> Web)
*   **Task:** Implement `scion server start --enable-web`.
*   **Approach:**
    1.  Add `//go:embed` to `cmd/server.go` (or `pkg/web`) to bundle the *built* frontend assets (`dist/client`).
    2.  Implement a simple HTTP server in Go that serves these static assets.
    3.  Configure the frontend build to output a "CSR-only" (Client-Side Rendering) bundle for this mode, bypassing the need for Node.js SSR in the CLI.

### M2: Event Bus Implementation
*   **Task:** Implement the Event Bus in `pkg/hub`.
*   **Approach:**
    1.  Define an `EventBus` interface.
    2.  Implement `MemoryEventBus` for local `scion server` usage.
    3.  Implement `NatsEventBus` for clustered/k8s usage.
    4.  Wire this into `AgentService` to publish status changes.

### M3: Web Frontend Completion
*   **Task:** Continue with Frontend Milestones (M6-M9) in the `web/` directory.
*   **Focus:** Ensure components degrade gracefully if SSR is not available (i.e., when running served by the Go binary).

### M4: Unified Auth
*   **Task:** Ensure the Go binary's web server respects the Hub's authentication (Dev Auth or tokens). The static assets served by Go will need to know how to authenticate against the Hub API (which is likely on the same port or `localhost:9810`).
