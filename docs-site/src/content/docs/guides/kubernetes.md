---
title: Running Scion on Kubernetes (Experimental)
---

Scion supports running agents as Pods in a Kubernetes cluster. This allows for remote execution, better resource management, and scaling.

## Prerequisites

- A running Kubernetes cluster.
- `kubectl` configured on your local machine.
- Scion agent images available to the cluster (e.g., pushed to a registry or loaded into the cluster).

## Configuration

To use the Kubernetes runtime, you need to configure the `kubernetes` section in your `scion-agent.json` (either in a template or a specific agent instance).

### Example Configuration

```json
{
  "harness": "gemini",
  "kubernetes": {
    "namespace": "default",
    "context": "minikube",
    "resources": {
      "requests": {
        "cpu": "500m",
        "memory": "1Gi"
      },
      "limits": {
        "cpu": "2",
        "memory": "4Gi"
      }
    }
  }
}
```

### Fields

- **context**: (Optional) The kubectl context to use. If omitted, uses the current context.
- **namespace**: The namespace to deploy the agent into. Defaults to `default`.
- **runtimeClassName**: (Optional) For using sandboxed runtimes like gVisor or Kata Containers.
- **resources**: Standard Kubernetes resource requests and limits.

## Execution Flow

1.  **Start**: When you run `scion start`, Scion connects to the cluster and creates a Pod for the agent.
2.  **Sync**: Scion uses `mutagen` (if configured) or direct volume mounts (if local) to sync the workspace. *Note: Remote volume syncing is currently in active development.*
3.  **Attach**: `scion attach` streams the TTY from the container running in the Pod.
4.  **Stop**: `scion stop` deletes the Pod but preserves the workspace data if persistent volumes are configured.

## Limitations

- This feature is currently **experimental**.
- Networking between the host and the pod for file syncing requires careful setup.
- Authentication credentials must be propagated correctly to the remote pod.
