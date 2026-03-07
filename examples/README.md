# Examples

This directory contains small example configuration files for the tools used in this repo.

## Disclaimer

All of these example configs are highly insecure outside sandboxed or otherwise disposable environments. They are meant for controlled setups only, and should not be used as-is on a normal workstation, server, or other non-sandboxed system.

## Files

### `git.conf`

SSH config snippet for connecting to GitHub through an HTTP proxy with `corkscrew`. It also disables host key checks, which is convenient for constrained environments but less strict from a security perspective.

### `codex/config.toml`

Example Codex configuration. It sets a pragmatic personality, chooses the `gpt-5.4` model with medium reasoning effort, disables approval prompts, marks `/workspace` as trusted, and hides the full-access warning.

### `claude/settings.json`

Example Claude settings. It allows Bash commands by default, disables interactive permission prompts, runs a custom status line script, and selects the `haiku` model.
