package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

type errorJSON struct {
	Error errorBodyJSON `json:"error"`
}

type errorBodyJSON struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeErrorJSON(ui *UI, err error) {
	_ = ui.WriteJSON(errorJSON{Error: errorBodyJSON{Code: AsExitError(err).Kind, Message: err.Error()}})
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type statusResult struct {
	Authenticated bool    `json:"authenticated"`
	Email         *string `json:"email"`
	Method        string  `json:"method"`
	Source        string  `json:"source"`
	ExpiresAt     *string `json:"expires_at"`
}

func renderStatus(ui *UI, res statusResult) error {
	if ui.JSON() {
		return ui.WriteJSON(res)
	}
	ui.Success("Authenticated")
	if res.Email != nil {
		ui.Log(fmt.Sprintf("Email:   %s", *res.Email))
	}
	methodLabel := res.Method
	if res.Method == "api_token" {
		methodLabel = "API token"
	}
	ui.Log(fmt.Sprintf("Method:  %s", methodLabel))
	ui.Log("Expires: " + formatDatePtr(res.ExpiresAt, "never"))
	return nil
}

type initResult struct {
	Config     string         `json:"config"`
	Texlive    string         `json:"texlive"`
	Compiler   string         `json:"compiler"`
	Documents  []initDocument `json:"documents"`
	discovered bool
}

type initDocument struct {
	Name      string `json:"name"`
	Main      string `json:"main"`
	Directory string `json:"directory,omitempty"`
}

func renderInit(ui *UI, res initResult) error {
	if ui.JSON() {
		return ui.WriteJSON(res)
	}
	if res.discovered {
		ui.Success(fmt.Sprintf("Created .texops.yaml with %d document(s)", len(res.Documents)))
	} else {
		ui.Success("Created .texops.yaml")
	}
	return nil
}

type buildJSON struct {
	OK         bool           `json:"ok"`
	DurationMS int64          `json:"duration_ms"`
	Documents  []buildDocJSON `json:"documents"`
}

type buildDocJSON struct {
	Name       string       `json:"name"`
	Main       string       `json:"main"`
	Status     string       `json:"status"`
	Reason     *string      `json:"reason"`
	Output     *string      `json:"output"`
	Log        *string      `json:"log"`
	BuildID    *string      `json:"build_id"`
	DurationMS int64        `json:"duration_ms"`
	Errors     []Diagnostic `json:"errors"`
	Warnings   []Diagnostic `json:"warnings"`
	Truncated  bool         `json:"truncated"`
}

func renderBuild(ui *UI, results []docResult, elapsed time.Duration) error {
	if ui.JSON() {
		for _, r := range results {
			for _, line := range docSummaryLines(r) {
				ui.Log(line)
			}
		}
		return ui.WriteJSON(buildResultJSON(results, elapsed))
	}
	succeeded, failed := 0, 0
	for _, r := range results {
		if r.Success {
			succeeded++
		} else {
			failed++
		}
	}
	ui.Gap()
	ui.Result(fmt.Sprintf("Build complete: %d succeeded, %d failed (%.1fs)", succeeded, failed, elapsed.Seconds()))
	for _, r := range results {
		for _, line := range docSummaryLines(r) {
			ui.Log(line)
		}
	}
	return nil
}

// docSummaryLines is a document's line in the build summary followed by one
// indented line per diagnostic.
func docSummaryLines(r docResult) []string {
	var head string
	switch {
	case r.Success:
		head = fmt.Sprintf("  %s => %s", r.Name, filepath.ToSlash(r.Output))
		if len(r.Diagnostics) > 0 {
			head += ", " + diagnosticCounts(r.Diagnostics)
		}
	case len(r.Diagnostics) > 0:
		head = fmt.Sprintf("  %s: FAILED, %s", r.Name, diagnosticCounts(r.Diagnostics))
	case r.Reason != "":
		head = fmt.Sprintf("  %s: FAILED (%s)", r.Name, r.Reason)
	default:
		head = fmt.Sprintf("  %s: FAILED", r.Name)
	}
	if r.Log != "" {
		head += fmt.Sprintf(" (log: %s)", filepath.ToSlash(r.Log))
	}
	lines := []string{head}
	for _, d := range r.Diagnostics {
		lines = append(lines, "    "+formatDiagnostic(d))
	}
	if r.Truncated {
		lines = append(lines, "    (more diagnostics were found; see the log)")
	}
	return lines
}

func diagnosticCounts(diags []Diagnostic) string {
	errs := 0
	for _, d := range diags {
		if d.Severity == "error" {
			errs++
		}
	}
	return plural(errs, "error") + ", " + plural(len(diags)-errs, "warning")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// formatDiagnostic renders a diagnostic as "file:line: severity: message",
// leaving out the parts the server did not send.
func formatDiagnostic(d Diagnostic) string {
	var b strings.Builder
	switch {
	case d.File != "" && d.Line > 0:
		fmt.Fprintf(&b, "%s:%d: ", d.File, d.Line)
	case d.File != "":
		b.WriteString(d.File + ": ")
	}
	if d.Severity != "" {
		b.WriteString(d.Severity + ": ")
	}
	b.WriteString(d.Message)
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, b.String())
}

func buildResultJSON(results []docResult, elapsed time.Duration) buildJSON {
	out := buildJSON{OK: true, DurationMS: elapsed.Milliseconds(), Documents: make([]buildDocJSON, 0, len(results))}
	for _, r := range results {
		doc := buildDocJSON{
			Name:       r.Name,
			Main:       filepath.ToSlash(r.Main),
			Status:     "succeeded",
			Log:        nullable(filepath.ToSlash(r.Log)),
			BuildID:    nullable(r.BuildID),
			DurationMS: r.Duration.Milliseconds(),
			Errors:     []Diagnostic{},
			Warnings:   []Diagnostic{},
			Truncated:  r.Truncated,
		}
		for _, d := range r.Diagnostics {
			if d.Severity == "error" {
				doc.Errors = append(doc.Errors, d)
			} else {
				doc.Warnings = append(doc.Warnings, d)
			}
		}
		if r.Success {
			doc.Output = nullable(filepath.ToSlash(r.Output))
		} else {
			out.OK = false
			doc.Status = "failed"
			reason := r.Reason
			if reason == "" {
				reason = failureReason(r.Err, "internal")
			}
			doc.Reason = &reason
		}
		out.Documents = append(out.Documents, doc)
	}
	return out
}

// failureReason names why a document failed when the server gave no reason.
func failureReason(err error, fallback string) string {
	switch AsExitError(err).Kind {
	case KindNotAuthenticated:
		return "auth"
	case KindNetwork:
		return "network"
	case KindConfig:
		return "config"
	}
	return fallback
}

type tokenListEntry struct {
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	ExpiresAt  *string `json:"expires_at"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  *string `json:"created_at"`
}

func renderTokenList(ui *UI, tokens []APITokenListItem) error {
	if ui.JSON() {
		entries := make([]tokenListEntry, 0, len(tokens))
		for _, tok := range tokens {
			entries = append(entries, tokenListEntry{
				Name:       tok.Name,
				Prefix:     tok.Prefix,
				ExpiresAt:  tok.ExpiresAt,
				LastUsedAt: tok.LastUsedAt,
				CreatedAt:  nullable(tok.CreatedAt),
			})
		}
		return ui.WriteJSON(entries)
	}
	if len(tokens) == 0 {
		ui.DimInfo("No tokens found. Create one with 'tx token create --name <name>'.")
		return nil
	}
	nameWidth := len("NAME")
	for _, tok := range tokens {
		nameWidth = max(nameWidth, utf8.RuneCountInString(tok.Name))
	}
	padName := func(name string) string {
		return name + strings.Repeat(" ", nameWidth-utf8.RuneCountInString(name))
	}
	ui.Log(fmt.Sprintf("%s %-12s %-14s %-14s %-12s", padName("NAME"), "PREFIX", "EXPIRES", "LAST USED", "CREATED"))
	for _, tok := range tokens {
		expires := formatDatePtr(tok.ExpiresAt, "never")
		lastUsed := formatDatePtr(tok.LastUsedAt, "never")
		created := formatDate(tok.CreatedAt)
		ui.Log(fmt.Sprintf("%s %-12s %-14s %-14s %-12s", padName(tok.Name), tok.Prefix, expires, lastUsed, created))
	}
	return nil
}

type tokenCreateResult struct {
	Name      string  `json:"name"`
	Token     string  `json:"token"`
	ExpiresAt *string `json:"expires_at"`
}

func renderTokenCreate(ui *UI, res tokenCreateResult) error {
	if ui.JSON() {
		return ui.WriteJSON(res)
	}
	ui.Gap()
	ui.Result(res.Token)
	ui.Gap()
	ui.DimInfo("This token won't be shown again. Copy it now.")
	ui.DimInfo("Expires: " + formatDatePtr(res.ExpiresAt, "never"))
	return nil
}

type tokenDeleteResult struct {
	Deleted string `json:"deleted"`
}

func renderTokenDelete(ui *UI, res tokenDeleteResult) error {
	if ui.JSON() {
		return ui.WriteJSON(res)
	}
	return nil
}

type loginResult struct {
	Authenticated bool `json:"authenticated"`
}

func renderLogin(ui *UI, res loginResult) error {
	if ui.JSON() {
		return ui.WriteJSON(res)
	}
	ui.Success("Logged in successfully")
	return nil
}
