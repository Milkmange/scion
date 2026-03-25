# Git-Workspace Hybrid Groves

**Created:** 2026-03-25
**Status:** Draft / Proposal
**Related:** `hosted/git-groves.md`, `hosted/sync-design.md`, `hosted/git-ws.md`, `grove-dirs.md`

---

## 1. Overview

### 1.1 Problem Statement

Today, Hub-created groves come in two flavors:

| Type | Workspace Strategy | Agent Isolation |
|------|-------------------|-----------------|
| **Git-based** (`gitRemote` set) | Each agent clones the repo independently into its container. Workspace is ephemeral — lost on container deletion. | Full isolation: each agent has its own `.git`, branch, and working tree. |
| **Hub-native** (no `gitRemote`) | A single shared workspace at `~/.scion/groves/<slug>/` is mounted into all agents. | No isolation: agents share the same files. Concurrent writes can conflict. |

Both models have significant limitations for a common use case: **teams that want a git-backed project but prefer a shared, persistent workspace** on the Hub rather than ephemeral per-agent clones.

**Pain points with the current git-based model:**

1. **Every agent clones independently** — wasteful for large repositories, especially with multiple agents on the same grove.
2. **Workspace is ephemeral** — if a container is deleted (not just stopped), all uncommitted work is lost. Users must remember to push.
3. **No shared state** — agents cannot see each other's file changes unless they push to the remote and pull.
4. **Clone latency** — each agent start incurs clone time, especially for large repos.

**Pain points with the current hub-native model:**

1. **No git integration** — no ability to commit, push, create branches, or open PRs.
2. **No source of truth** — the workspace is a bare directory with no version history.
3. **No reproducibility** — if the workspace is lost, there's no way to restore it from a remote.

### 1.2 Proposal

Introduce a **git-workspace hybrid** grove type: a grove that is associated with a git URI (like a git-based grove) but provisions a **single shared git clone** into the hub-native workspace path. Agents mount this shared workspace, just as hub-native groves work today.

This combines the benefits of both models:
- Git integration (branches, commits, push/pull, PRs)
- Shared persistent workspace (survives agent deletion)
- Single clone (no per-agent duplication)
- Hub-managed lifecycle

### 1.3 Goals

1. Allow creating a grove from a git URL that provisions a shared, persistent workspace.
2. The workspace is a real git clone — agents can commit, branch, push, and pull.
3. Only one clone operation per grove (not per agent).
4. Agents share the workspace filesystem, similar to hub-native groves.
5. Support private repositories via `GITHUB_TOKEN` or GitHub App credentials.

### 1.4 Non-Goals

- Per-agent branch isolation within the shared workspace (agents must coordinate).
- Automatic merge/conflict resolution between agents.
- Multi-broker workspace replication (deferred — one broker at a time initially).
- SSH key-based git auth (HTTPS + token only in this phase).
- Submodule support.

---

## 2. Design Alternatives Considered

### 2.1 Alternative A: Shared Bare Clone + Per-Agent Worktrees

**Concept:** Clone the repo once as a bare repository on the broker. Each agent gets a git worktree from the shared bare clone, providing per-agent isolation with shared object storage.

```
~/.scion/groves/<slug>/
├── repo.git/           # Shared bare clone (object store)
├── worktrees/
│   ├── agent-alpha/    # git worktree for agent-alpha
│   └── agent-beta/     # git worktree for agent-beta
```

**Pros:**
- Per-agent branch isolation (each agent works on its own branch).
- Disk-efficient: objects are shared, only working trees are duplicated.
- Familiar pattern — matches current local worktree behavior.

**Cons:**
- Agents still can't see each other's uncommitted changes (separate working trees).
- Worktree creation adds startup latency (though less than a full clone).
- More complex lifecycle management (worktree cleanup, branch tracking).
- Doesn't match the hub-native "shared workspace" mental model that users expect.

**Verdict:** This is a good approach for isolation-first use cases but doesn't fulfill the "shared workspace" goal. It's essentially an optimization of the current git-based model, not a new hybrid. Could be offered as a future option.

### 2.2 Alternative B: Clone Once, Mount Shared (Proposed)

**Concept:** Clone the repo once into the hub-native workspace path. All agents mount the same directory. The workspace is a normal git working tree (not bare).

```
~/.scion/groves/<slug>/
├── .git/               # Full git clone
├── src/
├── README.md
└── ...
```

