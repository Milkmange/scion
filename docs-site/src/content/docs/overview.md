---
title: Scion Overview
---

Scion is a container-based orchestration tool designed to manage concurrent LLM-based code agents across your local machine and remote clusters. It enables developers to run specialized sub-agents with isolated identities, credentials, and workspaces, allowing for parallel execution of tasks such as coding, auditing, and testing.

## Key Features

- **Parallelism**: Run multiple agents concurrently as independent processes either locally or remote.
- **Isolation**: Each agent runs in its own container with strict separation of credentials, configuration, and environment.
- **Context Management**: Scion automatically manages `git worktrees` to provide each agent with a dedicated workspace, preventing merge conflicts and ensuring clean separation during concurrent parallel operation. See the [Worktree Guide](guides/workspace.md).
- **Specialization**: Agents can be customized via templates (e.g., "Security Auditor", "QA Tester") to perform specific roles.
- **Interactivity**: Agents support "detached" background operation, but users can "attach" to any running agent for human-in-the-loop interaction.
- **Local and Remote Runtimes**: Supports both local and remote runtime contexts, with the ability to pause and resume agents, including teleporting an agent by, for example, pausing a remote agent, and resuming it locally.

## Configuration

Scion uses a flexible configuration system based on **Profiles**, **Runtimes**, and **Harnesses**. This allows you to define different environments (e.g., local Docker vs. remote Kubernetes) and switch between them easily.

- **Global Settings**: `~/.scion/settings.json`
- **Grove Settings**: `.scion/settings.json` (Project overrides)

For detailed information on configuring Scion, see the [Settings Reference](settings.md) and [Agent Configuration Reference](reference/scion-config-reference.md).
To learn about the different agent tools supported by Scion, see [Supported Harnesses](supported-harnesses.md).

## Getting Started

Scion is designed to be easy to start with.

1.  **Install**: Follow the [Installation Guide](install.md) to get Scion on your machine.
2.  **Initialize**: Run `scion init` in your project root to create a `.scion` directory.
3.  **Start an Agent**: Use `scion start <agent-name> "<task>"` to launch an agent.
4.  **Interact**: Use `scion attach <agent-name>` to interact with the agent's session, or `scion logs <agent-name>` to view its output.
5.  **Resume**: Use `scion resume <agent-name>` to restart a stopped agent, preserving its state.

## Architecture

Scion follows a Manager-Worker architecture:

- **scion**: A host-side CLI that orchestrates the lifecycle of agents. It manages the "Grove" (the project workspace) and provides tools for template management (`scion templates`).
- **Agents**: Isolated runtime containers (e.g., Docker) running the agent software (like Gemini CLI, Claude Code, or OpenAI Codex).
