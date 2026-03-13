# Scion Docs Agent - Design Document

## Overview

A lightweight, standalone satellite service that answers user questions about Scion using Gemini 3.1 Flash-Lite. The service ships as a container deployed to Cloud Run, bundling a checkout of the Scion source code and documentation as context. A simple HTTP handler accepts a query, invokes the Gemini CLI in non-interactive mode with the repository as grounding context, and returns the answer.

This is intentionally separate from the main Scion codebase. It is a small, self-contained project with its own repository (or subdirectory), Dockerfile, and deployment pipeline.

## Goals

- Provide a public-facing Q&A endpoint for Scion users and contributors
- Answer questions about Scion usage, configuration, architecture, and source code
- Keep the service minimal: no database, no auth, no state
- Leverage existing deployment patterns (Cloud Build + Cloud Run) from `docs-site/`
- Use Gemini 3.1 Flash-Lite for fast, low-cost responses

## Architecture

```
┌─────────────┐     HTTP POST /ask      ┌──────────────────────────────┐
│   Client     │ ──────────────────────> │   Cloud Run Service          │
│ (browser,    │                         │                              │
│  curl, etc.) │ <───────────────────── │  ┌────────────────────────┐  │
│              │     JSON response       │  │  Go HTTP Handler       │  │
└─────────────┘                         │  │  - Validates query      │  │
                                        │  │  - Invokes gemini CLI   │  │
                                        │  │  - Returns response     │  │
                                        │  └────────────────────────┘  │
                                        │                              │
                                        │  /workspace/scion/           │
                                        │  (source + docs checkout)    │
                                        └──────────────────────────────┘
```

### Request Flow

1. Client sends `POST /ask` with `{"query": "How do I start an agent?"}`.
2. Go HTTP handler validates the query (length, rate limit).
3. Handler constructs a Gemini CLI invocation with the query as a prompt.
4. Gemini CLI runs against the local source checkout, using it as context.
5. Handler captures stdout, strips any ANSI escape codes, and returns the response as JSON.

## Container Image

### Dockerfile (Conceptual)

```dockerfile
# Stage 1: Clone repo and build the handler
FROM golang:1.25 AS builder

WORKDIR /build

# Clone the Scion repo at build time for latest content
ARG SCION_REPO=https://github.com/GoogleCloudPlatform/scion.git
ARG SCION_REF=main
RUN git clone --depth 1 --branch ${SCION_REF} ${SCION_REPO} /scion-source

# Build the docs-agent handler
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /docs-agent .

# Stage 2: Runtime
FROM node:20-slim

# Install Gemini CLI
RUN npm install -g @google/gemini-cli@latest \
    && npm cache clean --force

# Copy handler binary and source context
COPY --from=builder /docs-agent /usr/local/bin/docs-agent
COPY --from=builder /scion-source /workspace/scion

# Copy the system prompt
COPY system-prompt.md /etc/docs-agent/system-prompt.md

EXPOSE 8080
CMD ["docs-agent"]
```

Key points:
- The Scion repo is cloned at **image build time**, so updates require an image rebuild
- Gemini CLI is installed via npm (same pattern as `image-build/gemini/Dockerfile`)
- No scion-base image dependency; this is fully standalone
- The Go handler is a single static binary

### System Prompt

A `system-prompt.md` file will be included in the image and passed to the CLI via the `GEMINI_SYSTEM_MD` environment variable to completely override the default system instructions, giving Gemini its persona and constraints:

```markdown
You are the Scion Documentation Agent. You answer questions about Scion,
a container-based orchestration platform for concurrent LLM-based code agents.

Your knowledge comes from:
- The Scion documentation in /workspace/scion/docs-site/
- The Scion source code in /workspace/scion/
- Design documents in /workspace/scion/.design/

Rules:
- Answer concisely and accurately based on the available source material
- If you cannot find the answer in the available context, say so
- Do not make up information or speculate about undocumented features
- Reference specific files or documentation pages when possible
- Format responses in Markdown
```

## Gemini CLI Invocation

The Gemini CLI supports non-interactive (headless) use via the `-p` or `--prompt` flag. For a simple one-shot Q&A use case, the invocation would look like:

```bash
GEMINI_SYSTEM_MD=/etc/docs-agent/system-prompt.md \
gemini --prompt "<user_query>" \
       --sandbox_dir /workspace/scion
```

**Key considerations:**
- The `--prompt` flag provides the user query. The `GEMINI_SYSTEM_MD` environment variable points to our custom markdown file, completely replacing the default agent system prompt.
- The `--sandbox_dir` flag (if available) or working directory should point to the Scion checkout so Gemini can reference files.
- The process runs to completion and stdout is captured.
- A configurable timeout (e.g., 60 seconds) kills the process if it hangs.

### Alternative: Gemini API Direct

Instead of shelling out to the Gemini CLI, the handler could call the Gemini API directly using the Go SDK. This would:
- Eliminate the npm/Node.js dependency
- Allow more control over model parameters (temperature, max tokens)
- Support streaming responses
- Be more suitable for a production service