**Pros:**
- Simple mental model: one workspace, multiple agents.
- Agents can see each other's file changes in real-time.
- Full git operations available (commit, push, pull, branch).
- Minimal lifecycle complexity — it's just a directory.
- Matches hub-native behavior exactly (just with git pre-initialized).

**Cons:**
- No isolation: concurrent agent writes can conflict.
- Single branch at a time (or agents must coordinate git operations).
- Potential for `.git` lock contention with concurrent git operations.

**Verdict:** This is the simplest approach and matches the user's stated desire for a "shared workspace with git". The lack of isolation is a known trade-off that users accept when choosing hub-native groves today.

### 2.3 Alternative C: Git Clone + GCS Sync Hybrid

**Concept:** Clone the repo once into GCS storage. Sync to broker workspace via the existing GCS signed-URL mechanism. Agents mount the synced copy.

**Pros:**
- Multi-broker support (any broker can sync from GCS).
- Leverages existing sync infrastructure.

**Cons:**
- No live git operations inside the container (synced copy has no `.git` context unless we sync that too).
- Requires additional sync steps on agent start.
- Git state in GCS is awkward (`.git` directory is large and changes frequently).
- Adds latency and complexity for what should be a simple clone.

**Verdict:** Over-engineered for the initial use case. GCS sync is designed for non-git workspaces. Using it for git repos fights against its design.

### 2.4 Alternative D: In-Container Clone per Agent (Current git-grove behavior)

**Concept:** Keep the current model where each agent clones independently.

**Pros:**
- Already implemented and working.
- Full per-agent isolation.

**Cons:**
- Doesn't address any of the pain points listed in Section 1.1.
- Redundant clones, ephemeral workspaces, no shared state.

**Verdict:** This is the baseline we're improving upon.

### 2.5 Decision

**Alternative B (Clone Once, Mount Shared)** is the proposed approach. It provides the best balance of simplicity, git integration, and shared workspace behavior. The isolation trade-offs are acceptable because:

1. Users choosing "shared workspace" mode are explicitly opting into shared state.
2. The same trade-off already exists for hub-native groves.
3. Per-agent isolation can be layered on later (Alternative A) as a separate grove option.

---

## 3. Detailed Design

### 3.1 Grove Type Extension

The grove creation flow gains a new option that combines git remote association with hub-native workspace behavior. This can be modeled as:

- **Data model:** A grove with both `gitRemote` set AND a flag/label indicating shared-workspace mode.
- **New label:** `scion.dev/workspace-mode: shared` (vs. the default `per-agent` for standard git groves).
- **GroveType computation:** The existing computed `GroveType` field (`git`, `linked`, `hub-native`) gains a fourth value: `git-shared`.

```go
// In pkg/store/models.go, extend GroveType computation:
func (g *Grove) ComputeGroveType() string {
    if g.GitRemote != "" {
        if g.Labels["scion.dev/workspace-mode"] == "shared" {
            return "git-shared"   // NEW: git-backed with shared workspace
        }
        if hasLinkedProvider {
            return "linked"
        }
        return "git"
    }
    return "hub-native"
}
```

### 3.2 Bootstrap: Cloning via Container

The clone operation uses a **bootstrap container invocation**, reusing the existing `sciontool init` infrastructure. This is similar to how templates are loaded from git URLs — a container is started with git credentials, runs the clone command, and exits.

#### 3.2.1 Bootstrap Flow

```
Web UI / CLI                Hub                    Runtime Broker             Bootstrap Container
   |                         |                          |                        |
   |-- create grove -------->|                          |                        |
   |   (gitRemote, mode:     |                          |                        |
   |    shared)               |                          |                        |
   |                         |                          |                        |
   |                         |-- init workspace dir --->|                        |
   |                         |   (~/.scion/groves/slug) |                        |
   |                         |                          |                        |
   |                         |-- start bootstrap ------>|                        |
   |                         |   container               |-- create container -->|
   |                         |   (GITHUB_TOKEN,          |   (git clone cmd)     |
   |                         |    clone URL)             |                       |
   |                         |                          |                  sciontool run:
   |                         |<-- status: CLONING ------|<-- event ------------|
   |                         |                          |                  git clone <url>
   |                         |                          |                  git config ...
   |                         |<-- status: READY --------|<-- container exits --|
   |                         |                          |                        |
   |<-- grove ready ---------|                          |                        |
```

#### 3.2.2 Bootstrap Container Specification

The bootstrap container is a short-lived container that:

