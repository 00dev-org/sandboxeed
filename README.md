# sandboxeed

A sandboxed container environment with filtered internet access via a Squid proxy.

One consistent Docker-based sandbox for all of your AI agents.

It addresses common issues across popular agent tools:
- Claude creating empty dotfiles breaking git commands: https://github.com/anthropic-experimental/sandbox-runtime/issues/139
- Codex not having network access when sandbox is enabled
- No built-in sandbox in OpenCode or Copilot CLI
- Inconsistent sandbox experience when using different AI tools.

## Requirements

- Linux/macOS
- Docker
- Go 1.25+ (to build from source)

## Installation

```bash
go build -o sandboxeed .
```

Or use the pre-built binary if available.

## Usage

```bash
# Start an interactive shell in the sandbox
sandboxeed

# Show command help
sandboxeed help

# Run an AI agent tool
sandboxeed claude

# Run a specific command
sandboxeed node script.js

# Build the sandbox image only
sandboxeed --build

# Build and run a specific command
sandboxeed --build claude

# Run without Docker-in-Docker even if docker: true is in the config
sandboxeed --no-docker claude

# Print the current app version
sandboxeed version

# List sandboxeed-owned Docker resources and remove them after confirmation
sandboxeed cleanup
```

### Flags

| Flag           | Description                                                                   |
|----------------|-------------------------------------------------------------------------------|
| `--build`      | Build the Docker image; if a command is provided, run it afterward            |
| `--no-docker`  | Skip Docker-in-Docker even if `docker: true` is set in `sandboxeed.yaml`      |

### Arguments

The first non-flag argument is the command to run inside the container (default: `bash`). Any additional arguments are
passed to that command. When `--build` is provided without a command, sandboxeed builds the image and exits.

`version` is a built-in command that prints the app version from Go build metadata. Local builds typically print
`devel`; module-aware installs such as `go install` print the module version when available.

`help` is a built-in command that prints a short usage summary.

`cleanup` is a built-in command that scans Docker for sandboxeed-managed containers, networks, and volumes, prints the
exact resources it found, and asks for confirmation before force-removing them. It does not remove images.

## How it works

1. A Squid proxy container is started with a generated config that whitelists only the domains listed in
   `sandboxeed.yaml`.
2. The sandbox container is started connected only to an internal network — all outbound traffic goes through the proxy.
3. When the sandbox exits, sandboxeed removes the proxy, sandbox, optional DinD sidecar, and their per-run networks.

Each run creates uniquely named containers, networks, and DinD volume resources, so interrupted starts do not block the
next run and multiple sandboxes can run concurrently from the same directory.

## Configuration

Place a `sandboxeed.yaml` file in your project directory:

```yaml
sandbox:
  build:
    dockerfile: Dockerfile.sandbox   # Dockerfile to build the image (used with --build)
  image: my-custom-image:latest      # Image to use (omit if using build)
  volumes:                           # appended to defaults
    - ~/.gitconfig:/root/.gitconfig:ro
  environment:                       # appended to defaults
    - MY_VAR=value
  working_dir: /workspace            # default: /workspace
  docker: true                       # enable Docker-in-Docker support
  domains:                           # Whitelisted outbound domains
    - github.com
    - api.example.com
    - .docker.io                     # wildcard: matches docker.io and all subdomains
```

### Configuration fields

| Field              | Description                                                            |
|--------------------|------------------------------------------------------------------------|
| `build.dockerfile` | Path to the Dockerfile. Used by `--build`, and also for automatic builds when the configured image tag does not exist locally. Defaults to `Dockerfile` only when `--build` is used without an explicit path. |
| `image`            | Docker image to run and, with `--build`, the image tag to build. Defaults to a per-project tag like `<project>-sandboxeed`; without build config, sandboxeed falls back to `bash:latest`. |
| `volumes`          | Extra volume mounts added after the default `.:/workspace` mount. Supports `~` and `./`. |
| `environment`      | Extra environment variables added after the proxy defaults (`HTTP_PROXY`, etc.). |
| `working_dir`      | Working directory inside the container. Default: `/workspace`.         |
| `docker`           | Set to `true` to start a Docker-in-Docker sidecar (see below).         |
| `domains`          | Domains the sandbox is allowed to reach. All other traffic is blocked. |

### Agent examples

If you want a starter setup for a specific agent, copy one of the example pairs from [`examples/`](examples/README.md):

