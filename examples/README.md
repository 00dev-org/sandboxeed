# Examples

This directory contains small example configuration files for the tools used in this repo.

## Disclaimer

All of these example configs are highly insecure outside sandboxed or otherwise disposable environments. They are meant for controlled setups only, and should not be used as-is on a normal workstation, server, or other non-sandboxed system.

## Files

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

Copy the files for the agent you want into your project root, then edit them for your project:

```bash
cp examples/codex/Dockerfile.sandbox ./Dockerfile.sandbox
cp examples/codex/sandboxeed.yaml ./sandboxeed.yaml
cp examples/codex/config.toml ./config.toml
```

These examples are intentionally project-agnostic starters. You will usually need to add your own language runtimes, package managers, SDKs, or extra allowed domains.