1. Mounts the grove workspace directory (`~/.scion/groves/<slug>/`) at `/workspace`.
2. Receives `GITHUB_TOKEN` (or GitHub App token) as an environment variable.
3. Runs `git clone` as its primary command via `sciontool`.
4. Configures git remote with token-based auth.
5. Exits when clone is complete.

```bash
# Conceptual container invocation:
docker run --rm \
  -v ~/.scion/groves/<slug>:/workspace \
  -e GITHUB_TOKEN=ghp_xxx \
  -e SCION_GIT_CLONE_URL=https://github.com/org/repo.git \
  -e SCION_GIT_BRANCH=main \
  <base-image> \
  sciontool clone-workspace
```

The `sciontool clone-workspace` command (new) would:

```
1. cd /workspace
2. git clone --branch=${SCION_GIT_BRANCH:-main} \
     https://oauth2:${GITHUB_TOKEN}@<host>/<path>.git .
3. git config user.name "Scion"
4. git config user.email "agent@scion.dev"
5. git config credential.helper \
     '!f() { cat /workspace/.scion/git-credentials; }; f'
6. Write token to /workspace/.scion/git-credentials (mode 0600)
7. Exit 0
```

#### 3.2.3 Why a Container (Not Host-Side Clone)?

1. **Credential isolation** — the git token never touches the broker's host filesystem outside the workspace mount.
2. **Consistent environment** — same git version, same tooling, regardless of broker host OS.
3. **Reuse** — leverages existing container infrastructure (image pulling, volume mounting, secret injection).
4. **Network access** — containers already have network config for reaching external services.

### 3.3 Agent Workspace Mounting

Once the shared workspace is cloned, agents mount it identically to hub-native groves:

```go
// In agent provisioning (pkg/agent/provision.go):
if grove.GroveType == "git-shared" {
    // Mount the shared workspace — same as hub-native
    workspaceSource = hubNativeGrovePath(grove.Slug)
    // Skip worktree creation — workspace already exists
    shouldCreateWorktree = false
}
```

The container sees `/workspace` with a full git clone. Agents can:
- Read and modify files (shared with all other agents on this grove)
- Run `git status`, `git diff`, `git log`
- Commit and push changes
- Create and switch branches (with coordination — only one branch checked out at a time)

### 3.4 Git Credential Management

#### 3.4.1 Initial Token Storage

During the bootstrap clone, the token is embedded in the git credential helper configuration within the workspace. This allows subsequent git operations (push, pull, fetch) to authenticate without additional setup.

**Storage location:** `<workspace>/.scion/git-credentials`

This file is:
- Mode `0600` (readable only by the workspace owner)
- Listed in `.gitignore` (never committed)
- Accessible to all agents mounting the workspace

#### 3.4.2 Token Expiry and Refresh

**This is a key open question.** GitHub fine-grained PATs have configurable expiry (up to 1 year). When a token expires:

1. **Detection:** Git operations (push/pull) fail with `401 Unauthorized`.
2. **Current state:** No automatic refresh mechanism exists for PATs.

**Proposed approaches (in order of preference):**

**Option 1: Manual Token Rotation**
- User sets a new `GITHUB_TOKEN` via `scion hub secret set`.
- A `scion grove refresh-credentials` command (new) runs a lightweight bootstrap container that updates the credential file in the shared workspace.
- Simple, explicit, no background processes.

**Option 2: GitHub App Token (Auto-Refresh)**
- Use GitHub App installation tokens instead of PATs.
- Installation tokens expire after 1 hour but can be refreshed programmatically.
- `sciontool` already has a GitHub App token refresh loop (see `init.go:452-507`).
- When the grove uses a GitHub App, the credential helper calls `sciontool` to get a fresh token.
- More complex but fully automated.

**Option 3: Credential Helper Proxy**
- Run a lightweight sidecar or cron job that periodically refreshes the token file.
- Adds operational complexity.

**Recommendation:** Start with **Option 1** (manual rotation) for simplicity. The token file approach makes rotation straightforward — just overwrite the file. Design the credential helper interface to support **Option 2** in a later phase without breaking changes.

#### 3.4.3 Credential Helper Design

Rather than embedding the token directly in the git remote URL (which exposes it in `git remote -v` output), use a file-based credential helper:

```bash
# In workspace .git/config:
[credential]
    helper = !f() { echo "username=oauth2"; echo "password=$(cat /workspace/.scion/git-credentials)"; }; f
```

