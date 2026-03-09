# Examples

This directory contains small example configuration files for the tools used in this repo.

## Disclaimer

All of these example configs are highly insecure outside sandboxed or otherwise disposable environments. They are meant for controlled setups only, and should not be used as-is on a normal workstation, server, or other non-sandboxed system.

## Files

### `.sandboxeed.yaml`

A global [user config](../README.md#user-config) file that should be saved to `~/.sandboxeed.yaml`. It pre-configures volumes, environment variables, and allowed domains shared across all tools (Claude Code, Codex, Gemini, OpenCode). Use this alongside the tool-specific config files below (`settings.json`, `config.toml`, etc.), which should be saved under `~/.sandboxeed/` and mounted into the sandbox via the user config (e.g., `~/.sandboxeed/.claude/settings.json`, `~/.sandboxeed/.codex/config.toml`).

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

> **Note:** `config.toml` sets `shell_environment_policy.include_only`, which is a strict
> allowlist — Codex strips every environment variable not listed there before running shell
> commands. If a variable isn't in `include_only`, it won't be visible to anything Codex runs,
> regardless of how it was set. Make sure any environment variable you rely on (API keys, custom
> vars set via `sandboxeed.yaml`, etc.) is listed there.

### `gemini/`

Gemini CLI examples:
- `Dockerfile.sandbox` installs `@google/gemini-cli`.
- `sandboxeed.yaml` mounts Gemini settings and allows Google API domains.

### `opencode/`

OpenCode examples:
- `Dockerfile.sandbox` installs OpenCode via the official install script.
- `sandboxeed.yaml` mounts OpenCode config and allows OpenCode plus common model provider domains.

## How to use them

Copy `.sandboxeed.yaml` to your home directory as the global user config, along with any tool-specific config files:

```bash
cp examples/.sandboxeed.yaml ~/.sandboxeed.yaml

# Claude Code config
mkdir -p ~/.sandboxeed/.claude
cp examples/claude/settings.json ~/.sandboxeed/.claude/settings.json

# Codex config
mkdir -p ~/.sandboxeed/.codex
cp examples/codex/config.toml ~/.sandboxeed/.codex/config.toml
```

Then copy the project files for your agent into your project root:

```bash
cp examples/codex/Dockerfile.sandbox ./Dockerfile.sandbox
cp examples/codex/sandboxeed.yaml ./sandboxeed.yaml
```

These examples are intentionally project-agnostic starters. You will usually need to add your own language runtimes, package managers, SDKs, or extra allowed domains.
