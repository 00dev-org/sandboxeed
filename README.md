# sandboxeed

A Docker-based sandbox with filtered internet access via a Squid proxy.

One consistent environment for all of your AI coding agents.

It addresses common issues across popular agent tools:

- Claude creating empty dotfiles breaking git
  commands: https://github.com/anthropic-experimental/sandbox-runtime/issues/139
- Codex not having network access when sandbox is enabled
- No built-in sandbox in OpenCode or Copilot CLI
- Inconsistent sandbox experience when using different AI tools

## Architecture

```
  HOST
  +--------------------------------------------------------------------------+
  |                                                                          |
  |  $ sandboxeed claude                                                     |
  |        |                                                                 |
  |        |  manages container lifecycle                                    |
  |        |                                                                 |
  |  +-----+----------------- internal network ----------------------------+ |
  |  |                                                                     | |
  |  |  +-----------------------------+   +-----------------------------+  | |
  |  |  |          sandbox            |   |        squid proxy          |  | |
  |  |  |                             |-->|                             |  | |
  |  |  |  AI agent                   |   |  [+] github.com             |  | |
  |  |  |  (claude / codex /          |   |  [+] api.openai.com         |  | |
  |  |  |   gemini / opencode / ...)  |   |  [-] everything else -> 403 |  | |
  |  |  |                             |   +---------------+-------------+  | |
  |  |  |  /workspace <- ./           |                   |                | |
  |  |  |  (host dir, always mounted) |                   | egress network | |
  |  |  +-----------------------------+                   |                | |
  |  |                                                    |                | |
  |  |  +-----------------------------+                   |                | |
  |  |  |       DinD  (optional)      |                   |                | |
  |  |  |       Podman-in-Docker      |                   |                | |
  |  |  |       unix:///var/sock/...  |                   |                | |
  |  |  +-----------------------------+                   |                | |
  |  +------------------------------------------------------+--------------+ |
  |                                                         |                |
  +---------------------------------------------------------+----------------+
                                                            |
                                                            v
                                                   +-----------------+
                                                   |    internet     |
                                                   |  (allowed only) |
                                                   +-----------------+
```

The sandbox container has **no direct internet access** - all outbound traffic is
forced through the Squid proxy, which only forwards connections to your configured
`domains`. Two Docker networks are created per run: an internal-only network shared
by the sandbox, proxy, and optional DinD sidecar, and an egress network that gives
only the proxy a path to the internet.

## Requirements

- Linux/macOS
- Docker or Podman (via a Docker-compatible `docker` CLI)
- Go 1.25+ (to build from source)

Podman is supported when `docker` points to a Podman-compatible CLI. On macOS, sandboxeed uses a
Podman-specific forced container removal path during shutdown to avoid slow proxy teardown.

## Installation

```bash
go build -o sandboxeed .
```

Or use the pre-built binary if available.

## Quick start

1. Copy an example config for your agent:

```bash
cp examples/claude/Dockerfile.sandbox ./Dockerfile.sandbox
cp examples/claude/sandboxeed.yaml ./sandboxeed.yaml
```

2. Build the sandbox image:

```bash
sandboxeed --build
```

3. Run your agent:

```bash
sandboxeed claude
```

