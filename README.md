# tx

Command-line client for [TexOps](https://texops.dev) — a remote LaTeX compilation service.

## Install

```
curl -fsSL https://raw.githubusercontent.com/texops/tx/main/scripts/install.sh | sh
```

To download the binary into the current directory without installing:

```
curl -fsSL https://raw.githubusercontent.com/texops/tx/main/scripts/download.sh | sh
```

Or with `go install`:

```
go install github.com/texops/tx/cmd/tx@latest
```

## Usage

```
tx login                        # Authenticate with TexOps
tx init                         # Initialize a project in the current directory
tx build                        # Build all documents
tx build <name>                 # Build a specific document
tx build --live                 # Watch for changes and rebuild automatically
tx status                       # Show project status
tx token create [--name "CI"]   # Create an API token
tx token list                   # List API tokens
tx token delete [name]          # Delete an API token
```

### Getting started

1. Run `tx login` to authenticate.
2. In your LaTeX project directory, run `tx init` to create a `.texops.yaml` config file. This interactively prompts for TexLive version and compiler, auto-discovers `.tex` files with `\documentclass`, and lets you select which documents to build. Use `--texlive` and `--compiler` flags to skip interactive prompts.
3. Run `tx build` to compile your documents remotely. The first build creates the project on TexOps; subsequent builds use incremental file sync for speed.

### Configuration

Project settings are stored in `.texops.yaml`:

- `project_key` — unique identifier (safe to commit)
- `texlive` — TexLive version (e.g. `texlive:2025`)
- `compiler` — LaTeX compiler: `pdflatex` (default), `xelatex`, `lualatex`, `latex`, `platex`, `uplatex`
- `documents` — list of documents to build

The API URL defaults to `https://api.texops.dev` and can be overridden with `TX_API_URL` or `api_url` in `.texops.yaml`.

### Output and exit codes

Results (the build summary, `tx status` fields, token values) go to stdout; progress, the build log and errors go to stderr.

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Service or unexpected failure (network, server error, build timeout) |
| `2` | Usage error (unknown or missing command, unknown flag or document, missing input without a terminal) |
| `3` | Not authenticated, or the session or token expired or was rejected |
| `4` | `.texops.yaml` missing or invalid, or unsupported TeX Live version |
| `5` | A document failed to compile |

When documents fail for different reasons, the most severe code wins (`1`, then `3`, then `4`); `5` means every failure was a compile failure.

## License

[MIT](LICENSE)
