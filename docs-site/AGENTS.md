# Scion Documentation Meta-Guide

This guide outlines the organizational philosophy, architecture, and curation standards for the Scion documentation project. It serves as a blueprint for both human and AI contributors to ensure a consistent and navigable documentation experience.

## 1. Documentation Dimensions

Scion documentation must navigate three primary dimensions:

| Dimension | Variants |
| :--- | :--- |
| **User Persona** | End User (Developer), Administrator (DevOps/Ops), Contributor (Core Developer) |
| **Operational Mode** | Solo (Local-only, zero-config), Hosted (Distributed, Hub-centric) |
| **Interface** | CLI (Primary), Web Dashboard (Visualization & Control) |

Note no one variant within a dimension has supremecy over another.

## 2. Proposed Content Architecture

The documentation is organized into five functional pillars. The "Intersection" of dimensions is handled by grouping guides by **Persona** and then branching by **Mode**.

### I. Foundations (Everyone)
*   **Overview**: High-level value proposition.
*   **Core Concepts**: Universal primitives (Grove, Harness, Profile, Runtime, Agent).
*   **Glossary**: Standardized terminology.

### II. Developer's Guide (End Users)
*   **Solo Workflow (The "Local" Path)**:
    *   Installation & Quick Start.
    *   Managing local agents and workspaces.
    *   Customizing templates.
*   **Hosted Workflow (The "Team" Path)**:
    *   Connecting to a Scion Hub.
    *   Using the Web Dashboard.
    *   Managing environment variables and secrets via the Hub.
*   **Core Skills (Shared)**:
    *   Advanced Template management.
    *   Interactive sessions (Attach & Message).
    *   Observability (Logs & Monitoring).

### III. Administrator's Guide (Ops/Admins)
*   **Local Governance**: Detailed `settings.json` configuration and environment variable substitution.
*   **Infrastructure Deployment**:
    *   Setting up the Scion Hub (Persistence, NATS, Web Server).
    *   Provisioning Runtime Hosts (Docker, Apple Virtualization).
    *   Kubernetes Integration (Pod management, SCM strategies).
*   **Security & Identity**:
    *   Authentication flows (OAuth vs. Dev Auth).
    *   Permissions & Policy design.
*   **Operations**: Metrics collection and OpenTelemetry forwarding.

### IV. Contributor's Guide (Developers)
*   **Architecture Deep Dive**: Internal component interactions.
*   **Harness Development**: How to add support for new LLM tools.
*   **Runtime Development**: Implementing new execution backends.
*   **Design Catalog**: Historical and future design specifications (mirrored from `.design/`).

### V. Technical Reference (Everyone)
*   **CLI Reference**: Generated command documentation.
*   **API Reference**: Hub and Runtime Host REST/WebSocket specifications.
*   **Configuration Schemas**: `settings.json` and `scion-agent.json` field definitions.

## 3. Handling the Intersections

### Mode Intersection (Solo vs. Hosted)
*   **The Default is Solo**: All "Getting Started" content assumes Solo mode to minimize friction.
*   **The "Upgrade" Pattern**: Hosted mode features are presented as enhancements. For example, the "Secret Management" guide should start with "Local Env Vars" and then provide a "Using the Hub for Secrets" section.

### Interface Intersection (CLI vs. Web)
*   **Action-Oriented Documentation**: For any user task (e.g., "Starting an Agent"), the documentation should provide instructions for both interfaces using a tabbed or side-by-side format:
    *   **CLI**: `scion start ...`
    *   **Web**: Navigation path in the UI.

## 4. Starlight Sidebar Configuration

The `astro.config.mjs` sidebar should be updated to reflect this hierarchy:

```javascript
sidebar: [
  { label: 'Foundations', items: ['overview', 'concepts', 'supported-harnesses'] },
  {
    label: 'Developer Guide',
    items: [
      { label: 'Local Workflow', items: ['install', 'guides/workspace'] },
      { label: 'Team Workflow', items: ['guides/hosted-user'] }, // To be created
      { label: 'Skills', items: ['guides/templates', 'guides/tmux'] },
    ],
  },
  {
    label: 'Operations & Hosting',
    items: [
      { label: 'Hub Setup', items: ['guides/hub-server'] }, // To be created
      { label: 'Runtimes', items: ['guides/kubernetes'] },
      { label: 'Security', items: ['guides/auth'] }, // To be created
    ],
  },
  { label: 'Reference', autogenerate: { directory: 'reference' } },
  { label: 'Contributing', autogenerate: { directory: 'contributing' } },
]
```

## 5. Curation Standards

1.  **Code-First Truth**: Documentation should be updated in the same PR as the feature implementation.
2.  **Persona-Specific Tone**:
    *   *User docs* should be practical and task-oriented.
    *   *Admin docs* should be technical and detail-oriented regarding side effects.
    *   *Reference docs* should be exhaustive and dry.
3.  **Cross-Linking**: Always link from high-level guides to specific references (e.g., "Set the model in your config [see Reference]") to avoid content duplication.
