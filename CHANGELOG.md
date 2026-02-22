# Release Notes (Feb 21, 2026)

This period heavily focused on implementing the end-to-end "env-gather" flow to manage environment variables safely, alongside several CLI improvements and runtime fixes.

## 🚀 Features
* **Env-Gather Flow Pipeline:** Implemented a comprehensive environment variable gathering system across the CLI, Hub, and Broker. This includes harness-aware env key extraction, Hub 202 handling with submission endpoints, and broker-side evaluation to finalize the environment prior to agent creation.
* **Agent Context Threading:** Threaded the CLI hub endpoint directly to agent containers and added support for environment variable overrides.
* **Agent Dashboard Enhancements:** The agent details page now displays the `lastSeen` heartbeat as a relative time format.
* **Template Pathing:** Added support for `SCION_EXTRA_PATH` to optionally include template bin directories in the system `PATH`.
* **Build System Upgrades:** Overhauled the Makefile with new standard targets for build, install, test, lint, and web compilation.

## 🐛 Fixes
* **Env-Gather Safety & UX:** Added strict rejection of env-gather in non-interactive modes to prevent unsanctioned variable forwarding. Improved confirmation messaging and added dispatch support for grove-scoped agent creation.
* **CLI Output Formatting:** Redirected informational CLI output to `stderr` to ensure `stdout` can be piped cleanly as JSON.
* **Podman Performance:** Fixed slow container provisioning on Podman by directly editing `/etc/passwd` instead of using `usermod`.
* **Profile Parameter Routing:** Corrected the threading of the profile parameter from the CLI through the Hub to the runtime broker.
* **Hub API Accuracy:** The Hub API now correctly surfaces the `harness` type in responses for agent listings.
* **Docker Build Context:** Fixed an issue where the `scion-base` Docker image build was missing the web package context.

# Release Notes (Feb 20, 2026)

This period focused heavily on unifying the Hub API and Web Server architectures, refactoring the agent status model, and enhancing the web frontend experience with new routing and pages.

## ⚠️ BREAKING CHANGES
* **Status Model:** Consolidated the `SessionStatus` field into the primary `Status` field across the codebase (API, Database, UI). The `WAITING_FOR_INPUT` and `COMPLETED` states are now treated as "sticky" statuses.
* **Server Architecture:** Combined the Hub API and Web server to serve on a single port (`8080`) when both are enabled. API traffic is now routed to `/api/v1/`, resolving CORS issues and simplifying local deployment.

## 🚀 Features
* **Web Frontend Enhancements:** Added a new Brokers list page, implemented full client-side routing for the Vite dev server, and unified OAuth provider detection via a new `/auth/providers` endpoint.
* **Agent Environment:** Added support for injecting harness-specific telemetry and hub environment variables directly into agent containers based on grove settings.
* **Git Operations:** Added cloning status indicators and improved git clone config parity during grove-scoped agent creation.

## 🐛 Fixes
* **Real-time UI Updates:** Fixed the Server-Sent Events (SSE) format to ensure real-time UI updates correctly broadcast agent state changes.
* **Routing & Port Prioritization:** Fixed port prioritization to use the web port for broker hub endpoints in combined mode, and ensured unhandled `/api/` routes return proper JSON 404 responses.
* **OAuth & Login:** Fixed conditional rendering for the `/login` route and correctly populated OAuth provider attributes during client-side navigation.
* **Container Configuration:** Fixed container image resolution from on-disk harness configurations and normalized YAML key parsing.
* **Status Reporting:** Ensured Hub status reporting correctly respects and preserves the newly unified, sticky statuses.

# Release Notes (Feb 19, 2026)

This period represented a major architectural shift, consolidating the web server into a single Go binary, removing dependencies like NATS and Koa, and introducing hub-first remote workspaces via Git.

## ⚠️ BREAKING CHANGES
* **Secrets Management:** The system now strictly requires a configured production secret backend (e.g., `gcpsm`) for any secret Set operations across user, grove, and runtime broker scopes. Plaintext fallbacks have been removed. Read, list, and delete operations remain functional locally to support data migration.
* **Server Architecture:** The Node.js Koa server and NATS message broker dependencies have been completely retired. The Scion Hub now natively handles web frontend serving, SPA routing, and Server-Sent Events (SSE) via a consolidated Go binary.

## 🚀 Features
* **Hub-First Git Workspaces:** Implemented end-to-end support for creating remote workspaces directly from Git URLs. This integration enables git clone mode across `sciontool init` and the runtime broker pipeline.
* **Web Server & Auth Integration:** Introduced native session management and OAuth routing within the Go web server, alongside a new EventPublisher for real-time SSE streaming.
* **Telemetry & Settings:** Added telemetry injection to the `v1` settings schema. Telemetry configuration now supports hierarchical merging and is automatically bridged into the agent container's environment variables.
* **CLI Additions:** Introduced the `scion look` command for non-interactive terminal viewing. Project initialization now automatically sets up template directories and requires a global grove.

## 🐛 Fixes
* **Lifecycle Hooks:** Relocated the cleanup handler to container lifecycle hooks to guarantee reliable execution upon container termination.
* **Settings Overrides:** Fixed configuration parsing to ensure environment variable overrides are correctly applied when loaded from `settings.yaml`.
* **CLI Defaults:** Ensured the `update-default` command consistently targets the global grove, and introduced a new `--force` flag.
* **Frontend Assets:** Resolved static asset serving issues by removing an erroneous `StripPrefix` in the router, and fixed client entry point imports.
