package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

var agentEnvVars = []string{"TX_AGENT", "AI_AGENT", "CLAUDECODE", "CODEX_THREAD_ID", "CURSOR_AGENT", "GEMINI_CLI"}

func envFrom(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func setAgentEnv(t *testing.T, kv ...string) {
	t.Helper()
	for _, name := range agentEnvVars {
		t.Setenv(name, "")
	}
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
}

func setVersion(t *testing.T, v string) {
	t.Helper()
	cli.SetVersion(v)
	t.Cleanup(func() { cli.SetVersion("") })
}

func platform() string {
	return fmt.Sprintf("(%s; %s)", runtime.GOOS, runtime.GOARCH)
}

func TestDetectAgent(t *testing.T) {
	t.Run("no variables", func(t *testing.T) {
		assert.Empty(t, cli.DetectAgent(envFrom(nil)))
	})

	t.Run("TX_AGENT=none disables detection", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{
			"TX_AGENT":        "none",
			"AI_AGENT":        "amp",
			"CLAUDECODE":      "1",
			"CODEX_THREAD_ID": "thr_1",
			"CURSOR_AGENT":    "1",
			"GEMINI_CLI":      "1",
		}))
		assert.Empty(t, got)
	})

	t.Run("TX_AGENT with another value does not disable detection", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"TX_AGENT": "yes", "CLAUDECODE": "1"}))
		assert.Equal(t, "claude-code", got)
	})

	t.Run("AI_AGENT is used up to the first underscore", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"AI_AGENT": "amp_1.2.3_extra"}))
		assert.Equal(t, "amp", got)
	})

	t.Run("AI_AGENT without an underscore is used whole", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"AI_AGENT": "opencode"}))
		assert.Equal(t, "opencode", got)
	})

	t.Run("AI_AGENT starting with an underscore falls through", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"AI_AGENT": "_x", "CLAUDECODE": "1"}))
		assert.Equal(t, "claude-code", got)
	})

	t.Run("AI_AGENT wins over agent-specific variables", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"AI_AGENT": "amp", "CLAUDECODE": "1", "CODEX_THREAD_ID": "thr_1"}))
		assert.Equal(t, "amp", got)
	})

	t.Run("CLAUDECODE=1 is claude-code", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"CLAUDECODE": "1"}))
		assert.Equal(t, "claude-code", got)
	})

	t.Run("CLAUDECODE other than 1 is ignored", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"CLAUDECODE": "0"}))
		assert.Empty(t, got)
	})

	t.Run("CLAUDECODE wins over CODEX_THREAD_ID", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"CLAUDECODE": "1", "CODEX_THREAD_ID": "thr_1"}))
		assert.Equal(t, "claude-code", got)
	})

	t.Run("CODEX_THREAD_ID is codex", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"CODEX_THREAD_ID": "thr_1"}))
		assert.Equal(t, "codex", got)
	})

	t.Run("CURSOR_AGENT is cursor", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"CURSOR_AGENT": "1"}))
		assert.Equal(t, "cursor", got)
	})

	t.Run("GEMINI_CLI is gemini-cli", func(t *testing.T) {
		got := cli.DetectAgent(envFrom(map[string]string{"GEMINI_CLI": "1"}))
		assert.Equal(t, "gemini-cli", got)
	})
}

func TestUserAgent(t *testing.T) {
	t.Run("without an agent", func(t *testing.T) {
		assert.Equal(t, "tx/1.2.3 "+platform(), cli.UserAgent("1.2.3", ""))
	})

	t.Run("with an agent", func(t *testing.T) {
		assert.Equal(t, "tx/1.2.3 "+platform()+" agent/claude-code", cli.UserAgent("1.2.3", "claude-code"))
	})

	t.Run("agent name is reduced to token characters", func(t *testing.T) {
		assert.Equal(t, "tx/1.2.3 "+platform()+" agent/myagent", cli.UserAgent("1.2.3", "my agent\r\n"))
	})

	t.Run("agent name without token characters is dropped", func(t *testing.T) {
		assert.Equal(t, "tx/1.2.3 "+platform(), cli.UserAgent("1.2.3", " /;"))
	})
}

type uaRecorder struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []string
}

func newUARecorder(t *testing.T, handler http.HandlerFunc) *uaRecorder {
	t.Helper()
	rec := &uaRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.requests = append(rec.requests, fmt.Sprintf("%s %s User-Agent: %q", r.Method, r.URL.Path, r.UserAgent()))
		rec.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (rec *uaRecorder) log() string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return strings.Join(rec.requests, "\n")
}

