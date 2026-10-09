package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

// DetectAgent returns the name of the coding agent tx runs under, or "" when none is detected.
func DetectAgent(getenv func(string) string) string {
	if getenv("TX_AGENT") == "none" {
		return ""
	}
	if name, _, _ := strings.Cut(getenv("AI_AGENT"), "_"); name != "" {
		return name
	}
	if getenv("CLAUDECODE") == "1" {
		return "claude-code"
	}
	if getenv("CODEX_THREAD_ID") != "" {
		return "codex"
	}
	if getenv("CURSOR_AGENT") != "" {
		return "cursor"
	}
	if getenv("GEMINI_CLI") != "" {
		return "gemini-cli"
	}
	return ""
}

// UserAgent formats the User-Agent header value sent with every request.
func UserAgent(version, agent string) string {
	ua := fmt.Sprintf("tx/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH)
	if agent = sanitizeUAToken(agent); agent != "" {
		ua += " agent/" + agent
	}
	return ua
}

func sanitizeUAToken(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune(".-+", r):
			return r
		default:
			return -1
		}
	}, s)
}

var appVersion atomic.Value

// SetVersion sets the tx version reported in the User-Agent header.
func SetVersion(v string) {
	appVersion.Store(v)
}

func currentUserAgent() string {
	v, _ := appVersion.Load().(string)
	if v == "" {
		v = "dev"
	}
	return UserAgent(v, DetectAgent(os.Getenv))
}