This approach:
- Keeps the token out of git config and remote URLs.
- Allows token rotation by updating a single file.
- Works identically for all agents mounting the workspace.
- Can be extended to call `sciontool` for GitHub App token refresh.

### 3.5 Hub-Side Changes

#### 3.5.1 Grove Creation Handler

The existing `POST /api/v1/groves` handler needs to:

1. Accept a new `workspaceMode` field (or detect it from labels).
2. When `workspaceMode: "shared"` and `gitRemote` is set:
   a. Create the grove record (as today for git groves).
   b. Create the hub-native workspace directory (`~/.scion/groves/<slug>/`).
   c. Trigger the bootstrap clone via a broker.
3. Report grove status during clone: `PROVISIONING` → `READY` (or `ERROR`).

#### 3.5.2 API Request Extension

```json
// POST /api/v1/groves
{
  "name": "my-project",
  "slug": "my-project",
  "gitRemote": "https://github.com/org/repo.git",
  "workspaceMode": "shared",
  "labels": {
    "scion.dev/default-branch": "main",
    "scion.dev/clone-url": "https://github.com/org/repo.git"
  }
}
```

#### 3.5.3 Grove Status

Groves gain a status field to track provisioning:

| Status | Meaning |
|--------|---------|
| `pending` | Grove record created, workspace not yet provisioned |
| `provisioning` | Bootstrap clone in progress |
| `ready` | Workspace cloned and ready for agents |
| `error` | Bootstrap clone failed (details in grove annotations) |

This status is advisory — it prevents starting agents on a grove whose workspace isn't ready yet.

### 3.6 Agent Provisioning Changes

When an agent is started on a `git-shared` grove:

1. **Skip git clone** — the workspace already contains a clone.
2. **Skip worktree creation** — agents share the workspace directly.
3. **Mount shared workspace** — same bind mount as hub-native groves.
4. **No branch creation** — agents work on whatever branch is checked out (or coordinate via agent instructions).

```go
// Decision logic in ProvisionAgent():
switch {
case opts.GitClone != nil:
    // Standard git grove: clone inside container
    // (existing behavior)

case groveType == "git-shared":
    // Hybrid: mount shared workspace, no clone, no worktree
    workspaceSource = hubNativeGrovePath(grove.Slug)
    shouldCreateWorktree = false

case isGit && noExplicitWorkspace:
    // Local git grove: create worktree
    // (existing behavior)

default:
    // Hub-native: mount shared workspace
    // (existing behavior)
}
```

### 3.7 Workspace File Management

The existing `handleGroveWorkspace` handler (`grove_workspace_handlers.go:84-86`) currently rejects file management for groves with `gitRemote` set:

```go
if grove.GitRemote != "" {
    Conflict(w, "Workspace file management is only available for hub-native groves")
    return
}
```

For `git-shared` groves, this check should be relaxed:

```go
if grove.GitRemote != "" && grove.Labels["scion.dev/workspace-mode"] != "shared" {
    Conflict(w, "Workspace file management is only available for hub-native and git-shared groves")
    return
}
```

---

## 4. Open Questions

### 4.1 Concurrency and Coordination

**Q: How should agents coordinate when sharing a git workspace?**

With a shared workspace, two agents running concurrently could:
- Edit the same file simultaneously
- Run `git checkout` to different branches
- Run conflicting git operations (e.g., concurrent commits)

**Potential mitigations:**
- **Agent instructions:** System prompts can instruct agents to coordinate via shared dirs or messaging.
- **File-level locking:** A simple lock file mechanism for critical sections.
- **Branch discipline:** One agent "owns" the branch; others work on files but don't do git operations.
- **Read-only mounts for secondary agents:** Only one agent mounts read-write; others mount read-only.

**Recommendation:** Start with no formal coordination mechanism. Document the shared workspace semantics clearly and let users decide coordination strategies. This matches how hub-native groves work today.

### 4.2 Branch Management

**Q: Should agents create their own branches in the shared workspace?**

In the current git-based model, each agent gets its own branch (`scion/<agent-name>`). In the shared workspace model, only one branch can be checked out at a time.

**Options:**
- **Single branch:** All agents work on the same checked-out branch. Simple but limits parallelism.
- **Git worktree within workspace:** Agents could create worktrees within the shared clone for isolated branches. This is effectively Alternative A (Section 2.1) but initiated by the agent rather than the platform.
- **Stacked branches:** Agents commit sequentially, each building on the previous agent's work.