func TestUserAgentHeader(t *testing.T) {
	whoami := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"user_id": "usr_1", "auth_method": "api_token"})
	}
	pdf := func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("%PDF-1.4 test"))
	}

	t.Run("API request carries version, platform and agent", func(t *testing.T) {
		setAgentEnv(t, "CLAUDECODE", "1")
		setVersion(t, "1.2.3")
		rec := newUARecorder(t, whoami)

		api := cli.NewAPIClient(rec.srv.URL, "test-token")
		api.SetHTTPClient(rec.srv.Client())
		_, err := api.Whoami()
		require.NoError(t, err, rec.log())

		assert.Equal(t, fmt.Sprintf("GET /auth/whoami User-Agent: %q", "tx/1.2.3 "+platform()+" agent/claude-code"), rec.log())
	})

	t.Run("unauthenticated API request carries the agent", func(t *testing.T) {
		setAgentEnv(t, "GEMINI_CLI", "1")
		setVersion(t, "1.2.3")
		rec := newUARecorder(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, map[string]any{"device_code": "dev_1", "user_code": "ABCD", "verification_url": "https://example.com", "expires_in": 60})
		})

		api := cli.NewUnauthenticatedAPIClient(rec.srv.URL)
		api.SetHTTPClient(rec.srv.Client())
		_, err := api.RequestDeviceCode()
		require.NoError(t, err, rec.log())

		assert.Equal(t, fmt.Sprintf("POST /auth/device-code User-Agent: %q", "tx/1.2.3 "+platform()+" agent/gemini-cli"), rec.log())
	})

	t.Run("instance request carries version, platform and agent", func(t *testing.T) {
		setAgentEnv(t, "CODEX_THREAD_ID", "thr_1")
		setVersion(t, "1.2.3")
		rec := newUARecorder(t, pdf)

		inst := cli.NewInstanceClient(rec.srv.URL, "instance-jwt")
		inst.SetHTTPClient(rec.srv.Client())
		err := inst.DownloadPDF(t.Context(), "prj_test", "bld_1", t.TempDir()+"/out.pdf")
		require.NoError(t, err, rec.log())

		assert.Equal(t, fmt.Sprintf("GET /projects/prj_test/builds/bld_1/output User-Agent: %q", "tx/1.2.3 "+platform()+" agent/codex"), rec.log())
	})

	t.Run("TX_AGENT=none omits the agent", func(t *testing.T) {
		setAgentEnv(t, "TX_AGENT", "none", "CLAUDECODE", "1")
		setVersion(t, "1.2.3")
		rec := newUARecorder(t, pdf)

		inst := cli.NewInstanceClient(rec.srv.URL, "instance-jwt")
		inst.SetHTTPClient(rec.srv.Client())
		err := inst.DownloadPDF(t.Context(), "prj_test", "bld_1", t.TempDir()+"/out.pdf")
		require.NoError(t, err, rec.log())

		assert.Equal(t, fmt.Sprintf("GET /projects/prj_test/builds/bld_1/output User-Agent: %q", "tx/1.2.3 "+platform()), rec.log())
	})

	t.Run("version defaults to dev", func(t *testing.T) {
		setAgentEnv(t)
		setVersion(t, "")
		rec := newUARecorder(t, whoami)

		api := cli.NewAPIClient(rec.srv.URL, "test-token")
		api.SetHTTPClient(rec.srv.Client())
		_, err := api.Whoami()
		require.NoError(t, err, rec.log())

		assert.Equal(t, fmt.Sprintf("GET /auth/whoami User-Agent: %q", "tx/dev "+platform()), rec.log())
	})

	t.Run("tx build sends the Run version on every API and instance request", func(t *testing.T) {
		setAgentEnv(t, "CURSOR_AGENT", "1")
		t.Cleanup(func() { cli.SetVersion("") })
		f := newFakeTexOps(t)
		projectDir(t, projectConfig)

		r := runTx(t, f, "build")
		require.Equal(t, cli.ExitOK, r.code, r)

		want := "tx/test " + platform() + " agent/cursor"
		recorded := f.recordedUserAgents()
		require.NotEmpty(t, recorded, r)
		for _, rec := range recorded {
			assert.Equal(t, want, rec.userAgent, "%s\n%s", rec.request, r)
		}
		assert.Contains(t, r.requests, "POST /api/projects/prj_test/session", r)
		assert.Contains(t, r.requests, "POST /projects/prj_test/build", r)
	})
}
