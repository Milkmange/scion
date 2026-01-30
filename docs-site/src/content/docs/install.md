---
title: Installation Guide
---

This guide covers the steps to install and configure Scion on your local machine.

## Prerequisites

### 1. Go
Scion is written in Go. You need Go 1.22 or later installed.
- [Download and install Go](https://golang.org/doc/install)

### 2. Container Runtime
Scion requires a container runtime to manage agents. You can use either Docker or the Apple Virtualization Framework (experimental).

#### Docker (Linux/Windows)
- Install [Docker Desktop](https://www.docker.com/products/docker-desktop) or [Docker Engine](https://docs.docker.com/engine/install/).
- Ensure the `docker` command is available in your PATH.

#### Apple Virtualization (macOS only)
- Requires the [container](https://github.com/apple/container/releases) tool (an Apple tool for running OCI images in micro VMs).
- Ensure the `container` command executes.
- Start the system services `container system start`.
- Although the container images refrenced in this project are public, the container tool seems to require auth, until this can be investigated, this will authorized the container tool to pull images from the GCP Artifact registry (requires gcloud) `gcloud auth print-access-token | container registry login --username oauth2accesstoken --password-stdin us-central1-docker.pkg.dev`

### 3. Git
Scion uses `git worktree` to manage agent workspaces.
- Ensure `git` is installed and available in your PATH.
- Because Scion uses a new feature for relative path worktrees ensure that `git --version` >= 2.48

For Ubuntu you can install the latest version with

```bash
add-apt-repository ppa:git-core/ppa

apt update; apt install git
```

For Debian you may need to build from source, see the [git site](https://git-scm.com/install/source), or see the Dockerfile in this repo for the base image.

---

## Scion Installation

### From Source
You can install Scion directly using `go install`:

```bash
go install github.com/ptone/scion-agent/cmd/scion@latest
```

Ensure your `$GOPATH/bin` (typically `~/go/bin`) is in your system `$PATH`.

### From Binary
If you have the repository cloned, you can use the provided `Makefile`:

```bash
make build
# This creates a 'scion' binary in the current directory.
# You can move it to a directory in your PATH:
mv scion /usr/local/bin/
```

To verify your installation, run:

```bash
scion version
```

---

## Configuration

### 1. Initialize a Grove
Navigate to the root of a project where you want to use Scion and run:

```bash
scion init
```

This creates a `.scion` directory in your project root containing:
- `settings.json`: Grove-specific settings.
- `templates/`: Default agent templates (gemini, claude, etc.).

**Note:** If you are in a git repository, it is recommended to add `.scion/agents` to your `.gitignore` to avoid issues with nested git worktrees:
```bash
echo ".scion/agents" >> .gitignore
```

### 2. Select Runtime
Scion automatically selects the appropriate runtime based on your operating system:
- **macOS**: Defaults to `container` (Apple Virtualization Framework).
- **Linux/Windows**: Defaults to `docker`.

If you wish to change this (e.g., to use Docker on macOS), you can manually edit `.scion/settings.json`:

```json
{
  "profiles": {
    "local": {
      "runtime": "docker"
    }
  }
}
```

---

## Shell Completions

Scion provides shell completions for `bash`, `zsh`, `fish`, and `powershell`.

### Zsh

If you have installed `scion` and want to enable completions:

1.  Generate the completion script:
    ```bash
    scion completion zsh > _scion
    ```
2.  Move the file to a directory in your `$fpath`.

**For macOS users:**
If you are using Homebrew, you likely already have a configured site-functions directory. If you do **not** use Homebrew or prefer a manual setup:

1.  Create the directory if it doesn't exist:
    ```bash
    sudo mkdir -p /usr/local/share/zsh/site-functions
    ```
2.  Move the completion file:
    ```bash
    sudo mv _scion /usr/local/share/zsh/site-functions/
    ```
3.  Ensure that directory is in your `$fpath` in your `~/.zshrc` (usually added automatically, but verify if completions don't work):
    ```bash
    # in ~/.zshrc
    fpath=(/usr/local/share/zsh/site-functions $fpath)
    autoload -U compinit; compinit
    ```

### Bash

To load completions for the current session:
```bash
source <(scion completion bash)
```

To load completions for each session, execute once:
```bash
# Linux:
scion completion bash > /etc/bash_completion.d/scion

# macOS:
scion completion bash > /usr/local/etc/bash_completion.d/scion
```

---

## Troubleshooting

### `git worktree` errors
Ensure your project is a git repository. `scion init` and `scion start` require being inside a git repository to manage workspaces.

### Permission Denied (Docker)
Ensure your user has permissions to run Docker commands without `sudo`. On Linux, add your user to the `docker` group.

### Path Issues
If `scion` command is not found after `go install`, add the following to your shell profile (`.zshrc` or `.bashrc`):

```bash
export PATH=$PATH:$(go env GOPATH)/bin
```