- [`examples/claude/Dockerfile.sandbox`](examples/claude/Dockerfile.sandbox) and [`examples/claude/sandboxeed.yaml`](examples/claude/sandboxeed.yaml)
- [`examples/codex/Dockerfile.sandbox`](examples/codex/Dockerfile.sandbox) and [`examples/codex/sandboxeed.yaml`](examples/codex/sandboxeed.yaml)
- [`examples/gemini/Dockerfile.sandbox`](examples/gemini/Dockerfile.sandbox) and [`examples/gemini/sandboxeed.yaml`](examples/gemini/sandboxeed.yaml)
- [`examples/opencode/Dockerfile.sandbox`](examples/opencode/Dockerfile.sandbox) and [`examples/opencode/sandboxeed.yaml`](examples/opencode/sandboxeed.yaml)

Those files are project-agnostic starters. Copy them into your project root as `Dockerfile.sandbox` and `sandboxeed.yaml`, then adjust packages, mounts, and allowed domains for your actual project.

### No config file

If no `sandboxeed.yaml` is found, sandboxeed runs a `bash:latest` container with:

- Current directory mounted at `/workspace` (default volume)
- Working directory set to `/workspace`
- Proxy environment variables set (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`)
- `bash` as the shell
- No outbound internet access (all traffic blocked by proxy)

## Docker-in-Docker

When `docker: true` is set in the config, sandboxeed starts a `docker:dind` sidecar container on the internal
network. The sandbox container is configured to use it automatically via `DOCKER_HOST=tcp://dind:2375`.

This lets you run `docker` commands inside the sandbox (for example, `docker build` and `docker run`) without granting the
sandbox container privileged access. The DinD container itself runs privileged, but is isolated within the internal
network — its outbound traffic goes through the Squid proxy like everything else.

Starting the DinD sidecar typically adds about 10 seconds to sandbox startup time. To skip that overhead for a single
run, use `sandboxeed --no-docker ...` even when `docker: true` is set in the config.

TLS is disabled between the sandbox and DinD since they communicate over an internal-only network with no external
routing.

When using Docker-in-Docker, make sure your `domains` list includes the registries you need to pull from (e.g.,
`.docker.io`, `.cloudflarestorage.com` for Docker Hub image layers).

## Network isolation

The sandbox container has **no direct internet access**. All traffic is routed through a Squid proxy that only allows
connections to the domains listed under `domains`. An empty `domains` list blocks all outbound traffic.

Two Docker networks are created per run:

- `<project>-internal-<run-id>` — connects the sandbox, proxy, and optional DinD sidecar (internal, no external routing)
- `<project>-egress-<run-id>` — connects the proxy to the outside world

When Docker-in-Docker is enabled, sandboxeed also creates a per-run labeled volume for `/var/lib/docker` inside the
DinD sidecar so the volume can be cleaned up reliably.

## Security considerations

sandboxeed reduces the attack surface of running untrusted or semi-trusted code, but it is **not a complete security
boundary**. Keep the following in mind:

- **Docker-in-Docker runs privileged.** The DinD sidecar requires `--privileged`, which grants full host kernel access
  to that container. It is isolated on the internal network, but a container escape from DinD could compromise the host.
  Only set `docker: true` when you need it.
- **Domain filtering is not bulletproof.** The Squid proxy filters by domain name, not by IP. DNS rebinding, tunneling
  over allowed domains (e.g., via a compromised CDN), or exfiltration through DNS itself are not prevented.
- **Volume mounts expose host files.** Any path listed under `volumes` is directly accessible inside the sandbox.
  Sensitive files (SSH keys, API tokens, config files) mounted into the container can be read or exfiltrated to allowed
  domains. Mount with `:ro` where possible and avoid mounting credentials you don't need. If you mount a Git SSH key,
  use a dedicated key with the narrowest repository or project scope you can enforce instead of a broader personal key.
- **No syscall filtering.** The sandbox container runs with Docker's default seccomp profile but no additional
  restrictions. It is not equivalent to a VM-level isolation boundary like gVisor or Firecracker.
- **Example configs are intentionally permissive.** The files in `examples/` disable host key checking, approval
  prompts, and TLS verification for convenience in disposable environments. **Do not use them outside a sandbox.**
- **Proxy bypass.** If the sandbox container gains the ability to manipulate its own network namespace or routes
  (e.g., via `CAP_NET_ADMIN`), it could bypass the proxy entirely. The default Docker capability set does not grant
  this, but custom images or runtime flags could.
