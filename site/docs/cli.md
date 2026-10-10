---
title: CLI Reference
---

Complete reference for all `tx` commands, flags, environment variables, and files. `tx --help` gives an overview, and `tx <command> --help` describes a command with examples. To run `tx` from a coding agent or a script, see [Using tx from coding agents](agents.md).

## Global options

These flags work with every command, before or after the command name.

| Flag | Description |
|------|-------------|
| `--json` | Print exactly one JSON document to stdout, also on failure, and nothing else. Progress, the build log and error messages go to stderr as plain lines. See [JSON output](#json-output). |
| `-h`, `--help` | Show help for `tx` or for the command and exit `0`. |

`tx` never prompts without a terminal (stdin and stdout both a TTY). Instead it uses a default, or fails with a non-zero exit code when there is no safe default: usually `2` for missing input, `4` for `tx build` without `.texops.yaml`. The one exception is the selection list of `tx token delete --yes` without a name, which needs only stdout to be a TTY.

## `tx login`

Authenticate with the TexOps service using the device code flow. `tx login` prints `Open <verification URL> and enter code <CODE>` to stderr, then opens the URL in a browser when possible, with the code already filled in. After authorization completes, the session JWT is stored in the system keyring (or the credentials file as a fallback).

A person has to approve the login in a browser. In CI or from a coding agent, use [`TX_API_TOKEN`](#environment-variables) instead.

| Flag | Description |
|------|-------------|
| `--no-browser` | Print the URL and code without opening a browser. |
| `--timeout <duration>` | How long to wait for authorization, as a Go duration such as `2m` or `90s`. Defaults to the lifetime of the login code. When it runs out (or the code expires first), `tx login` exits `1`. |

Commands that need credentials exit `3` when there are none, with the message `not authenticated: ask the user to run 'tx login' in a terminal, or set TX_API_TOKEN (create one with 'tx token create')`. When the stored session has expired, the message names the date, for example `session expired on 2026-03-30; run 'tx login'`.

## `tx init`

Create a `.texops.yaml` configuration file in the current directory. Recursively discovers `.tex` files containing `\documentclass` and presents them for selection. Exits `4` if `.texops.yaml` already exists.

The supported TeX Live versions come from the service (`GET /api/distributions`); when it cannot be reached, `tx` uses its built-in list. Without a terminal (stdin and stdout), `tx init` does not prompt: it uses the newest version, `pdflatex` and every discovered document.

| Flag | Description |
|------|-------------|
| `--texlive <version>` | TeX Live distribution version, e.g. `"2025"`. Defaults to the newest. Skips the interactive prompt. Must be a version the service supports; otherwise `tx init` exits `2`, lists the supported versions and writes nothing. |
| `--compiler <name>` | LaTeX compiler (`pdflatex`, `xelatex`, `lualatex`, `latex`, `platex`, `uplatex`). Defaults to `pdflatex`. Skips the interactive prompt. Any other value exits `2`. |
| `--main <file>` | Fallback main `.tex` file, used only when recursive discovery finds no `.tex` files containing `\documentclass`. Does not override discovered documents. Defaults to `main.tex`. |

## `tx build [name...]`

Build one or more documents defined in `.texops.yaml`. Positional arguments select documents by name; when none are given, all documents are built. An unknown name exits `2`. On success, each PDF is downloaded to the output path defined in the config.

If `.texops.yaml` does not exist and both stdin and stdout are a TTY, an interactive prompt offers to run `tx init` first. Otherwise the build fails with exit code `4`.

| Flag | Description |
|------|-------------|
| `--no-cache` | Rebuild without using the remote build cache. |
| `--log=terminal\|file` | Where the LaTeX log goes. `terminal` streams it while the document compiles (to stderr, next to the progress lines) and saves nothing. `file` streams nothing and saves the full log to `.texops/logs/<doc>.log` after each build. Defaults to `file` under a coding agent (see [`TX_AGENT`](#environment-variables)) or with `--json`, and to `terminal` otherwise. |
| `--live` | Watch for file changes and rebuild automatically. On each change, files are re-synced, the document is rebuilt, and the output PDF is rewritten in place so viewers like Skim refresh automatically. Runs until interrupted with Ctrl+C, so it is meant for people, not for scripts or agents. Cannot be combined with `--json` (exit `2`). |

### Diagnostics

At the end of every build, `tx` prints a block per document with the LaTeX errors and warnings the service found:

```text
Build complete: 0 succeeded, 1 failed (4.2s)
  paper: FAILED, 1 error, 1 warning (log: .texops/logs/paper.log)
    sec/intro.tex:3: error: Undefined control sequence.
    sec/intro.tex:1: warning: Citation `missing' undefined
```

Each diagnostic is `file:line: severity: message`, leaving out what the service could not determine. File paths are relative to the project root. The `(log: ...)` part appears only when the log was saved. Successful documents list their warnings too. When the service found more diagnostics than it sent, a last line says that more are in the log.

With `--log=file`, a failed build also prints the last 20 lines of the log to stderr. The first time a log is saved, `tx` creates `.texops/.gitignore` containing `*`, so the directory never shows up in Git. `.texops/` is never uploaded.

## `tx status`

Show authentication status including email, authentication method, and token expiry. Exits `3` when not authenticated or when the session or token has expired or was rejected. When the rejected credential came from `TX_API_TOKEN`, the message says so (`TX_API_TOKEN was rejected (invalid, expired or deleted); set a valid token, or unset it to use your 'tx login' session`), since `tx login` alone does not help while the variable is set.

## `tx token create [name]`

Create a new API token for CI pipelines or non-interactive use. The token value is printed to stdout once and cannot be retrieved again. The name can be given as an argument (`tx token create ci`) or with `--name`; giving two different names exits `2`.

| Flag | Description |
|------|-------------|
| `--name <name>` | Name for the token, the same as the name argument. Required in non-interactive mode. |
| `--expires-in <duration>` | Expiry duration: a positive integer followed by `d` (days) or `y` (years), max 10 years (`3650d` or `10y`). Mutually exclusive with `--no-expiry`. |
| `--no-expiry` | Create a token that does not expire. Mutually exclusive with `--expires-in`. |

Without a terminal, a name and one of `--expires-in` or `--no-expiry` are required; otherwise `tx token create` exits `2`.

## `tx token list`

List all API tokens with their name, prefix, expiry, last-used date, and creation date.

## `tx token delete [name]`

Delete an API token. With a name argument, deletes the matching token after confirmation. Without a name argument, when stdout is a TTY and either stdin is a TTY or `--yes` is given, presents a selection list. An unknown name exits `2`.

Without a terminal (stdin and stdout), or with `--json`, `tx` cannot ask for confirmation: it exits `2` without deleting anything unless `--yes` is given.

| Flag | Description |
|------|-------------|
| `-y`, `--yes` | Delete without asking for confirmation, also on a terminal. |

## `tx version`

Print the `tx` version and exit. Also available as `tx --version`.

## JSON output

With `--json`, stdout carries exactly one JSON document followed by a newline, whatever the outcome. The exit code is the same as without `--json`. A human-readable error line still goes to stderr.

When a command fails before it has a result (bad flags, no credentials, a missing `.texops.yaml`, a network error), the document is an error:

```json
{"error": {"code": "not_authenticated", "message": "not authenticated: ask the user to run 'tx login' in a terminal, or set TX_API_TOKEN (create one with 'tx token create')"}}
```

| Field | Description |
|-------|-------------|
| `error.code` | One of `internal`, `network`, `timeout`, `usage`, `not_authenticated`, `config`, `build_failed`. Matches the [exit status](#exit-status). |
| `error.message` | The same message that is printed to stderr. |

### `tx build --json`

A build that ran prints a build document, also when documents failed:

```json
{
  "ok": false,
  "duration_ms": 4210,
  "documents": [
    {
      "name": "paper",
      "main": "paper.tex",
      "status": "failed",
      "reason": "latex_error",
      "output": null,
      "log": ".texops/logs/paper.log",
      "build_id": "bld_8f3k2",
      "duration_ms": 3120,
      "errors": [
        {"severity": "error", "file": "sec/intro.tex", "line": 3, "message": "Undefined control sequence.", "context": "\\badmacro"}
      ],
      "warnings": [
        {"severity": "warning", "kind": "undefined_citation", "file": "sec/intro.tex", "line": 1, "message": "Citation `missing' undefined"}
      ],
      "truncated": false
    }
  ]
}
```

| Field | Description |
|-------|-------------|
| `ok` | `true` when every document built. |
| `duration_ms` | Wall time of the whole command. |
| `documents[].name` | Document name from `.texops.yaml`. |
| `documents[].main` | Main file, relative to the project root. |
| `documents[].status` | `succeeded` or `failed`. |
| `documents[].reason` | Why the document failed, `null` on success: `latex_error` or `no_pdf` (the document did not compile), `timeout` (the build hit the time limit), `internal` (the service failed), `network`, `auth`, `sync` (uploading the project failed) or `config`. |
| `documents[].output` | PDF path relative to the project root, `null` on failure. |
| `documents[].log` | Path of the saved log (`--log=file`), else `null`. |
| `documents[].build_id` | Service build id, or `null`. |
| `documents[].duration_ms` | Time spent on this document. |
| `documents[].errors` | Error diagnostics (may be empty). |
| `documents[].warnings` | Warning diagnostics (may be empty). |
| `documents[].truncated` | `true` when the service found more diagnostics than it sent. |

Each diagnostic has these fields; the optional ones are left out when unknown:

| Field | Description |
|-------|-------------|
| `severity` | `error` or `warning`. |
| `kind` | Warnings only: `undefined_citation`, `undefined_reference`, `duplicate_label` or `rerun`. |
| `file` | Source file relative to the project root. |
| `line` | Line number. |
| `message` | The LaTeX message. |
| `context` | Errors only: the source text TeX showed after the line number. |

### `tx status --json`

```json
{"authenticated": true, "email": "ada@example.com", "method": "jwt", "source": "keyring", "expires_at": "2026-11-02T10:00:00Z"}
```

| Field | Description |
|-------|-------------|
| `authenticated` | `true` when logged in. When not, it is `false`, the other fields are left out, and the exit code is `3`. |
| `email` | Account email, or `null`. |
| `method` | `jwt` (a `tx login` session) or `api_token`. |
| `source` | Where the credentials came from: `env` (`TX_API_TOKEN`), `file` or `keyring`. |
| `expires_at` | RFC 3339 expiry, or `null` when the credentials do not expire. |

### `tx init --json`

```json
{"config": ".texops.yaml", "texlive": "2025", "compiler": "pdflatex", "documents": [{"name": "paper", "main": "paper.tex"}]}
```

| Field | Description |
|-------|-------------|
| `config` | Always `.texops.yaml`. |
| `texlive` | TeX Live version written to the config. |
| `compiler` | Compiler written to the config. |
| `documents[]` | `name`, `main` and, for a document in a subdirectory, `directory`. |

### `tx login --json`

```json
{"authenticated": true}
```

| Field | Description |
|-------|-------------|
| `authenticated` | Always `true`; a failed login prints an error document instead. |

### `tx token list --json`

An array, empty when there are no tokens:

```json
[{"name": "ci", "prefix": "tx_3f9a", "expires_at": "2027-01-01T00:00:00Z", "last_used_at": null, "created_at": "2026-10-01T09:30:00Z"}]
```

| Field | Description |
|-------|-------------|
| `name` | Token name. |
| `prefix` | First characters of the token value. |
| `expires_at` | RFC 3339, or `null` when the token does not expire. |
| `last_used_at` | RFC 3339, or `null` when never used. |
| `created_at` | RFC 3339. |

### `tx token create --json`

```json
{"name": "ci", "token": "tx_3f9a...", "expires_at": "2027-01-01T00:00:00Z"}
```

| Field | Description |
|-------|-------------|
| `name` | Token name. |
| `token` | The token value. It is shown only once. |
| `expires_at` | RFC 3339, or `null` when the token does not expire. |

### `tx token delete --json`

```json
{"deleted": "ci"}
```

| Field | Description |
|-------|-------------|
| `deleted` | Name of the deleted token. |

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
| `TX_AGENT` | Set to `none` to turn off coding agent detection. |
| `AI_AGENT`, `CLAUDECODE`, `CODEX_THREAD_ID`, `CURSOR_AGENT`, `GEMINI_CLI` | Set by coding agents. When one is present (and `TX_AGENT` is not `none`), `tx build` defaults to `--log=file`, and requests name the agent in the `User-Agent` header. |
| `XDG_CONFIG_HOME` | When set to an absolute path, credentials are stored at `$XDG_CONFIG_HOME/texops/credentials.yaml`. |

## Files

| Path | Description |
|------|-------------|
| `.texops.yaml` | Project configuration file. |
| `.txignore` | File exclusion patterns (`.gitignore` syntax). Can appear in subdirectories. |
| `.texops/logs/<doc>.log` | Full LaTeX log of the last build of a document, saved with `--log=file`. |
| `.texops/.gitignore` | Created with the first saved log; contains `*` so Git ignores `.texops/`. |
| `$XDG_CONFIG_HOME/texops/credentials.yaml` | Credentials file when `XDG_CONFIG_HOME` is set. |
| `~/.config/texops/credentials.yaml` | Default credentials file location. |

## Output streams

Results go to stdout: the build summary, the `tx status` fields, the `tx token list` table and the token value printed by `tx token create`. Progress, the streamed build log, hints and error messages go to stderr. On a terminal both appear on screen; in a pipe, stdout carries only the results. Each error is printed once, to stderr. With `--json`, stdout carries only the JSON document.

## Exit status

| Code | Meaning | JSON `error.code` |
|------|---------|-------------------|
| `0` | Success, including `--help` and `tx version`. | |
| `1` | Service or unexpected failure: network errors, server errors (5xx), sync or upload failures, a build that hit the time limit or failed inside the service. | `internal`, `network`, `timeout` |
| `2` | Usage error: no command (`tx` alone prints the help to stderr), unknown command, missing subcommand or unknown flag, bad flag value (including an unsupported `--texlive` or `--compiler`), unknown document name, `--json` with `--live`, missing required input when there is no terminal to prompt on, `tx token delete` without `--yes` when there is no terminal to confirm on. | `usage` |
| `3` | Not authenticated, or the session or API token has expired or was rejected. | `not_authenticated` |
| `4` | Project configuration error: `.texops.yaml` is missing or invalid, or its TeX Live version is not supported. | `config` |
| `5` | A document failed to compile (LaTeX errors, or no PDF was produced). | `build_failed` |

When several documents fail for different reasons, `tx build` exits with the most severe code: `1` if any failure was a service failure, otherwise `3` for an authentication failure, then `4` for a configuration failure. It exits `5` only when every failure is a compile failure.
