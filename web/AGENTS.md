# Scion Web Frontend - Agent Instructions

This document provides instructions for AI agents working on the Scion Web Frontend.

## Design Documents

Before making changes, review the relevant design documentation:

- **[Web Frontend Design](../.design/hosted/web-frontend-design.md)** - Architecture, technology stack, component patterns

## Architecture Overview

The web frontend is a **client-side SPA** built with Lit web components. There is no Node.js server at runtime. The Go `scion` binary serves the compiled client assets and handles all server-side concerns (OAuth, sessions, SSE real-time events, API routing) via `pkg/hub/web.go` and `pkg/hub/events.go`.

Node.js and npm are used **only at build time** to compile and bundle client assets via Vite.

## Development Workflow

### Building and Running

```bash
cd web
npm install    # First time only, or after package.json changes

# Build client assets
npm run build

# Run the Go server with dev auth (from repository root)
scion server start --enable-hub --enable-web --dev-auth \
  --web-assets-dir ./web/dist/client
```

Dev auth bypasses OAuth and auto-creates a session with admin privileges. The `--web-assets-dir` flag loads assets from disk so you can rebuild and refresh without restarting the server.

### Using Vite Dev Server

For client-side development with hot module reload:

```bash
npm run dev
```

Note: The Vite dev server only serves client assets. API calls and SSE require the Go server to be running.

### Common Commands

| Command | Purpose |
|---------|---------|
| `npm run dev` | Start Vite dev server with hot reload |
| `npm run build` | Build client assets for production |
| `npm run build:dev` | Build client assets in development mode |
| `npm run lint` | Check for linting errors |
| `npm run lint:fix` | Auto-fix linting errors |
| `npm run format` | Format code with Prettier |
| `npm run typecheck` | Run TypeScript type checking |

### Verifying Changes

After making changes, verify:

1. **Type checking passes:** `npm run typecheck`
2. **Linting passes:** `npm run lint`
3. **Client builds:** `npm run build`

## Project Structure

```
web/
├── src/
│   ├── client/              # Browser-side code
│   │   ├── main.ts          # Client entry point, routing setup
│   │   ├── state.ts         # State manager with SSE subscriptions
│   │   └── sse-client.ts    # SSE client for real-time updates
│   ├── components/          # Lit web components
│   │   ├── index.ts         # Component exports
│   │   ├── app-shell.ts     # Main application shell (sidebar, header, content)
│   │   ├── shared/          # Reusable UI components
│   │   │   ├── index.ts         # Shared component exports
│   │   │   ├── nav.ts           # Sidebar navigation
│   │   │   ├── header.ts       # Top header bar with user menu
│   │   │   ├── breadcrumb.ts   # Breadcrumb navigation
│   │   │   ├── debug-panel.ts  # Debug panel component
│   │   │   └── status-badge.ts # Status indicator badges
│   │   └── pages/           # Page components
│   │       ├── home.ts          # Dashboard page
│   │       ├── login.ts         # OAuth login page
│   │       ├── agents.ts       # Agents list page
│   │       ├── agent-detail.ts # Agent details page
│   │       ├── groves.ts       # Groves list page
│   │       ├── grove-detail.ts # Grove details page
│   │       ├── terminal.ts     # Terminal/session page (xterm.js)
│   │       ├── unauthorized.ts # 401/403 page
│   │       └── not-found.ts    # 404 page
│   ├── styles/              # CSS theme and utilities
│   │   ├── theme.css        # CSS custom properties, light/dark mode
│   │   └── utilities.css    # Utility classes
│   └── shared/              # Shared types between components
│       └── types.ts         # Type definitions (User, Grove, Agent, etc.)
├── public/                  # Static assets
│   └── assets/              # Built client assets (CSS, JS)
├── dist/                    # Build output (gitignored)
├── vite.config.ts           # Vite build configuration
├── tsconfig.json            # TypeScript configuration
└── package.json
```

## Technology Stack

- **Components:** Lit 3.x with TypeScript decorators
- **UI Library:** Shoelace 2.x
- **Build:** Vite for client-side bundling
- **Routing:** Client-side via History API (click interception in `main.ts`)
- **Terminal:** xterm.js for terminal sessions
- **Server:** Go (`scion` binary with `--enable-web`)

## Key Patterns

### Creating Lit Components

Components use standard Lit patterns with TypeScript decorators:

```typescript
import { LitElement, html, css } from 'lit';
import { customElement, property } from 'lit/decorators.js';

@customElement('my-component')
export class MyComponent extends LitElement {
  @property({ type: String })
  myProp = 'default';

  static override styles = css`
    :host { display: block; }
  `;

  override render() {
    return html`<div>${this.myProp}</div>`;
  }
}
```

### Using Shoelace Components

```typescript
render() {
  return html`
    <sl-button variant="primary" @click=${() => this.handleClick()}>
      <sl-icon slot="prefix" name="plus-lg"></sl-icon>
      Create Agent
    </sl-button>

    <sl-badge variant="success">Running</sl-badge>
  `;
}
```

### Theme Variables

Use CSS custom properties with the `--scion-` prefix for consistent theming:

```css
:host {
  background: var(--scion-surface);
  color: var(--scion-text);
  border: 1px solid var(--scion-border);
  border-radius: var(--scion-radius);
}
```

### Dark Mode

Dark mode is handled automatically via CSS custom properties. The theme toggle in the navigation saves the preference to localStorage. Components should use the semantic color variables (e.g., `--scion-surface`, `--scion-text`) which automatically adjust for dark mode.

## Containerized / Sandboxed Environments

When working in a containerized or sandboxed agent environment (e.g., scion agents), keep these points in mind:


- **Vite dev server is available.** You can run `npm run dev` to start the Vite dev server for client-side development and visual inspection. API calls and SSE will not work without the Go backend.
- **Use `--dev-auth` for local testing.** When a Go server is available, `--dev-auth` bypasses OAuth and auto-creates a dev session, which is the simplest way to test the frontend end-to-end. See the README for details.
- **go server** the golang server can be started as a background process, but oauth flows can not be used in a container