**Recommendation:** Default to single-branch mode. Power users can use worktrees within the workspace if needed.

### 4.3 Multi-Broker Support

**Q: How does this work with multiple brokers?**

The shared workspace lives on a single broker's filesystem. For multi-broker groves:

- **Option 1:** Restrict git-shared groves to a single broker (simplest).
- **Option 2:** Use GCS sync to replicate the workspace across brokers (complex, potential for divergence).
- **Option 3:** Each broker clones independently but uses the same remote (effectively per-broker shared workspace).

**Recommendation:** Start with single-broker restriction. Multi-broker can use per-broker clones as a later enhancement.

### 4.4 `.scion` Directory in Cloned Workspace

**Q: What happens if the cloned repo already has a `.scion/` directory?**

The cloned repo might contain its own `.scion/` project configuration. The bootstrap should:
- Preserve the repo's `.scion/` directory (it's part of the source code).
- Add `.scion/git-credentials` to `.gitignore` if not already present.
- Ensure the Hub's grove settings (in the grove DB record) take precedence over any settings in the cloned `.scion/`.

### 4.5 Workspace Size and Cleanup

**Q: How are large cloned workspaces managed?**

Shallow clones (`--depth 1`) reduce initial size but limit git history. Over time, as agents create commits and fetch updates, the workspace may grow.

**Options:**
- Periodic `git gc` (can be run via `sciontool`).
- `git maintenance` scheduled task.
- Leave it to users (most repos are manageable).

### 4.6 Token in Workspace `.scion/` Directory

**Q: Is storing the token inside the workspace safe?**

The credential file at `<workspace>/.scion/git-credentials` is:
- Within the workspace mount, so all agents can access it (intended — they need it for git push).
- Not committed to git (in `.gitignore`).
- On the broker's filesystem, accessible to the broker process.

This matches the security model of `GITHUB_TOKEN` as an environment variable (which is also visible to all agents). The file-based approach is slightly more persistent but no less secure.

**Risk:** If an agent commits `.scion/git-credentials` to the repo despite `.gitignore`, the token leaks. Mitigation: the bootstrap should also configure a git pre-commit hook that blocks commits of files matching `*credentials*`.

### 4.7 Handling Clone Failures

**Q: What if the bootstrap clone fails (bad URL, expired token, network error)?**

- Grove status transitions to `error` with a descriptive message.
- Web UI displays the error with actionable guidance (e.g., "Check GITHUB_TOKEN", "Verify repository URL").
- User can retry via `scion grove reprovision <slug>` or a web UI button.
- The workspace directory may be empty or partially cloned — on retry, the bootstrap should clean and re-clone.

### 4.8 Pulling Updates from Remote

**Q: How do agents get upstream changes?**

Agents can run `git pull` manually. For automated refresh:
- A `scion grove pull <slug>` command could exec into the shared workspace and run `git pull`.
- A scheduled task could periodically pull (but risks conflicts with in-progress agent work).

**Recommendation:** Manual pull initially. Automated pull is a future enhancement that requires conflict resolution design.

---

## 5. Web UI Changes

### 5.1 Grove Creation Form

The existing `grove-create.ts` component has a `GroveMode` type with two options: `'git' | 'hub'`. This extends to three:

```typescript
type GroveMode = 'git' | 'hub' | 'git-shared';
```

The form gains a third option in the "Workspace Type" selector:

```html
<sl-option value="hub">Hub Workspace</sl-option>
<sl-option value="git">Git Repository (per-agent clone)</sl-option>
<sl-option value="git-shared">Git Repository (shared workspace)</sl-option>
```

When `git-shared` is selected:
- Show the Git Remote URL field (same as `git` mode).
- Show the Default Branch field.
- Add a note: "A single git clone will be created and shared by all agents."

### 5.2 Grove Detail Page

For `git-shared` groves, the grove detail page should show:
- Git remote URL (linked to GitHub).
- Current branch checked out.
- Workspace provisioning status.
- File browser (reuse hub-native workspace file listing).
- A "Re-clone" or "Pull Latest" action button.

### 5.3 Grove Status Display

Add provisioning status to the grove cards and detail views:
- `provisioning` → spinner with "Cloning repository..."
- `ready` → green checkmark
- `error` → red error badge with details

---

## 6. Implementation Plan

### Phase 1: Data Model & API (Foundation)