**Trade-off:** The CLI approach is faster to prototype and automatically gets file-reading tool use; the API approach is more robust and efficient for production.

## Go Handler

```go
// Minimal handler sketch
func main() {
    http.HandleFunc("/ask", handleAsk)
    http.HandleFunc("/health", handleHealth)
    log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleAsk(w http.ResponseWriter, r *http.Request) {
    // 1. Parse query from JSON body
    // 2. Validate length (e.g., max 1000 chars)
    // 3. Build gemini CLI command
    // 4. Execute with timeout context
    // 5. Strip ANSI codes from output
    // 6. Return JSON response
}
```

## Deployment

### Cloud Run Configuration

Follow the same pattern as `docs-site/deploy.sh` and `docs-site/cloudbuild.yaml`:

- **Project:** `duet01` (or configurable)
- **Region:** `us-west1`
- **Service name:** `scion-docs-agent`
- **Image registry:** `${REGION}-docker.pkg.dev/${PROJECT_ID}/scion-images/docs-agent`
- **Concurrency:** 1 (each request spawns a Gemini CLI process)
- **Memory:** 1Gi (Gemini CLI + Node.js runtime)
- **CPU:** 1-2 vCPUs
- **Timeout:** 120s (request timeout)
- **Min instances:** 0 (scale to zero when idle)
- **Max instances:** 5 (cost control)

### Authentication

The Gemini CLI needs a `GEMINI_API_KEY` (or equivalent). This should be:
- Stored in Google Secret Manager
- Mounted as an environment variable on the Cloud Run service
- **Not** baked into the container image

### Rebuild Trigger

A Cloud Build trigger on the main branch of the Scion repo would rebuild and redeploy the docs-agent image, ensuring the bundled source stays current.

## Project Structure

```
docs-agent/
├── main.go              # HTTP handler
├── go.mod
├── go.sum
├── system-prompt.md     # Gemini system prompt
├── Dockerfile
├── cloudbuild.yaml      # Cloud Build config
├── deploy.sh            # Deploy script
└── README.md
```

This could live as a top-level directory in the Scion repo (like `docs-site/`) or as a separate repository.

## Open Questions

### 1. CLI vs API - Which Gemini integration approach?

**Option A: Gemini CLI (`gemini --prompt`)**
- Pros: Quick to build, CLI has built-in file reading/tool use, familiar pattern from Scion harness
- Cons: Requires Node.js in the image, process overhead per request, harder to control model parameters, less suitable for concurrent requests, cold-start overhead from npm

**Option B: Gemini API (Go SDK direct)**
- Pros: No Node.js dependency, better control over model/params, supports streaming, lower per-request overhead, simpler container image
- Cons: Need to manually handle context/file reading, more code to write, no built-in tool use

**Recommendation:** Start with the CLI for a quick prototype, plan to migrate to the API for production.

### 2. Context strategy - How does Gemini access the source?

- **Full repo in working directory:** Simple, but the Gemini CLI may not automatically read relevant files without tool use
- **Pre-indexed/concatenated context:** Build a single context file at image build time containing all docs + key source files, pass it in the prompt
- **RAG/embedding approach:** Overkill for an MVP, but could improve accuracy on large codebases

### 3. Where should the project live?

- **Option A: `docs-agent/` in the Scion repo** - Same pattern as `docs-site/`, easier to keep in sync, single CI pipeline
- **Option B: Separate repository** - Cleaner separation, independent release cycle
- **Recommendation:** Start in the Scion repo for simplicity.

### 4. Concurrency model on Cloud Run

The Gemini CLI is a heavyweight process. Options:
- **Concurrency=1:** Each instance handles one request at a time. Simple, but more instances needed under load.
- **Concurrency=N with request queuing:** Handler queues requests and processes them serially. Better utilization but adds complexity.
- If using the API approach, concurrency becomes much less of a concern.

### 5. Rate limiting and abuse prevention

- Should the endpoint be public or require an API key?
- If public, what rate limiting strategy? (Cloud Run has no built-in rate limiting; would need Cloud Armor, API Gateway, or application-level limits)
- Cost control: each request consumes Gemini API credits

### 6. Response format and frontend

- Is this API-only, or should there be a simple web UI (chat-like interface)?
- If a web UI, should it be bundled with the existing docs-site (Astro/Starlight) or standalone?
- Should responses support streaming (SSE/WebSocket) for better UX?

### 7. Model selection - Gemini 3.1 Flash-Lite availability

- Is `gemini-3.1-flash-lite` available in the Gemini CLI, or does it need to be configured via `settings.json` or environment variable?
- Fallback model if Flash-Lite is unavailable or rate-limited?

### 8. Cold start latency

- Cloud Run scale-to-zero means first requests will have cold start latency
- The Gemini CLI itself has startup overhead (Node.js + npm)
- Consider keeping min-instances=1 if latency matters, at the cost of always-on billing

### 9. Content freshness

- Image rebuild is the mechanism for updating bundled source content
- How often should rebuilds happen? (On every commit to main? Nightly? Manual?)
- Could a webhook from GitHub trigger Cloud Build on push to main
