---
title: Using tx from coding agents
---

Coding agents such as Claude Code, Codex, Cursor and Gemini CLI can build a LaTeX project with `tx`, read the errors, fix the sources and build again. `tx` never prompts without a terminal, reports results as JSON with `--json`, and uses a distinct exit code for each kind of failure.

## Authentication

`tx login` needs a person to approve the login in a browser, so an agent should not run it. Instead, either:

- set `TX_API_TOKEN` to an API token (create one with `tx token create --name <name> --expires-in 90d`), or
- ask the user to run `tx login` in their own terminal. The session is stored in the system keyring, and later `tx` commands from the agent use it.

`tx status --json` checks the credentials. It exits `3` when there are none or they have expired, and the message says what to do:

```text
not authenticated: ask the user to run 'tx login' in a terminal, or set TX_API_TOKEN (create one with 'tx token create')
```

## The build loop

1. Run `tx build --json` (or `tx build <name> --json` for one document).
2. Parse stdout. It is always exactly one JSON document; progress and messages go to stderr.
3. For each document with `"status": "failed"`, read `errors`: each one names the `file` and `line` to fix.
4. Edit the sources and go back to step 1.

A failed build prints a document like this:

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
      "warnings": [],
      "truncated": false
    }
  ]
}
```

| Field | What to do with it |
|-------|--------------------|
| `ok` | `true`: every PDF was built and downloaded. |
| `documents[].reason` | `latex_error` or `no_pdf`: fix the sources. Anything else is not a LaTeX problem (see the exit codes below). |
| `documents[].errors` | The LaTeX errors with `file`, `line`, `message` and, when TeX showed it, the source `context`. |
| `documents[].warnings` | Undefined citations and references, duplicate labels, rerun hints. Worth fixing after the errors. |
| `documents[].log` | The full LaTeX log, for errors the diagnostics do not explain. |
| `documents[].output` | The PDF path when the document built. |

When the command fails before building (no credentials, no `.texops.yaml`, a bad flag), stdout carries an error document instead, and `error.code` says why:

```json
{"error": {"code": "config", "message": "no project config found; run `tx init` to set up your project"}}
```

Every field is described in the [CLI reference](cli.md#json-output).

## Logs

When `tx` detects a coding agent, `tx build` saves the full LaTeX log to `.texops/logs/<doc>.log` instead of streaming it, and prints only the diagnostics and, on failure, the last 20 lines of the log. `--json` does the same. `.texops/` ignores itself in Git and is never uploaded.

`tx` recognizes an agent by `AI_AGENT`, `CLAUDECODE`, `CODEX_THREAD_ID`, `CURSOR_AGENT` or `GEMINI_CLI` in the environment. Set `TX_AGENT=none` to turn detection off, or pass `--log=stdout` or `--log=file` to choose explicitly.

## Exit codes

| Code | Meaning | What to do |
|------|---------|------------|
| `0` | Success | Done. |
| `1` | Service or network failure, or the build hit the time limit | Retry later; do not edit the sources because of it. |
| `2` | Usage error | Fix the command line. |
| `3` | Not authenticated | Ask the user to run `tx login`, or set `TX_API_TOKEN`. |
| `4` | `.texops.yaml` missing or invalid, or unsupported TeX Live version | Run `tx init`, or fix `.texops.yaml`. |
| `5` | LaTeX errors | Read `errors`, fix the sources, rebuild. |

## Things to avoid

- `tx build --live` runs until it is interrupted. It is for people watching a PDF viewer, not for agents, and it cannot be combined with `--json`.
- `tx token delete` asks for confirmation on a terminal and refuses without one. Pass `--yes` only when the user asked for that token to be deleted.
- `tx init` without flags picks the newest TeX Live version and `pdflatex`. Pass `--texlive` and `--compiler` when the project needs something else.

## Snippet for your repository

Add this to the `AGENTS.md` or `CLAUDE.md` of a LaTeX repository that uses TexOps:

```markdown
## Building the PDF

- Build with `tx build --json`. Stdout is one JSON document; for each failed
  document, fix the `errors` (each has `file` and `line`) and build again.
- Exit codes: 0 ok, 1 service error (retry, don't edit), 2 usage, 3 not logged
  in (ask me to run `tx login`), 4 `.texops.yaml` problem, 5 LaTeX errors.
- The full log of the last build is in `.texops/logs/<document>.log`.
- Never run `tx login`, `tx build --live` or `tx token delete`.
```
