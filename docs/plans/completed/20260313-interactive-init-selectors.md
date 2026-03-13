# Interactive TexLive and Compiler Selection in `tx init`

## Overview
- Add interactive TUI pickers for TexLive version and compiler during `tx init`
- Reuses the existing `SelectOne` bubbletea component (no new TUI code)
- Pickers show only when flags are not explicitly passed and stdin is a TTY; non-TTY falls back to defaults
- Updates default TexLive from `texlive:2021` to `texlive:2025`

## Context (from discovery)
- Files/components involved:
  - `internal/cli/config.go` — constants, `AllowedCompilers`, `defaultCompiler`
  - `internal/cli/commands.go` — `InitCmd` struct, `Execute()`, `initProject()`
  - `internal/cli/commands_test.go` — init tests
  - `internal/cli/ui.go` — `SelectOne()` (no changes needed)
- Related patterns: `SelectDocuments()` in init flow, `SelectOne()` used by token delete
- Dependencies: `charmbracelet/bubbletea` (already in go.mod)

## Development Approach
- **Testing approach**: Regular (code first, then tests)
- Complete each task fully before moving to the next
- Make small, focused changes
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**
- Run tests after each change
- Maintain backward compatibility (explicit `--texlive` / `--compiler` flags still work)

## Progress Tracking
- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with + prefix
- Document issues/blockers with ! prefix
- Update plan if implementation deviates from original scope

## Implementation Steps

### Task 1: Add TexLive version constants

**Files:**
- Modify: `internal/cli/config.go`

- [x] Add `defaultTexlive` const with value `"texlive:2025"`
- [x] Add `TexliveVersions` var: `[]string{"texlive:2025", "texlive:2024", "texlive:2023", "texlive:2022", "texlive:2021"}`
- [x] Run tests to confirm nothing breaks: `go test ./...`

### Task 2: Wire interactive selection into `InitCmd.Execute()`

**Files:**
- Modify: `internal/cli/commands.go`

- [x] Remove `default:"texlive:2021"` from `InitCmd.Texlive` struct tag (keep empty default)
- [x] Remove `default:"pdflatex"` from `InitCmd.Compiler` struct tag (keep empty default)
- [x] In `Execute()`, before validation: if `cmd.Texlive == ""` and TTY, call `ui.SelectOne("TexLive version:", TexliveVersions)` and set `cmd.Texlive` from result
- [x] If `cmd.Texlive == ""` and non-TTY, set `cmd.Texlive = defaultTexlive`
- [x] Same pattern for `cmd.Compiler`: if empty and TTY, call `ui.SelectOne("Compiler:", AllowedCompilers)`, else set `defaultCompiler`
- [x] Run tests: `go test ./...`

### Task 3: Update existing tests and add new tests

**Files:**
- Modify: `internal/cli/commands_test.go`

- [x] Verify existing `TestInitCmd` subtests still pass (they set fields explicitly, so they should)
- [x] Add subtest: non-TTY with empty texlive/compiler gets defaults (`texlive:2025`, `pdflatex`)
- [x] Add subtest: explicit `--texlive` and `--compiler` flags skip interactive selection
- [x] Run full test suite: `go test ./...`

### Task 4: Verify acceptance criteria

- [x] Verify: `tx init` in TTY shows version picker, then compiler picker
- [x] Verify: `tx init --texlive texlive:2023 --compiler xelatex` skips pickers
- [x] Verify: non-TTY `tx init` uses defaults without prompting
- [x] Run full test suite: `go test ./...`

### Task 5: [Final] Cleanup

- [x] Run `make fmt` if available
- [x] Run `go mod tidy`
- [x] Move this plan to `docs/plans/completed/`

## Technical Details

**Flag detection approach:** Change struct tag defaults from explicit values to empty strings. Empty string after flag parsing = flag not passed = show interactive picker (TTY) or use hardcoded default (non-TTY).

**Selection flow in `Execute()`:**
```
1. Parse flags (go-flags)
2. If Texlive empty:
   a. TTY → SelectOne("TexLive version:", TexliveVersions) → set Texlive
   b. non-TTY → Texlive = defaultTexlive
3. If Compiler empty:
   a. TTY → SelectOne("Compiler:", AllowedCompilers) → set Compiler
   b. non-TTY → Compiler = defaultCompiler
4. Validate compiler (existing logic)
5. Call initProject() (existing logic)
```

**Static version list:** `texlive:2025`, `texlive:2024`, `texlive:2023`, `texlive:2022`, `texlive:2021` (newest first, default pre-selected at index 0).