1. Add `workspaceMode` field to `CreateGroveRequest` and grove labels.
2. Add grove `Status` field to the store model (`pending`, `provisioning`, `ready`, `error`).
3. Update `ComputeGroveType()` to return `git-shared` for hybrid groves.
4. Update `handleGroveWorkspace` to allow file operations on `git-shared` groves.
5. Update `POST /api/v1/groves` handler to accept and store hybrid grove configuration.

### Phase 2: Bootstrap Clone Infrastructure

1. Add `sciontool clone-workspace` command (or extend `sciontool init` with a clone-only mode).
2. Implement bootstrap container invocation on the broker:
   - Start a short-lived container with workspace mount and git credentials.
   - Execute clone command.
   - Report status back to Hub.
3. Implement credential file setup (`.scion/git-credentials`, credential helper config).
4. Add `.gitignore` management for credential files.

### Phase 3: Agent Provisioning Integration

1. Update `ProvisionAgent()` to detect `git-shared` groves and skip worktree/clone.
2. Mount shared workspace path for agents on `git-shared` groves.
3. Ensure agent environment includes `GITHUB_TOKEN` for push operations.
4. Test concurrent agent access to shared workspace.

### Phase 4: Web UI

1. Add `git-shared` option to `grove-create.ts` form.
2. Add grove provisioning status display.
3. Enable workspace file browser for `git-shared` groves.
4. Add "Pull Latest" / "Re-clone" actions to grove detail page.

### Phase 5: Credential Refresh & Polish

1. Implement `scion grove refresh-credentials` command.
2. Add grove status API endpoints for monitoring provisioning.
3. Add retry mechanism for failed bootstrap clones.
4. Error handling and user guidance for common failure modes.
5. Documentation and template updates.

### Future Phases (Deferred)

- **GitHub App token auto-refresh** — integrate with existing `sciontool` token refresh loop.
- **Per-agent worktrees within shared clone** — Alternative A as an opt-in sub-mode.
- **Multi-broker workspace sync** — GCS-based replication of the shared workspace.
- **Automated upstream pull** — scheduled `git pull` with conflict detection.
- **Pre-commit hooks** — block accidental credential commits.

---

## 7. Decisions Record

| # | Question | Status | Decision |
|---|----------|--------|----------|
| 1 | **Workspace model?** | Resolved | Shared workspace (Alternative B). Simplest model, matches hub-native mental model. |
| 2 | **Clone mechanism?** | Resolved | Bootstrap container via `sciontool`. Reuses existing container infrastructure. |
| 3 | **Credential storage?** | Proposed | File-based credential helper in `<workspace>/.scion/git-credentials`. |
| 4 | **Token refresh?** | Open | Start with manual rotation; GitHub App auto-refresh as Phase 2. |
| 5 | **Agent coordination?** | Open | No formal mechanism initially. Document shared semantics. |
| 6 | **Multi-broker?** | Deferred | Single broker only. Multi-broker requires broader design. |
| 7 | **Branch management?** | Open | Single checked-out branch. Per-agent worktrees as future option. |
| 8 | **Grove status model?** | Proposed | New `Status` field: `pending` → `provisioning` → `ready` / `error`. |

---

## 8. References

### Design Documents

| Document | Relevance |
|----------|-----------|
| `hosted/git-groves.md` | Current git-based grove design (per-agent clone). This proposal extends it. |
| `hosted/sync-design.md` | GCS workspace sync. Non-git bootstrap in Section 13. Informs Alternative C. |
| `hosted/git-ws.md` | Research on current git workspace state. Section 4 gaps are partially addressed here. |
| `hosted/hosted-architecture.md` | Overall hosted architecture. Grove identity model. |
| `grove-dirs.md` | Shared directories design. Shared workspace mounting patterns. |
| `kubernetes/scm.md` | K8s git clone design. Init container approach (related but different). |

### Source Files

| File | Relevance |
|------|-----------|
| `pkg/hub/handlers.go` | Grove creation handler. Hub-native workspace init. |
| `pkg/hub/grove_workspace_handlers.go` | Workspace file management (currently rejects git groves). |
| `pkg/agent/provision.go` | Agent provisioning. Workspace resolution and worktree creation. |
| `pkg/runtime/common.go` | Container workspace mounting logic. |
| `cmd/sciontool/commands/init.go` | `sciontool init` — git clone phase, credential management. |
| `pkg/store/models.go` | Grove and agent data models. `GitCloneConfig`. |
| `web/src/components/pages/grove-create.ts` | Web UI grove creation form. |
