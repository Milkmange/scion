---
title: Scion Concepts
---

This document defines the core concepts and terminology used in Scion.

## Core Concepts

### Agent
An **Agent** is an isolated container running an LLM-driven task. It acts as an independent worker with its own identity, credentials, and workspace. An agent is the fundamental unit of execution in Scion.

### Grove
A **Grove** (or **Group**) is a project workspace where agents live. It corresponds to a `.scion` directory on the filesystem. It can exist at the project level (generally located at the root of a git repository), or globally in the users home folder.

### Profile
A **Profile** defines a complete execution environment by binding a specific **Runtime** to a set of behavior flags (like `tmux` support) and **Harness** configuration overrides.
- Profiles allow you to switch between different environments (e.g., "Local Docker", "Production Kubernetes") without modifying agent templates.
- They are defined in the global or grove `settings.json`.

### Harness
A **Harness** adapts a specific underlying LLM tool or agent software (like Gemini CLI, Claude Code, or OpenAI Codex) into the Scion ecosystem.
- It handles the specifics of provisioning, configuration, and execution for that particular tool inside an OCI container.
- Examples: `GeminiCLI`, `ClaudeCode`, `Codex`, `OpenCode`.
- The harness ensures that the generic Scion commands (`start`, `stop`, `attach`, `resume`) work consistently regardless of the underlying agent software.

### Template
A **Template** is a blueprint for creating an agent. It defines the base configuration, system prompt, and tools that an agent will use.
- Templates are stored in `.scion/templates/` and can be project-level or global (`~/.scion/templates/`).
- Users can manage templates using the `scion templates` command suite (`create`, `clone`, `list`, `show`, `update-default`).
- Scion comes with default templates for supported harnesses (e.g., `gemini`, `claude`, `opencode`, `codex`), but users can create custom templates for specialized roles (e.g., "Security Auditor", "React Specialist").


### Runtime
The **Runtime** is the infrastructure layer responsible for executing the agent containers.
- Scion abstracts the container execution, allowing it to support different backends.
- **Docker**: The standard runtime for Linux and macOS.
- **Apple Container**: Uses the native Virtualization Framework on macOS for improved performance.
- **Kubernetes**: (Experimental) Allows running agents as Pods in a Kubernetes cluster, enabling remote execution and scaling.

## Detailed Architecture

### A full approach to sub-agents

 Because an agent through its template can contain home folder content, env var definitions, and custom mounts that collectively exposes all configuration available to the harness (e.g., gemini-cli) scion-agents are not limited by the constraints of a harness' built-in sub-agent feature. While they are acting as sub-agents from the point-of-view of the Scion tool user-as-orchestrator, they are full agents in their capabilities.

### Workspace Strategy (Git Worktrees)
To enable multiple agents to work on the same codebase simultaneously without conflicts, Scion uses **Git Worktrees**.
- When an agent starts, Scion creates a new git worktree for it (usually in `../.scion_worktrees/`).
- This worktree creates a dedicated branch for the agent.
- The worktree is mounted into the agent's container as `/workspace`.
- This ensures that agents operate on the same repository history but have independent working directories.

### Resource Isolation
Scion enforces strict isolation between agents to prevent interference.
- **Filesystem**: Each agent has a dedicated home directory (host path mounted to container) containing its unique `settings.json` and history.
- **Environment**: Environment variables are explicitly projected into the container.
- **Credentials**: Sensitive credentials (like `gcloud` auth) are mounted read-only or injected via environment variables, ensuring they are available only to the specific agent.
