---
title: Using Tmux with Scion
---

Scion supports wrapping agent sessions in `tmux`, a terminal multiplexer. This enables persistent sessions, shared collaboration, and compatibility with certain runtimes.

## Enabling Tmux

You can enable tmux support globally, per-runtime, or for a specific agent start.

### Global/Runtime Configuration
In your `settings.json` (either global `~/.scion/settings.json` or project-local `.scion/settings.json`), set `tmux: true` within a runtime configuration.

```json
{
  "runtimes": {
    "local-dev": {
      "runtime": "docker",
      "tmux": true
    }
  }
}
```

### Profile Configuration
You can also define it in a profile:

```json
{
  "profiles": {
    "pair-programming": {
      "runtime": "local-dev",
      "tmux": true
    }
  }
}
```

When enabled, the agent container must have `tmux` installed. Standard Scion images include this by default.

## Attaching to Sessions

When `tmux` is enabled, running `scion attach <agent-name>` connects you directly to the agent's tmux session.

```bash
scion attach agent-1
```

Because the session is managed by tmux:
1.  **Persistence**: You can detach from the session (default tmux bind: `Ctrl-b` then `d`) without stopping the agent process.
2.  **Re-attach**: You can re-attach later from the same or a different terminal.

## Shared Collaboration

A powerful feature of using tmux is the ability for multiple users to attach to the same agent session simultaneously.

If you are running an agent on a remote machine (or a shared server accessible to multiple users), multiple developers can run `scion attach <agent-name>` targeting the same agent. Everyone attached will see the exact same screen and can type simultaneously. This effectively creates a **live pair-programming environment** inside the agent's context.

## Apple Silicon (Apple Container Runtime)

The `apple-container` runtime (using Apple's Virtualization Framework) **requires** tmux to support interactive attachment.

Unlike Docker, the Apple container runtime does not support standard stream attachment to a running process's PID 1. Scion works around this by executing a `tmux attach` command inside the container. If you are using the `container` runtime on macOS, you **must** set `tmux: true`.

## Known Issues

### Terminal Color & Banner Rendering

When running inside a tmux session within a container, the terminal capabilities may differ from your host terminal. You might notice that complex modern terminal graphics—specifically banner logos using specific ANSI coloring or unicode block characters—may not render correctly.

**Symptom**: You may see `___` patterns or stripped colors in banner logos instead of smooth gradients or solid blocks.

**Workaround**: This is purely cosmetic and does not affect the functionality of the agent or CLI tools.
