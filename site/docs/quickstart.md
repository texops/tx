---
title: Quickstart
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

Get your first remote LaTeX build running in minutes.

## Install

<Tabs>
  <TabItem value="brew" label="Homebrew" default>

```bash
brew install texops/tap/tx
```

  </TabItem>
  <TabItem value="curl" label="curl">

```bash
curl -fsSL https://raw.githubusercontent.com/texops/tx/main/scripts/install.sh | sh
```

To download the binary into the current directory without installing:

```bash
curl -fsSL https://raw.githubusercontent.com/texops/tx/main/scripts/download.sh | sh
```

  </TabItem>
  <TabItem value="source" label="From source (requires Go)">

```bash
go install github.com/texops/tx/cmd/tx@latest
```

  </TabItem>
</Tabs>

## Authenticate

Registration is open — running `tx login` creates your account automatically.

```bash
tx login
```

A device code appears in your terminal. Open the verification page, enter the code with your email, and click the magic link sent to your inbox.

:::info
Credentials are stored in your system keyring and persist for 30 days.
:::

## Initialize a project

In your LaTeX project directory:

```bash
tx init
```

This scans for `.tex` files containing `\documentclass`, prompts you to select documents, a TeX Live version, and a compiler, then writes a [`.texops.yaml`](configuration.md) config file. Commit it to version control.

To skip the interactive prompts:

```bash
tx init --texlive 2025 --compiler pdflatex
```

## Build

```bash
tx build
```

Files are synced to TexOps, compiled remotely, and the resulting PDF is downloaded to your working directory. Only changed files are uploaded on subsequent builds.

To build a specific document:

```bash
tx build paper
```

## Next steps

- [Configuration](configuration.md) — customize your `.texops.yaml` and control which files are uploaded
- [CI/CD](ci-cd.md) — set up automated builds with API tokens
- [CLI Reference](cli.md) — full list of commands and flags
