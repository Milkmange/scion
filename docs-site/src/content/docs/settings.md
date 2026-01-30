---
title: Scion Settings Reference
---

Scion's configuration is managed through a hierarchical settings system that allows for flexible definition of Runtimes, Harnesses, and Profiles. This configuration is stored in a `settings.json` file, which can exist at two levels:

1.  **Global Settings**: `~/.scion/settings.json` (User-wide defaults)
2.  **Grove Settings**: `.scion/settings.json` (Project-specific overrides)

## Structure

The `settings.json` file follows a "Flat Registry" model. Top-level keys define the available components, and a `profile` ties them together.

```json
{
  "active_profile": "local-dev",
  "default_template": "gemini",
  "runtimes": { ... },
  "harnesses": { ... },
  "profiles": { ... }
}
```

### 1. Runtimes
Runtimes define *where* the agent containers are executed (e.g., local Docker, Kubernetes).

-   **Key**: A unique name for the runtime (e.g., `docker-local`, `k8s-prod`).
-   **Value**: An object containing the runtime configuration.

| Field | Type | Description |
| :--- | :--- | :--- |
| `host` | string | (Docker) The path to the Docker socket. Default: `unix:///var/run/docker.sock`. |
| `namespace` | string | (Kubernetes) The namespace to deploy agents into. |
| `context` | string | (Kubernetes) The kubectl context to use. |
| `tmux` | boolean | (Optional) Whether to enable tmux by default for this runtime. See the [Tmux Guide](guides/tmux.md). |
| `env` | object | (Optional) A map of environment variables to set for the runtime execution. |

**Note**: The runtime type (`docker`, `kubernetes`, `container`) is inferred from the runtime's name in the registry or by context. Standard names are `docker`, `container`, and `kubernetes`.

**Example:**
```json
"runtimes": {
  "docker": {
    "host": "unix:///var/run/docker.sock",
    "env": {
      "DOCKER_API_VERSION": "1.41"
    }
  },
  "kubernetes": {
    "namespace": "scion-agents",
    "context": "gke_my-project_us-central1_dev-cluster"
  }
}
```

### 2. Harnesses
Harnesses define *what* software runs inside the container (e.g., Gemini CLI, Claude Code, OpenAI Codex).

-   **Key**: A unique name for the harness (typically `gemini`, `claude`, `opencode`, or `codex`).
-   **Value**: An object containing the base harness configuration.

| Field | Type | Description |
| :--- | :--- | :--- |
| `image` | string | The base Docker image to use. |
| `user` | string | The default username inside the container (e.g., `root`, `node`). |
| `env` | object | (Optional) A map of environment variables to inject into the agent container. |
| `volumes` | array | (Optional) A list of volume mounts (source, target, read_only). |

**Example:**
```json
"harnesses": {
  "gemini": {
    "image": "gemini-cli:latest",
    "user": "root",
    "env": {
      "GEMINI_MODEL": "gemini-2.5-pro"
    }
  },
  "claude": {
    "image": "claude-code:latest",
    "user": "node"
  }
}
```

### 3. Profiles
Profiles act as the "glue" that binds a Runtime to specific Harness configurations and behavioral flags. They represent a complete environment (e.g., "Local Development", "Production").

-   **Key**: A unique name for the profile (e.g., `local`, `prod`).
-   **Value**: An object defining the profile's behavior.

| Field | Type | Description |
| :--- | :--- | :--- |
| `runtime` | string | The name of the runtime to use (must exist in `runtimes`). |
| `tmux` | boolean | Whether to wrap the agent process in a `tmux` session. See the [Tmux Guide](guides/tmux.md). |
| `env` | object | (Optional) A map of environment variables to set for this profile (merges into runtime env). |
| `harness_overrides` | object | (Optional) A map of harness names to override specific settings. |

**Example:**
```json
"profiles": {
  "local": {
    "runtime": "docker",
    "tmux": true,
    "env": {
       "DEBUG": "true"
    },
    "harness_overrides": {
      "gemini": {
        "image": "gemini-cli:dev",
        "volumes": [
          {"source": "/tmp/logs", "target": "/logs"}
        ]
      }
    }
  },
  "prod": {
    "runtime": "kubernetes",
    "tmux": false
  }
}
```

### 4. Top-Level Settings
These settings apply globally or define defaults.

| Field | Type | Description |
| :--- | :--- | :--- |
| `active_profile` | string | The profile to use by default. Can be overridden with `--profile`. |
| `default_template` | string | The default template to use when creating new agents if none is specified. |
| `cli.autohelp` | boolean | (Optional) Whether to print usage help on every error. Default: `true`. |

**Example:**
```json
"active_profile": "local",
"default_template": "gemini",
"cli": {
  "autohelp": false
}
```

## Environment Variable Substitution

Scion supports environment variable substitution in `settings.json` for all `env` maps (both keys and values) and `volumes` (both source and target paths). This allows you to create portable configurations that adapt to different user environments.

Variables can be specified using either `${VAR}` or `$VAR` syntax. If a variable is not set in the host environment, a warning will be printed to stderr, and the variable will evaluate to an empty string.

**Example: Using GOPATH for volume mounts**

```json
"profiles": {
  "work": {
    "runtime": "docker",
    "volumes": [
      {
        "source": "${GOPATH}/pkg",
        "target": "/go/pkg"
      }
    ]
  }
}
```

## Resolution Logic

When Scion starts an agent, it resolves the configuration in the following order:

1.  **Determine Profile**:
    *   Command line flag: `scion start ... --profile prod`
    *   `active_profile` in `settings.json`
    *   Default hardcoded fallback (if neither is present).

2.  **Load Components**:
    *   Scion looks up the `runtime` specified in the active profile.
    *   Scion loads the base configuration for the requested `harness` (e.g., from the agent's template, typically one of `gemini`, `claude`, `opencode`, or `codex`).

3.  **Apply Overrides**:
    *   Any `overrides` defined in the active profile for the specific harness are applied on top of the base harness configuration. For example, replacing the `image` or `user`.

## Example Configuration

A complete `settings.json` might look like this:

```json
{
  "active_profile": "local",
  "runtimes": {
    "docker": {
      "host": "unix:///var/run/docker.sock"
    },
    "kubernetes": {
      "namespace": "default",
      "context": ""
    }
  },
  "harnesses": {
    "gemini": {
      "image": "us-central1-docker.pkg.dev/my-project/repo/gemini-cli:latest",
      "user": "root"
    },
    "claude": {
      "image": "us-central1-docker.pkg.dev/my-project/repo/claude-code:latest",
      "user": "node"
    }
  },
  "profiles": {
    "local": {
      "runtime": "docker",
      "tmux": true
    },
    "prod": {
      "runtime": "kubernetes",
      "tmux": false,
      "harness_overrides": {
        "gemini": {
          "image": "us-central1-docker.pkg.dev/my-project/repo/gemini-cli:stable"
        }
      }
    }
  }
}
```

## Use Cases

### Injecting Git Identity and Caches

You can use profiles to inject personal configurations, such as git authorship identity, into your agents. This example also demonstrates how to mount a local directory (e.g., a package cache) to speed up development within the agent.

```json
  "profiles": {
    "local-dev": {
      "runtime": "container",
      "tmux": true,
      "env": {
        "GIT_AUTHOR_EMAIL": "user@example.com",
        "GIT_AUTHOR_NAME": "Jane Doe",
        "GIT_COMMITTER_EMAIL": "user@example.com",
        "GIT_COMMITTER_NAME": "Jane Doe"
      }
    }
  }
```
