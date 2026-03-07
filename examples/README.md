# Examples

This directory contains small example configuration files for the tools used in this repo.

## Disclaimer

All of these example configs are highly insecure outside sandboxed or otherwise disposable environments. They are meant for controlled setups only, and should not be used as-is on a normal workstation, server, or other non-sandboxed system.

## Files

### `git.conf`

SSH config snippet for connecting to GitHub through an HTTP proxy with `corkscrew`. It also disables host key checks, which is convenient for constrained environments but less strict from a security perspective.

### `claude/`

Claude Code examples:
- `Dockerfile.sandbox` installs the Claude Code CLI.
- `sandboxeed.yaml` mounts Claude settings and allows Anthropic domains.
- `settings.json` is a minimal permissive Claude settings example for disposable sandboxes.

### `codex/`

Codex CLI examples:
- `Dockerfile.sandbox` installs `@openai/codex`.
- `sandboxeed.yaml` mounts Codex config and allows OpenAI domains.
- `config.toml` is a minimal permissive Codex configuration example.

### `gemini/`

Gemini CLI examples:
- `Dockerfile.sandbox` installs `@google/gemini-cli`.
- `sandboxeed.yaml` mounts Gemini settings and allows Google API domains.

### `opencode/`

OpenCode examples:
- `Dockerfile.sandbox` installs OpenCode via the official install script.
- `sandboxeed.yaml` mounts OpenCode config and allows OpenCode plus common model provider domains.

## How to use them

Copy the files for the agent you want into your project root, then edit them for your project:

```bash
cp examples/codex/Dockerfile.sandbox ./Dockerfile.sandbox
cp examples/codex/sandboxeed.yaml ./sandboxeed.yaml
cp examples/codex/config.toml ./config.toml
```

These examples are intentionally project-agnostic starters. You will usually need to add your own language runtimes, package managers, SDKs, or extra allowed domains.
