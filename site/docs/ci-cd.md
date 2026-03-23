---
title: CI/CD
---

TexOps supports headless builds in CI pipelines. Commit your `.texops.yaml` to the repository, create an API token, and set it as an environment variable.

## Create an API token

```bash
tx token create --name "GitHub Actions" --expires-in 90d
```

:::warning
The token is displayed once and cannot be retrieved again.
:::

Copy it to your CI provider's secret storage.

The `--expires-in` flag accepts a positive integer followed by `d` (days) or `y` (years), up to 10 years. Use `--no-expiry` for tokens that don't expire. In non-interactive mode, one of `--expires-in` or `--no-expiry` is required.

## Set `TX_API_TOKEN`

The `TX_API_TOKEN` environment variable takes priority over all other credential sources (keyring and credentials file). Set it to your API token in your CI configuration.

## GitHub Actions

```yaml title=".github/workflows/build.yml"
name: Build LaTeX
on: [push]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Install tx
        run: curl -fsSL https://raw.githubusercontent.com/texops/tx/main/scripts/install.sh | sh

      - name: Build paper
        env:
          TX_API_TOKEN: ${{ secrets.TEXOPS_TOKEN }}
        run: tx build
```

Other install methods:

- **Download binary to the current directory** (no install): `curl -fsSL https://raw.githubusercontent.com/texops/tx/main/scripts/download.sh | sh`
- **From source** (requires Go): `go install github.com/texops/tx/cmd/tx@latest`

## Manage tokens

List all tokens with their prefix, expiry, and last-used date:

```bash
tx token list
```

Delete a token by name:

```bash
tx token delete "GitHub Actions"
```