See [Agent examples](#agent-examples) for all supported agents.

## Usage

```bash
# Start an interactive shell in the sandbox
sandboxeed

# Run an AI agent tool
sandboxeed claude

# Run any command inside the sandbox
sandboxeed node script.js

# Build the sandbox image, then run a command
sandboxeed --build claude

# Build the sandbox image only (no command)
sandboxeed --build

# Skip Docker-in-Docker for this run (if enabled)
sandboxeed --no-docker claude

# Print the current version
sandboxeed --version

# Remove leftover sandboxeed Docker resources
sandboxeed --cleanup
```

### Flags

| Flag          | Description                                                                              |
|---------------|------------------------------------------------------------------------------------------|
| `--build`     | Build the Docker image (always `--no-cache`); if a command is provided, run it afterward |
| `--no-docker` | Skip Docker-in-Docker even if `docker: true` is set in `sandboxeed.yaml`                 |
| `--read-only` | Mount all volumes (including the project directory) as read-only inside the sandbox      |
| `--unsafe`    | Run DinD in privileged mode (insecure, for testing only)                                 |

### Built-in commands

- `--help` - show the usage summary (`-h` also works)
- `--version` - print the app version
- `--inspect` - print the effective merged sandbox config without starting containers
- `--cleanup` - list sandboxeed-managed containers, networks, and volumes and remove them after
  confirmation; images are not removed

Sandboxeed options are only parsed before the sandboxed command starts. The first positional token is
always the sandboxed command, and every token after that belongs to the sandboxed command. Use `--`
to force the next token to be treated as the sandboxed command when it begins with `-` or `--`. If
no command is given, an interactive `bash` shell opens.

### Auto-build

When `build.dockerfile` is set in `sandboxeed.yaml` and the configured image does not exist locally,
sandboxeed builds it automatically before starting the sandbox. Use `--build` to force a rebuild
regardless.

## Startup sequence

When you run `sandboxeed`:

1. If `github.com` or `.github.com` is in your `domains` list, GitHub's current SSH host keys are
   fetched from the GitHub API and injected into the sandbox at `/etc/ssh/` alongside an SSH config
   that routes `github.com` through the proxy via `corkscrew`.
2. A Squid proxy container starts with an allowlist of the domains you configured.
3. The sandbox container starts connected to an internal Docker network - all outbound traffic goes
   through the proxy, and everything not on your domain list is blocked.
4. When the sandbox exits, sandboxeed removes the proxy, sandbox, any DinD sidecar, and all
   per-run networks and volumes.

Each run uses uniquely named containers, networks, and volumes, so a crashed or interrupted run
won't block the next one and multiple sandboxes can run concurrently from the same directory.

## Configuration

Configuration is loaded in this order:

1. Built-in defaults
2. Optional user config at `~/.sandboxeed.yaml`
3. Optional project config at `./sandboxeed.yaml`

Later layers override earlier ones. Environment variables are merged by variable name, volume mounts
are merged by container destination path, and domains are deduplicated.

For reusable user-managed sandbox images, `sandbox.image` and `sandbox.build.dockerfile` can live in
`~/.sandboxeed.yaml`. A project config can either inherit that pair unchanged, override both with its own
build settings, or override just `image` to use a different prebuilt image.

The current directory is always mounted at `/workspace` inside the sandbox. Any volumes you
configure are merged on top of that default.

Place a `sandboxeed.yaml` in your project directory:

```yaml
sandbox:
  build:
    dockerfile: Dockerfile.sandbox   # Dockerfile for --build and auto-build
  image: my-custom-image:latest      # Image to run (required when this file is present)
  volumes:
    - ~/.gitconfig:/root/.gitconfig:ro
  environment:
    - MY_VAR=value
  working_dir: /workspace            # default: /workspace
  docker: true                       # enable Docker-in-Docker support
  memory: 2g                         # sandbox container memory limit
  cpus: "2"                          # sandbox container CPU limit
  pids: 512                          # sandbox container PID limit
  domains: # whitelisted outbound domains
    - github.com
    - api.example.com
    - .docker.io                     # matches docker.io and all subdomains
```

### Configuration fields

| Field              | Description                                                                                                                                                                                      |
|--------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `build.dockerfile` | Path to the Dockerfile. Used by `--build` and for automatic builds when the configured image tag is not found locally. Defaults to `Dockerfile` when `--build` is used without an explicit path. |
| `image`            | Required when `sandboxeed.yaml` is present. The Docker image to run (and to tag when using `--build`).                                                                                           |
| `volumes`          | Extra volume mounts added after the default `.:/workspace` mount. Supports `~`, `./`, and `../` path prefixes. When multiple entries target the same container path, the last config layer wins. |
| `environment`      | Extra environment variables added after the proxy defaults (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`). When the same variable appears more than once, the last config layer wins.                 |
| `working_dir`      | Working directory inside the container. Default: `/workspace`.                                                                                                                                   |
| `docker`           | Set to `true` to start a Docker-in-Docker sidecar (see [Docker-in-Docker](#docker-in-docker)).                                                                                                   |
| `memory`           | Memory limit for the sandbox container, passed through to Docker as `--memory`. Supported in both project and user config.                                                                       |
| `cpus`             | CPU limit for the sandbox container, passed through to Docker as `--cpus`. Supported in both project and user config.                                                                            |
| `pids`             | PID limit for the sandbox container, passed through to Docker as `--pids-limit`. Supported in both project and user config.                                                                      |
| `domains`          | Domains the sandbox is allowed to reach. All other outbound traffic is blocked. User and project domains are combined and deduplicated.                                                          |

### User config

Keep personal defaults in `~/.sandboxeed.yaml`. Only these fields are supported there:

- `sandbox.build.dockerfile`
- `sandbox.image`
- `sandbox.volumes`
- `sandbox.environment`
- `sandbox.domains`
- `sandbox.memory`
- `sandbox.cpus`
- `sandbox.pids`

`sandbox.build.dockerfile` in either user or project config requires `sandbox.image` in the same config layer so
builds always target a predictable image name.

Fields such as `working_dir` and `docker` belong in the project config.
sandboxeed will report an error if they appear in the user config.

Use it to avoid committing user-dependent changes to the project repository and to avoid repetition across multiple
projects.
For example: mount configurations for AI agents you use, set common environment variables, or whitelist commonly used
domains.

User config:

```yaml
# ~/.sandboxeed.yaml
sandbox:
  build:
    dockerfile: ~/.sandboxeed/Dockerfile
  image: localhost/sandboxeed:latest
  volumes:
    - ~/.claude:/home/node/.claude
    - ~/.claude.json:/home/node/.claude.json
    - ~/.sandboxeed/.claude/settings.json:/home/node/.claude/settings.json
    - ~/.codex:/home/node/.codex
    - ~/.sandboxeed/.codex/config.toml:/home/node/.codex/config.toml
    - ~/.gitconfig:/home/node/.gitconfig:ro
  environment:
    - DISABLE_AUTOUPDATER=1
  memory: 2g
  cpus: "2"
  pids: 512
  domains:
    - .anthropic.com
    - .claude.ai
    - .claude.com
    - .openai.com
    - .chatgpt.com
```

Project config:

```yaml
# ./sandboxeed.yaml
sandbox:
  build:
    dockerfile: Dockerfile.sandbox
  image: my-custom-image:latest
  environment:
    - ENV=DEV # overrides the user config value
  domains:
    - .github.com # combined with api.openai.com from user config
```

In the example above, the final allowed domain list includes `.anthropic.com`, `.claude.ai`,
`.claude.com`, `.openai.com`, `.chatgpt.com` from the user config and `.github.com` from the
project config. The `ENV` variable is set only by the project config; user-level volumes and
`DISABLE_AUTOUPDATER` carry through unchanged.

### Agent examples

For a ready-to-use starting point, copy the files for your agent from [`examples/`](examples/README.md):

| File                                            | Scope  | Description                                                        |
|-------------------------------------------------|--------|--------------------------------------------------------------------|
| [`.sandboxeed.yaml`](examples/.sandboxeed.yaml) | Global | User config (`~/.sandboxeed.yaml`) with shared volumes and domains |

| Agent                           | Project files                                            |
|---------------------------------|----------------------------------------------------------|
| [Claude Code](examples/claude/) | `Dockerfile.sandbox`, `sandboxeed.yaml`, `settings.json` |
| [Codex CLI](examples/codex/)    | `Dockerfile.sandbox`, `sandboxeed.yaml`, `config.toml`   |
| [Gemini CLI](examples/gemini/)  | `Dockerfile.sandbox`, `sandboxeed.yaml`                  |
| [OpenCode](examples/opencode/)  | `Dockerfile.sandbox`, `sandboxeed.yaml`                  |

Copy `.sandboxeed.yaml` to `~/.sandboxeed.yaml` for global defaults, and tool-specific config files
(like `settings.json` or `config.toml`) to `~/.sandboxeed/` for mounting into the sandbox.
Per-agent `Dockerfile.sandbox` and `sandboxeed.yaml` go in your project root.

These are project-agnostic starters. You will typically need to add language runtimes, package
managers, SDKs, or extra allowed domains for your actual project.

### No config file

If no `sandboxeed.yaml` is found, sandboxeed runs a `bash:latest` container with:

- Current directory mounted at `/workspace`
- Proxy environment variables set (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`)
- No outbound internet access (all traffic blocked by the proxy)

## Dockerfile requirements

sandboxeed injects several things into the sandbox at runtime - your Dockerfile does not need to
set these up:

| What              | Where                                      | Notes                                                                                                                                                     |
|-------------------|--------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------|
| SSH client config | `/etc/ssh/ssh_config`                      | Only when `github.com` or `.github.com` is in `domains`. Routes `github.com` through the proxy via `corkscrew`; overwrites any existing file at that path |
| GitHub host keys  | `/etc/ssh/ssh_known_hosts`                 | Only when `github.com` or `.github.com` is in `domains`. Fetched from the GitHub API at startup; `StrictHostKeyChecking yes`                              |
| Proxy env vars    | `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`    | Set for every run                                                                                                                                         |
| Docker socket     | `DOCKER_HOST=unix:///var/sock/podman.sock` | Only when `docker: true`; shared via a volume from the Podman-in-Docker sidecar                                                                           |
| Project mount     | `.:/workspace`                             | Current directory, always mounted                                                                                                                         |

**What your Dockerfile must provide:**

- **`corkscrew`** - required when `github.com` or `.github.com` is in `domains`. Used to tunnel
  SSH through the HTTP proxy; without it, `git clone` via SSH will fail.
- **`docker` CLI** - required when `docker: true` is set. The DinD sidecar provides the daemon but
  the client must be present in the image.

**Recommended patterns:**

- Create `/workspace` and `chown` it to the runtime user so the default volume mount is writable.
- Install your tools and runtimes as root, then switch to a non-root user with `USER` before the
  `CMD`. The injected files at `/etc/ssh/` only need to be readable, so no root access is required
  at runtime.
- If a tool does not respect `HTTP_PROXY`/`HTTPS_PROXY`, configure its proxy settings explicitly in
  the Dockerfile (for example, npm's `proxy` config or a tool-specific config file).

## Docker-in-Docker

When `docker: true` is set in the config, sandboxeed starts a Podman-in-Docker sidecar container on
the internal network. The sandbox container is configured to use it automatically via
`DOCKER_HOST=unix:///var/sock/podman.sock`, with the Podman socket shared between the sidecar and
the sandbox through a Docker volume.

Unlike traditional `docker:dind`, the Podman sidecar does **not** require `--privileged`. It runs
with a minimal set of capabilities (`SYS_ADMIN` for fuse-overlayfs storage, `seccomp=unconfined`,
and `apparmor=unconfined`). This blocks the most common container escape vectors — the sidecar
cannot access host block devices or the host device cgroup.

For testing scenarios that require full privileged access (e.g., creating custom container networks),
use the `--unsafe` flag to run the DinD sidecar in privileged mode.

Starting the DinD sidecar typically adds about 10 seconds to sandbox startup time. To skip that
overhead for a single run, use `sandboxeed --no-docker ...` even when `docker: true` is set.

When using Docker-in-Docker, make sure your `domains` list includes the registries you need to pull
from (e.g., `.docker.io`, `.cloudflarestorage.com` for Docker Hub image layers).

## Network isolation

The sandbox container has **no direct internet access**. All traffic is routed through a Squid proxy
that only allows connections to the domains listed under `domains`. An empty `domains` list blocks
all outbound traffic.

Two Docker networks are created per run:

- `<project>-internal-<run-id>` - connects the sandbox, proxy, and optional DinD sidecar (internal,
  no external routing)
- `<project>-egress-<run-id>` - connects the proxy to the outside world

When Docker-in-Docker is enabled, sandboxeed creates per-run labeled volumes for
`/var/lib/containers` (Podman storage) and `/var/sock` (the shared API socket) inside the DinD
sidecar so they can be cleaned up reliably.

## Security considerations

sandboxeed reduces the attack surface of running untrusted or semi-trusted code, but it is **not a
complete security boundary**. Keep the following in mind:

- **Volume mounts expose host files.** Any path listed under `volumes` is directly accessible
  inside the sandbox. Sensitive files (SSH keys, API tokens, config files) mounted into the
  container can be read or exfiltrated to allowed domains. Mount with `:ro` where possible and avoid
  mounting credentials you don't need. If you mount a Git SSH key, use a dedicated key with the
  narrowest repository or project scope you can enforce instead of a broader personal key.
  The example `sandboxeed.yaml` files mount agent config directories like `~/.claude`, `~/.codex`,
  `~/.gemini`, and `~/.config/opencode` without `:ro`. This means a sandboxed agent can **read**
  those files (exposing API keys and tokens stored in them), **modify** them (corrupting your
  configuration), or **inject** content that may be executed later on the host (e.g., shell hooks,
  MCP server entries, or custom slash commands). For stronger isolation, mount a separate,
  purpose-built config directory instead of your real one, or at minimum mount with `:ro`.
  You can also run with `--read-only` to force all mounts (including the project directory) to
  read-only mode.
- **Example configs are intentionally permissive.** The files in `examples/` disable approval
  prompts and safety checks for convenience in disposable environments.
  **Do not use them outside a sandbox.**
- **Domain filtering is not bulletproof.** The Squid proxy filters by domain name, not by IP. DNS
  rebinding, tunneling over allowed domains (e.g., via a compromised CDN), or exfiltration through
  DNS itself are not prevented.
- **Docker-in-Docker capabilities.** The Podman-in-Docker sidecar runs with `SYS_ADMIN`,
  `seccomp=unconfined`, and `apparmor=unconfined` — enough for fuse-overlayfs but not enough to
  access host devices. With `--unsafe`, the sidecar runs fully privileged, which grants host kernel
  access and should only be used for testing.
- **No syscall filtering.** The sandbox container runs with Docker's default seccomp profile but no
  additional restrictions. It is not equivalent to a VM-level isolation boundary like gVisor or
  Firecracker.
- **Proxy bypass.** If the sandbox container gains the ability to manipulate its own network
  namespace or routes (e.g., via `CAP_NET_ADMIN`), it could bypass the proxy entirely. The default
  Docker capability set does not grant this, but custom images or runtime flags could.
