---
title: CLI Reference
---

Complete reference for all `tx` commands, flags, environment variables, and files. All commands support `--help` for usage information.

## `tx login`

Authenticate with the TexOps service using the device code flow. A one-time code is displayed and the verification URL is opened in a browser automatically when possible. After authorization completes, the session JWT is stored in the system keyring (or the credentials file as a fallback).

## `tx init`

Create a `.texops.yaml` configuration file in the current directory. Recursively discovers `.tex` files containing `\documentclass` and presents them for selection.

| Flag | Description |
|------|-------------|
| `--texlive <version>` | TeX Live distribution version, e.g. `"2025"`. Skips the interactive prompt. |
| `--compiler <name>` | LaTeX compiler (`pdflatex`, `xelatex`, `lualatex`, `latex`, `platex`, `uplatex`). Skips the interactive prompt. |
| `--main <file>` | Fallback main `.tex` file, used only when recursive discovery finds no `.tex` files containing `\documentclass`. Does not override discovered documents. Defaults to `main.tex`. |

## `tx build`

Build one or more documents defined in `.texops.yaml`. Positional arguments select documents by name; when none are given, all documents are built. On success, each PDF is downloaded to the output path defined in the config.

If `.texops.yaml` does not exist and stdout is a TTY, an interactive prompt offers to run `tx init` first. When stdout is not a TTY, the build fails with an error.

| Flag | Description |
|------|-------------|
| `--no-cache` | Rebuild without using the remote build cache. |
| `--live` | Watch for file changes and rebuild automatically. On each change, files are re-synced, the document is rebuilt, and the output PDF is rewritten in place so viewers like Skim refresh automatically. |

## `tx status`

Show authentication status including email, authentication method, and token expiry. Always exits `0`, even when not authenticated.

## `tx token create`

Create a new API token for CI pipelines or non-interactive use. The token value is displayed once and cannot be retrieved again.

| Flag | Description |
|------|-------------|
| `--name <name>` | Name for the token. Required in non-interactive mode. |
| `--expires-in <duration>` | Expiry duration: a positive integer followed by `d` (days) or `y` (years), max 10 years. Mutually exclusive with `--no-expiry`. |
| `--no-expiry` | Create a token that does not expire. Mutually exclusive with `--expires-in`. |

## `tx token list`

List all API tokens with their name, prefix, expiry, last-used date, and creation date.

## `tx token delete [name]`

Delete an API token. With a name argument, deletes the matching token after confirmation. Without a name argument in interactive mode, presents a selection list.

## `tx version`

Print the `tx` version and exit. Also available as `tx --version`.

## Authentication order

`tx` resolves credentials in this order:

1. `TX_API_TOKEN` environment variable
2. JWT in the credentials file
3. JWT in the system keyring

## Environment variables

| Variable | Description |
|----------|-------------|
| `TX_API_TOKEN` | API token for authentication. Takes priority over all other credential sources. |
| `TX_API_URL` | Override the API endpoint URL. Takes priority over the `api_url` config key. |
| `XDG_CONFIG_HOME` | When set to an absolute path, credentials are stored at `$XDG_CONFIG_HOME/texops/credentials.yaml`. |

## Files

| Path | Description |
|------|-------------|
| `.texops.yaml` | Project configuration file. |
| `.txignore` | File exclusion patterns (`.gitignore` syntax). Can appear in subdirectories. |
| `$XDG_CONFIG_HOME/texops/credentials.yaml` | Credentials file when `XDG_CONFIG_HOME` is set. |
| `~/.config/texops/credentials.yaml` | Default credentials file location. |

## Exit status

| Code | Meaning |
|------|---------|
| `0` | Success (including `tx status` when not authenticated). |
| `1` | Failure: parse errors, configuration errors, build failures, runtime errors. |
