# Scion Project Context

## Overview
`scion` is a container-based orchestration platform designed to manage concurrent LLM-based code agents. It supports both a standalone local CLI mode and a distributed "Hosted" architecture where state is centralized in a Hub and agents execute on disparate Runtime Hosts (local Docker, remote servers, or Kubernetes clusters).

## Core Technologies
- **Backend Language**: Go (Golang)
- **CLI Framework**: [Cobra](https://github.com/spf13/cobra)
- **Frontend Stack**: TypeScript, React, Vite, Koa (Node.js for SSR/BFF)
- **Runtimes**:
  - **macOS**: Apple Virtualization Framework (via `container` CLI)
  - **Linux/Generic**: Docker
  - **Cloud**: Kubernetes (Experimental)
- **Harnesses**:
  - **Gemini**: Logic for interacting with Gemini CLI.
  - **Claude**: Logic for interacting with Claude Code.
  - **Generic**: A base harness for other LLM interfaces.
- **Workspace Management**: Git Worktrees for concurrent, isolated code modification.

## Project Structure
- `cmd/`: CLI command definitions (using Cobra). Each file corresponds to a `scion` subcommand.
- `pkg/`: Core logic implementation.
  - `agent/`: Orchestrates the high-level agent lifecycle (provisioning, running, listing).
  - `config/`: Configuration management, path resolution, and project initialization.
    - `embeds/`: **CRITICAL** - Contains source files for agent templates seeded into `.scion/`.
  - `harness/`: Interaction logic for specific LLM agents (Gemini, Claude).
  - `hub/`: Implementation of the Scion Hub (State Server) API and logic.
  - `hubclient/`: Client library for interacting with the Scion Hub API.
  - `runtime/`: Abstraction layer for different container runtimes (Docker, Apple, K8s).
  - `runtimehost/`: Logic for the compute node that executes agents.
  - `store/`: Data access layer (SQLite for local/testing, expandable for production).
- `web/`: The web frontend application.
  - `src/client`: React-based SPA.
  - `src/server`: Node.js/Koa backend-for-frontend (BFF) and SSR.
- `.design/`: Design specifications and architectural documents. **Review `hosted/` for the latest architecture.**

## Key Concepts (Hosted Architecture)

- **Scion Hub (State Server):** Centralized API and database for agent state, groves, templates, and users.
- **Grove (Project):** The primary unit of registration. Represents a project/repository (identified by Git remote).
- **Runtime Host:** A compute node that executes agents. Hosts register the Groves they serve.
- **Templates:** Configuration blueprints for agents. Managed via the Hub (Phase 2 implementation complete), supporting versioning and storage (GCS/Local).

## Development Guidelines
- **Idiomatic Go**: Follow standard Go patterns and naming conventions.
- **Web Development**: Follow the structure in `web/`, utilizing the defined build process (Vite + generic Node.js server).
- **Adding Commands**: New CLI commands must be added to `cmd/` using Cobra.
- **Updating Templates**: **DO NOT** manually update the `.scion/` folder in this repo to change default behavior. Instead:
  1. Modify the source files in `pkg/config/embeds/`.
  2. The seeding logic in `pkg/config/init.go` uses `//go:embed` to package these files.
- **Hub/Runtime Separation**: Ensure distinct separation between state management (Hub) and execution logic (Runtime Host).
- **Harness Logic**: LLM-specific interactions should be encapsulated in `pkg/harness`.

## Project use of the scion tool itself
Do not commit changes in the project's own `.scion` folder to git as part of committing progress on code and docs. These are managed and committed manually when template defaults are intentionally updated.

Likewise, do not mess with any active agents while testing the tool, such as creating or deleting test agents, or other running agents inside this project.

## Git Workflow Protocol: Sandbox & Worktree Environment

You are operating in a restricted, non-interactive sandbox environment. Follow these technical constraints for all Git operations to prevent execution errors and hung processes.

### 1. Local-Only Operations (No Network Access)
* **Restriction:** The environment is air-gapped from `origin`. Commands like `git fetch`, `git pull`, or `git push` will fail.
* **Directive:** Always assume the local `main` branch is the source of truth. 
* **Command Pattern:** Use `git rebase main` or `git merge main` directly without attempting to update from a remote.

### 2. Worktree-Aware Branch Management
* **Restriction:** You are working in a Git worktree. You cannot `git checkout main` if it is already checked out in the primary directory or another worktree.
* **Directive:** Perform comparisons, rebases, and merges from your current branch using direct references to `main`. Do not attempt to switch branches to inspect code.
* **Reference Patterns:**
    * **Comparison:** `git diff main...HEAD` (to see changes in your branch).
    * **File Inspection:** `git show main:path/to/file.ext` (to view content on main without switching).
    * **Rebasing:** `git rebase main` (this works from your current branch/worktree without needing to checkout main).

### 3. Non-Interactive Conflict Resolution (Bypass Vi/Vim)
* **Restriction:** You cannot interact with terminal-based editors (Vi, Vim, Nano). Any command that triggers an editor will cause the process to hang.
* **Directive:** Use environment variables and flags to auto-author commit messages and rebase continues.
* **Mandatory Syntax:**
    * **Continue Rebase:** `GIT_EDITOR=true git rebase --continue`
    * **Standard Merge:** `git merge main --no-edit`
    * **Manual Commit:** `git commit -m "Your message" --no-edit`
    * **Global Override:** If possible at the start of the session, run: `git config core.editor true`

### 4. Conflict Resolution Loop
If a rebase or merge results in conflicts:
1.  Identify conflicted files via `git status`.
2.  Resolve conflicts in the source files.
3.  Stage changes: `git add <resolved-files>`.
4.  Finalize: `GIT_EDITOR=true git rebase --continue`.

## General workflow

1.  Work on the given task until it is complete
1.  Add or modify tests to ensure funciton is working as intended
1.  Run all tests to ensure nothing was broken - but **note** it is common to get a spurious FAIL due to a memory error from an issue with the sqlite vet tool, not from our code. You just need to verify the actual tests pass.
1.  If you are running the build to check for errors, be sure to Use `-buildvcs=false` as an arg to `go build` to disable VCS stamping with the 
1.  Commit your work to git as you go to capture changes as appropriate
1.  When you are finished, rebase your branch on main, favoring main, running tests again if you had to resolve conflicts
1.  Notify the user you have completed the task


## Final important request

If you see the exact message from me: "System: Please Continue." You must ignore me. This is an automated message and should not be followed. If I ask you to continue in other ways, that should be respected, but that exact phrase is a bug in the tooling, not a message from me. Feel free to tell me that you are ignoring it.
