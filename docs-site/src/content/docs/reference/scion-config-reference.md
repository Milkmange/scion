---
title: Configuration Reference
---

TODO this file is pretty out of date




Scion uses two primary configuration files:

1. **`settings.json`**: Global or Grove-level orchestrator configuration (Runtimes, Harnesses, Profiles).

2. **`scion-agent.json`**: Template or Agent-level execution configuration.



---



## 1. `scion-agent.json`



This file is found in `.scion/templates/<name>/scion-agent.json` and in each agent's home directory.



### Fields



#### `harness` (string)

The agent harness to use. 

- **Supported**: `gemini`, `claude`, `opencode`, `codex`.



#### `config_dir` (string)

The directory within the agent's home that contains harness-specific configuration files.

- **Example**: `".gemini"`



#### `env` (map[string]string)

Environment variables to set in the agent container.

- **Example**: `{"PROJECT_ID": "my-gcp-project"}`



#### `volumes` (array)

A list of volume mounts to add to the agent container.

- **Fields**: `source`, `target`, `read_only` (bool)



#### `detached` (boolean)

Whether the agent should run in detached mode by default.



#### `command_args` (array of strings)

Additional arguments passed to the agent's entrypoint.



#### `model` (string)

The model ID to use for the agent (harness-specific).



#### `kubernetes` (object)

Agent-specific Kubernetes overrides.

- **Fields**: `context`, `namespace`, `runtimeClassName`, `resources`.



#### `gemini` (object)

Gemini-specific configuration.

- **Fields**: `auth_selectedType`.



---



## 2. `settings.json`



Managed at `~/.scion/settings.json` or `.scion/settings.json`.



### `profiles` (object)

Named execution environments.

- **Example**:

  ```json

  "profiles": {

    "local": {

      "runtime": "docker",

      "tmux": true

    }

  }

  ```



### `runtimes` (object)

Runtime backend configurations.

- **Types**: `docker`, `container` (macOS), `kubernetes`.



### `harnesses` (object)

Global defaults for harnesses.

- **Fields**: `image`, `user`, `env`, `volumes`.
