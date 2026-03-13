package cli_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/texops/tx/internal/cli"
)

func TestAPIClient_CreateProject(t *testing.T) {
	t.Run("creates project and returns response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/api/projects", r.URL.Path)
			assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))

			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "myproject", body["name"])
			assert.Equal(t, "texlive:2021", body["distribution_version"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"id":                   "prj_abc123",
				"name":                 "myproject",
				"distribution_version": "texlive:2021",
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-key")
		result, err := client.CreateProject("myproject", "texlive:2021", "")
		require.NoError(t, err)
		assert.Equal(t, "prj_abc123", result.ID)
		assert.Equal(t, "myproject", result.Name)
		assert.Equal(t, "texlive:2021", result.DistributionVersion)
	})

	t.Run("returns error on non-success response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte("no alive instance"))
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "key")
		_, err := client.CreateProject("proj", "texlive:2021", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "create project failed (409)")
	})

	t.Run("includes project_key in request body when provided", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "myproject", body["name"])
			assert.Equal(t, "texlive:2021", body["distribution_version"])
			assert.Equal(t, "k7Gx9mR2pL4wN8qY5vBt3a", body["project_key"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"id":                   "prj_new",
				"name":                 "myproject",
				"distribution_version": "texlive:2021",
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-key")
		result, err := client.CreateProject("myproject", "texlive:2021", "k7Gx9mR2pL4wN8qY5vBt3a")
		require.NoError(t, err)
		assert.Equal(t, "prj_new", result.ID)
	})

	t.Run("omits project_key from request body when empty", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			_, hasKey := body["project_key"]
			assert.False(t, hasKey, "project_key should not be present when empty")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"id":                   "prj_legacy",
				"name":                 "proj",
				"distribution_version": "texlive:2021",
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-key")
		_, err := client.CreateProject("proj", "texlive:2021", "")
		require.NoError(t, err)
	})

	t.Run("accepts 200 response for existing project", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"id":                   "prj_existing",
				"name":                 "myproject",
				"distribution_version": "texlive:2021",
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-key")
		result, err := client.CreateProject("myproject", "texlive:2021", "k7Gx9mR2pL4wN8qY5vBt3a")
		require.NoError(t, err)
		assert.Equal(t, "prj_existing", result.ID)
	})
}

func TestAPIClient_GetSession(t *testing.T) {
	t.Run("returns session info with distribution_version", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/api/projects/prj_abc/session", r.URL.Path)
			assert.Equal(t, "Bearer my-key", r.Header.Get("Authorization"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var body map[string]string
			err := json.NewDecoder(r.Body).Decode(&body)
			require.NoError(t, err)
			assert.Equal(t, "texlive:2021", body["distribution_version"])

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"instance_url": "https://10.0.0.1:8443",
				"jwt":          "eyJhbGciOi...",
				"cache_cold":   true,
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "my-key")
		session, err := client.GetSession("prj_abc", "texlive:2021")
		require.NoError(t, err)
		assert.Equal(t, "https://10.0.0.1:8443", session.InstanceURL)
		assert.Equal(t, "eyJhbGciOi...", session.JWT)
		assert.True(t, session.CacheCold)
	})

	t.Run("sends distribution_version in request body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]string
			err := json.NewDecoder(r.Body).Decode(&body)
			require.NoError(t, err)
			assert.Equal(t, "texlive:2019", body["distribution_version"])

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"instance_url": "https://10.0.0.2:8443",
				"jwt":          "jwt-2019",
				"cache_cold":   false,
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "my-key")
		session, err := client.GetSession("prj_abc", "texlive:2019")
		require.NoError(t, err)
		assert.Equal(t, "https://10.0.0.2:8443", session.InstanceURL)
		assert.Equal(t, "jwt-2019", session.JWT)
	})

	t.Run("returns error on 503", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("no alive instance"))
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "key")
		_, err := client.GetSession("prj_abc", "texlive:2021")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get session failed (503)")
	})
}

func TestInstanceClient_Sync(t *testing.T) {
	t.Run("sends files manifest and returns missing paths", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/projects/prj_123/sync", r.URL.Path)
			assert.Equal(t, "Bearer jwt-token", r.Header.Get("Authorization"))

			var body struct {
				Files []cli.FileEntry `json:"files"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Len(t, body.Files, 2)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"missing": []string{"file2.tex"},
			})
		}))
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "jwt-token")
		client.SetHTTPClient(srv.Client())
		result, err := client.Sync("prj_123", []cli.FileEntry{
			{Path: "file1.tex", Hash: "aaa"},
			{Path: "file2.tex", Hash: "bbb"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"file2.tex"}, result.Missing)
	})

	t.Run("returns error on non-ok response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(401)
			w.Write([]byte("Unauthorized"))
		}))
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "bad-jwt")
		client.SetHTTPClient(srv.Client())
		_, err := client.Sync("prj_123", []cli.FileEntry{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sync failed (401)")
	})
}

func TestInstanceClient_Upload(t *testing.T) {
	t.Run("sends tar archive with correct content type", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/projects/prj_123/upload", r.URL.Path)
			assert.Equal(t, "application/x-tar", r.Header.Get("Content-Type"))

			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			assert.True(t, len(body) > 0)

			tr := tar.NewReader(bytes.NewReader(body))
			hdr, err := tr.Next()
			require.NoError(t, err)
			assert.Equal(t, "test.tex", hdr.Name)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"stored": 1})
		}))
		defer srv.Close()

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "test.tex"), []byte("\\documentclass{article}"), 0644))

		client := cli.NewInstanceClient(srv.URL, "jwt")
		client.SetHTTPClient(srv.Client())
		err := client.Upload("prj_123", dir, []string{"test.tex"}, nil)
		require.NoError(t, err)
	})

	t.Run("skips upload when no files are missing", func(t *testing.T) {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
		}))
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "jwt")
		client.SetHTTPClient(srv.Client())
		err := client.Upload("prj_123", "/tmp", []string{}, nil)
		require.NoError(t, err)
		assert.False(t, called)
	})

	t.Run("calls progress callback during upload", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"stored": 1})
		}))
		defer srv.Close()

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "test.tex"), []byte("\\documentclass{article}"), 0644))

		var progressCalls []int64
		client := cli.NewInstanceClient(srv.URL, "jwt")
		client.SetHTTPClient(srv.Client())
		err := client.Upload("prj_123", dir, []string{"test.tex"}, func(sent, total int64) {
			progressCalls = append(progressCalls, sent)
		})
		require.NoError(t, err)
		assert.True(t, len(progressCalls) > 0, "progress callback should have been called")
		// Last call should be at or near total
		lastSent := progressCalls[len(progressCalls)-1]
		assert.True(t, lastSent > 0, "last sent bytes should be > 0")
	})
}

func TestInstanceClient_Build(t *testing.T) {
	t.Run("parses SSE stream and returns done event", func(t *testing.T) {
		doneData, _ := json.Marshal(map[string]interface{}{
			"status":   "success",
			"pdfUrl":   "/projects/prj_123/builds/bld_abc/output",
			"build_id": "bld_abc",
		})
		sseBody := "event: log\ndata: {\"message\":\"Compiling...\"}\n\nevent: log\ndata: {\"message\":\"Done.\"}\n\nevent: done\ndata: " + string(doneData) + "\n\n"

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/projects/prj_123/build", r.URL.Path)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(sseBody))
		}))
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "jwt")
		client.SetHTTPClient(srv.Client())
		var logs []string
		result, err := client.Build("prj_123", "paper.tex", "", "texlive:2021", "", nil, func(line string) {
			logs = append(logs, line)
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"Compiling...", "Done."}, logs)
		assert.Equal(t, "success", result.Status)
	})

	t.Run("handles build failure", func(t *testing.T) {
		doneData, _ := json.Marshal(map[string]interface{}{
			"status":  "error",
			"message": "Build failed",
		})
		sseBody := "event: done\ndata: " + string(doneData) + "\n\n"

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(sseBody))
		}))
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "jwt")
		client.SetHTTPClient(srv.Client())
		result, err := client.Build("prj_123", "paper.tex", "", "texlive:2021", "", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, "error", result.Status)
		assert.Equal(t, "Build failed", result.Message)
	})

	t.Run("returns error on non-ok response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte("Server error"))
		}))
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "jwt")
		client.SetHTTPClient(srv.Client())
		_, err := client.Build("prj_123", "paper.tex", "", "texlive:2021", "", nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "build request failed (500)")
	})
}

func TestInstanceClient_DownloadPDF(t *testing.T) {
	t.Run("downloads PDF from instance", func(t *testing.T) {
		pdfContent := []byte("%PDF-1.4 fake content")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/projects/prj_123/builds/bld_abc/output", r.URL.Path)
			assert.Equal(t, "Bearer jwt-token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/pdf")
			w.Write(pdfContent)
		}))
		defer srv.Close()

		dir := t.TempDir()
		outputPath := filepath.Join(dir, "output.pdf")

		client := cli.NewInstanceClient(srv.URL, "jwt-token")
		client.SetHTTPClient(srv.Client())
		err := client.DownloadPDF("prj_123", "bld_abc", outputPath)
		require.NoError(t, err)

		written, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, "%PDF-1.4 fake content", string(written))
	})

	t.Run("returns error on 404", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
		}))
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "jwt")
		client.SetHTTPClient(srv.Client())
		err := client.DownloadPDF("prj_123", "bld_bad", "/tmp/out.pdf")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PDF download failed (404)")
	})
}

func TestParseSSEStream(t *testing.T) {
	t.Run("parses JSON log events and extracts message", func(t *testing.T) {
		input := "event: log\ndata: {\"message\":\"Hello world\"}\n\nevent: done\ndata: {\"status\":\"success\"}\n\n"
		reader := strings.NewReader(input)

		var logs []string
		result, err := cli.ParseSSEStream(reader, func(line string) {
			logs = append(logs, line)
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"Hello world"}, logs)
		assert.Equal(t, "success", result.Status)
	})

	t.Run("falls back to raw text for non-JSON log data", func(t *testing.T) {
		input := "event: log\ndata: plain text line\n\nevent: done\ndata: {\"status\":\"success\"}\n\n"
		reader := strings.NewReader(input)

		var logs []string
		result, err := cli.ParseSSEStream(reader, func(line string) {
			logs = append(logs, line)
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"plain text line"}, logs)
		assert.Equal(t, "success", result.Status)
	})

	t.Run("handles chunked data", func(t *testing.T) {
		input := "event: log\ndata: {\"message\":\"line1\"}\n\nevent: log\ndata: {\"message\":\"line2\"}\n\nevent: done\ndata: {\"status\":\"success\",\"pdfUrl\":\"/download\"}\n\n"
		reader := strings.NewReader(input)

		var logs []string
		result, err := cli.ParseSSEStream(reader, func(line string) {
			logs = append(logs, line)
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"line1", "line2"}, logs)
		assert.Equal(t, "success", result.Status)
		assert.Equal(t, "/download", result.PdfURL)
	})

	t.Run("returns error status when stream ends without done event", func(t *testing.T) {
		input := "event: log\ndata: some output\n\n"
		reader := strings.NewReader(input)

		result, err := cli.ParseSSEStream(reader, nil)
		require.NoError(t, err)
		assert.Equal(t, "error", result.Status)
		assert.Contains(t, result.Message, "Stream ended unexpectedly")
	})

	t.Run("handles done event with error status", func(t *testing.T) {
		input := "event: done\ndata: {\"status\":\"error\",\"message\":\"failed\"}\n\n"
		reader := strings.NewReader(input)

		result, err := cli.ParseSSEStream(reader, nil)
		require.NoError(t, err)
		assert.Equal(t, "error", result.Status)
		assert.Equal(t, "failed", result.Message)
	})

	t.Run("handles queued event", func(t *testing.T) {
		input := "event: queued\ndata: {\"message\":\"build queued, waiting for previous build to finish\"}\n\nevent: log\ndata: {\"message\":\"Starting build\"}\n\nevent: done\ndata: {\"status\":\"success\"}\n\n"
		reader := strings.NewReader(input)

		var logs []string
		result, err := cli.ParseSSEStream(reader, func(line string) {
			logs = append(logs, line)
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"build queued, waiting for previous build to finish", "Starting build"}, logs)
		assert.Equal(t, "success", result.Status)
	})
}

func TestAPIClient_RequestDeviceCode(t *testing.T) {
	t.Run("returns device code response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/auth/device-code", r.URL.Path)

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"device_code":      "dc_abc123",
				"user_code":        "ABCD-1234",
				"verification_url": "https://texops.example.com/auth/verify",
				"expires_in":       900,
				"interval":         5,
			})
		}))
		defer srv.Close()

		client := cli.NewUnauthenticatedAPIClient(srv.URL)
		result, err := client.RequestDeviceCode()
		require.NoError(t, err)
		assert.Equal(t, "dc_abc123", result.DeviceCode)
		assert.Equal(t, "ABCD-1234", result.UserCode)
		assert.Equal(t, "https://texops.example.com/auth/verify", result.VerificationURL)
		assert.Equal(t, 900, result.ExpiresIn)
		assert.Equal(t, 5, result.Interval)
	})

	t.Run("returns error on server error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte("internal error"))
		}))
		defer srv.Close()

		client := cli.NewUnauthenticatedAPIClient(srv.URL)
		_, err := client.RequestDeviceCode()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "device code request failed (500)")
	})
}

func TestAPIClient_PollToken(t *testing.T) {
	t.Run("returns token when authorized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/auth/token", r.URL.Path)

			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			assert.Equal(t, "dc_test", body["device_code"])

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jwt":        "eyJhbGciOi.test.sig",
				"expires_at": "2026-03-30T00:00:00Z",
			})
		}))
		defer srv.Close()

		client := cli.NewUnauthenticatedAPIClient(srv.URL)
		result, err := client.PollToken("dc_test")
		require.NoError(t, err)
		assert.Equal(t, "eyJhbGciOi.test.sig", result.JWT)
		assert.Equal(t, "2026-03-30T00:00:00Z", result.ExpiresAt)
	})

	t.Run("returns ErrAuthorizationPending on 428", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPreconditionRequired)
			json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
		}))
		defer srv.Close()

		client := cli.NewUnauthenticatedAPIClient(srv.URL)
		_, err := client.PollToken("dc_pending")
		require.ErrorIs(t, err, cli.ErrAuthorizationPending)
	})

	t.Run("returns ErrDeviceCodeExpired on 410", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusGone)
			json.NewEncoder(w).Encode(map[string]string{"error": "expired"})
		}))
		defer srv.Close()

		client := cli.NewUnauthenticatedAPIClient(srv.URL)
		_, err := client.PollToken("dc_expired")
		require.ErrorIs(t, err, cli.ErrDeviceCodeExpired)
	})
}

func TestAPIClient_RefreshToken(t *testing.T) {
	t.Run("returns new token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/auth/refresh", r.URL.Path)
			assert.Equal(t, "Bearer old-jwt", r.Header.Get("Authorization"))

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"jwt":        "new-jwt-token",
				"expires_at": "2026-04-30T00:00:00Z",
			})
		}))
		defer srv.Close()

		client := cli.NewUnauthenticatedAPIClient(srv.URL)
		result, err := client.RefreshToken("old-jwt")
		require.NoError(t, err)
		assert.Equal(t, "new-jwt-token", result.JWT)
	})

	t.Run("returns error on 401", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("invalid token"))
		}))
		defer srv.Close()

		client := cli.NewUnauthenticatedAPIClient(srv.URL)
		_, err := client.RefreshToken("bad-jwt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "refresh failed (401)")
	})
}

func TestAPIClient_Whoami(t *testing.T) {
	t.Run("returns whoami response with JWT auth", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "GET", r.Method)
			assert.Equal(t, "/auth/whoami", r.URL.Path)
			assert.Equal(t, "Bearer test-jwt", r.Header.Get("Authorization"))

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"user_id":     "usr_abc123",
				"email":       "user@example.com",
				"auth_method": "jwt",
				"expires_at":  "2026-03-30T12:00:00Z",
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		result, err := client.Whoami()
		require.NoError(t, err)
		assert.Equal(t, "usr_abc123", result.UserID)
		assert.Equal(t, "user@example.com", result.Email)
		assert.Equal(t, "jwt", result.AuthMethod)
		require.NotNil(t, result.ExpiresAt)
		assert.Equal(t, "2026-03-30T12:00:00Z", *result.ExpiresAt)
	})

	t.Run("returns whoami response with API token auth (no expiry)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"user_id":     "usr_abc123",
				"email":       "",
				"auth_method": "api_token",
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "texops_token_test-token")
		result, err := client.Whoami()
		require.NoError(t, err)
		assert.Equal(t, "usr_abc123", result.UserID)
		assert.Equal(t, "api_token", result.AuthMethod)
		assert.Nil(t, result.ExpiresAt)
	})

	t.Run("returns error on 401 unauthorized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("unauthorized"))
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "bad-token")
		_, err := client.Whoami()
		require.Error(t, err)
		var apiErr *cli.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 401, apiErr.StatusCode)
	})

	t.Run("returns error on network failure", func(t *testing.T) {
		client := cli.NewAPIClient("http://127.0.0.1:1", "token")
		_, err := client.Whoami()
		require.Error(t, err)
	})
}

func TestE2E_TwoClients(t *testing.T) {
	t.Run("full build flow with API + instance mock servers", func(t *testing.T) {
		pdfContent := []byte("%PDF-1.4 test output")

		instSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/sync") && r.Method == "POST":
				var body struct {
					Files []cli.FileEntry `json:"files"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				missing := make([]string, len(body.Files))
				for i, f := range body.Files {
					missing[i] = f.Path
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"missing": missing})

			case strings.HasSuffix(r.URL.Path, "/upload") && r.Method == "POST":
				io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{"stored": 1})

			case strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST":
				doneData, _ := json.Marshal(map[string]interface{}{
					"status":   "success",
					"pdfUrl":   "/projects/prj_test/builds/bld_001/output",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "event: log\ndata: {\"message\":\"Running latexmk...\"}\n\n")
				fmt.Fprintf(w, "event: done\ndata: %s\n\n", string(doneData))

			case strings.HasSuffix(r.URL.Path, "/output") && r.Method == "GET":
				w.Header().Set("Content-Type", "application/pdf")
				w.Write(pdfContent)

			default:
				w.WriteHeader(404)
			}
		}))
		defer instSrv.Close()

		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/projects" && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(map[string]string{
					"id":                   "prj_test",
					"name":                 "test-project",
					"distribution_version": "texlive:2021",
				})

			case strings.HasSuffix(r.URL.Path, "/session") && r.Method == "POST":
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"instance_url": instSrv.URL,
					"jwt":          "test-jwt-token",
					"cache_cold":   false,
				})

			default:
				w.WriteHeader(404)
			}
		}))
		defer apiSrv.Close()

		api := cli.NewAPIClient(apiSrv.URL, "test-api-key")
		project, err := api.CreateProject("test-project", "texlive:2021", "")
		require.NoError(t, err)
		assert.Equal(t, "prj_test", project.ID)

		session, err := api.GetSession(project.ID, "texlive:2021")
		require.NoError(t, err)
		assert.Equal(t, instSrv.URL, session.InstanceURL)
		assert.Equal(t, "test-jwt-token", session.JWT)

		inst := cli.NewInstanceClient(session.InstanceURL, session.JWT)
		inst.SetHTTPClient(instSrv.Client())

		files := []cli.FileEntry{{Path: "paper.tex", Hash: "abc123"}}
		syncResult, err := inst.Sync(project.ID, files)
		require.NoError(t, err)
		assert.Len(t, syncResult.Missing, 1)

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "paper.tex"), []byte("\\documentclass{article}"), 0644))
		err = inst.Upload(project.ID, dir, syncResult.Missing, nil)
		require.NoError(t, err)

		var logs []string
		result, err := inst.Build(project.ID, "paper.tex", "", "texlive:2021", "", nil, func(line string) {
			logs = append(logs, line)
		})
		require.NoError(t, err)
		assert.Equal(t, "success", result.Status)
		assert.Equal(t, []string{"Running latexmk..."}, logs)

		outputPath := filepath.Join(dir, "paper.pdf")
		err = inst.DownloadPDF(project.ID, "bld_001", outputPath)
		require.NoError(t, err)

		written, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, pdfContent, written)
	})
}

func TestInstanceClient_Build_DirectoryInPayload(t *testing.T) {
	t.Run("includes directory in JSON payload when non-empty", func(t *testing.T) {
		var receivedPayload map[string]interface{}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST" {
				json.NewDecoder(r.Body).Decode(&receivedPayload)
				doneData, _ := json.Marshal(map[string]interface{}{
					"status":   "success",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		ic := cli.NewInstanceClient(srv.URL, "test-jwt")
		ic.SetHTTPClient(srv.Client())

		_, err := ic.Build("prj_001", "paper.tex", "chapters/paper", "texlive:2021", "", nil, nil)
		require.NoError(t, err)

		assert.Equal(t, "paper.tex", receivedPayload["main"])
		assert.Equal(t, "chapters/paper", receivedPayload["directory"])
		assert.Equal(t, "texlive:2021", receivedPayload["distribution_version"])
	})

	t.Run("omits directory from JSON payload when empty", func(t *testing.T) {
		var receivedBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST" {
				receivedBody, _ = io.ReadAll(r.Body)
				doneData, _ := json.Marshal(map[string]interface{}{
					"status":   "success",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		ic := cli.NewInstanceClient(srv.URL, "test-jwt")
		ic.SetHTTPClient(srv.Client())

		_, err := ic.Build("prj_001", "paper.tex", "", "texlive:2021", "", nil, nil)
		require.NoError(t, err)

		assert.NotContains(t, string(receivedBody), "directory")
	})
}

func TestInstanceClient_Build_CompilerInPayload(t *testing.T) {
	t.Run("includes compiler in JSON payload when non-empty", func(t *testing.T) {
		var receivedPayload map[string]interface{}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST" {
				json.NewDecoder(r.Body).Decode(&receivedPayload)
				doneData, _ := json.Marshal(map[string]interface{}{
					"status":   "success",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		ic := cli.NewInstanceClient(srv.URL, "test-jwt")
		ic.SetHTTPClient(srv.Client())

		_, err := ic.Build("prj_001", "paper.tex", "", "texlive:2021", "xelatex", nil, nil)
		require.NoError(t, err)

		assert.Equal(t, "paper.tex", receivedPayload["main"])
		assert.Equal(t, "xelatex", receivedPayload["compiler"])
		assert.Equal(t, "texlive:2021", receivedPayload["distribution_version"])
	})

	t.Run("omits compiler from JSON payload when empty", func(t *testing.T) {
		var receivedBody []byte

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST" {
				receivedBody, _ = io.ReadAll(r.Body)
				doneData, _ := json.Marshal(map[string]interface{}{
					"status":   "success",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		ic := cli.NewInstanceClient(srv.URL, "test-jwt")
		ic.SetHTTPClient(srv.Client())

		_, err := ic.Build("prj_001", "paper.tex", "", "texlive:2021", "", nil, nil)
		require.NoError(t, err)

		assert.NotContains(t, string(receivedBody), "compiler")
	})

	t.Run("includes compiler in BuildWithArgs payload", func(t *testing.T) {
		var receivedPayload map[string]interface{}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/build") && r.Method == "POST" {
				json.NewDecoder(r.Body).Decode(&receivedPayload)
				doneData, _ := json.Marshal(map[string]interface{}{
					"status":   "success",
					"build_id": "bld_001",
				})
				w.Header().Set("Content-Type", "text/event-stream")
				w.Write([]byte("event: done\ndata: " + string(doneData) + "\n\n"))
				return
			}
			w.WriteHeader(404)
		}))
		defer srv.Close()

		ic := cli.NewInstanceClient(srv.URL, "test-jwt")
		ic.SetHTTPClient(srv.Client())

		_, err := ic.BuildWithArgs("prj_001", "paper.tex", "", "texlive:2021", "lualatex", nil, nil, nil)
		require.NoError(t, err)

		assert.Equal(t, "lualatex", receivedPayload["compiler"])
	})
}

func TestAPIClient_CreateAPIToken(t *testing.T) {
	t.Run("creates token and returns full token", func(t *testing.T) {
		expiresAt := "2026-05-30T00:00:00Z"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/auth/tokens", r.URL.Path)
			assert.Equal(t, "Bearer test-jwt", r.Header.Get("Authorization"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "CI prod", body["name"])
			assert.Equal(t, float64(2592000), body["expires_in"])

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "texops_token_abcdefghijklmnopqrstuvwxyz1234567890ABCDE",
				"id":         "tok_01ABCDEF",
				"name":       "CI prod",
				"prefix":     "texops_token_abcd",
				"expires_at": expiresAt,
				"created_at": "2026-02-28T00:00:00Z",
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		expiresIn := int64(2592000)
		result, err := client.CreateAPIToken("CI prod", &expiresIn)
		require.NoError(t, err)
		assert.Equal(t, "texops_token_abcdefghijklmnopqrstuvwxyz1234567890ABCDE", result.Token)
		assert.Equal(t, "tok_01ABCDEF", result.ID)
		assert.Equal(t, "CI prod", result.Name)
		assert.Equal(t, "texops_token_abcd", result.Prefix)
		require.NotNil(t, result.ExpiresAt)
		assert.Equal(t, expiresAt, *result.ExpiresAt)
		assert.Equal(t, "2026-02-28T00:00:00Z", result.CreatedAt)
	})

	t.Run("creates token without expiry", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "no-expiry", body["name"])
			_, hasExpiry := body["expires_in"]
			assert.False(t, hasExpiry, "expires_in should not be present")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "texops_token_sometoken",
				"id":         "tok_02ABCDEF",
				"name":       "no-expiry",
				"prefix":     "texops_token_some",
				"created_at": "2026-02-28T00:00:00Z",
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		result, err := client.CreateAPIToken("no-expiry", nil)
		require.NoError(t, err)
		assert.Equal(t, "tok_02ABCDEF", result.ID)
		assert.Nil(t, result.ExpiresAt)
	})

	t.Run("returns ErrTokenConflict on 409", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte("token name already exists"))
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		_, err := client.CreateAPIToken("duplicate", nil)
		require.ErrorIs(t, err, cli.ErrTokenConflict)
	})

	t.Run("returns APIError on other errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		_, err := client.CreateAPIToken("test", nil)
		require.Error(t, err)
		var apiErr *cli.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 500, apiErr.StatusCode)
	})
}

func TestAPIClient_ListAPITokens(t *testing.T) {
	t.Run("returns empty list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "GET", r.Method)
			assert.Equal(t, "/auth/tokens", r.URL.Path)
			assert.Equal(t, "Bearer test-jwt", r.Header.Get("Authorization"))

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]interface{}{})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		result, err := client.ListAPITokens()
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("returns multiple tokens", func(t *testing.T) {
		expiresAt := "2026-05-30T00:00:00Z"
		lastUsedAt := "2026-02-28T12:00:00Z"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id":           "tok_01",
					"name":         "CI prod",
					"prefix":       "texops_token_abcd",
					"expires_at":   expiresAt,
					"last_used_at": lastUsedAt,
					"created_at":   "2026-02-01T00:00:00Z",
				},
				{
					"id":         "tok_02",
					"name":       "local dev",
					"prefix":     "texops_token_efgh",
					"created_at": "2026-02-15T00:00:00Z",
				},
			})
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		result, err := client.ListAPITokens()
		require.NoError(t, err)
		require.Len(t, result, 2)

		assert.Equal(t, "tok_01", result[0].ID)
		assert.Equal(t, "CI prod", result[0].Name)
		assert.Equal(t, "texops_token_abcd", result[0].Prefix)
		require.NotNil(t, result[0].ExpiresAt)
		assert.Equal(t, expiresAt, *result[0].ExpiresAt)
		require.NotNil(t, result[0].LastUsedAt)
		assert.Equal(t, lastUsedAt, *result[0].LastUsedAt)

		assert.Equal(t, "tok_02", result[1].ID)
		assert.Equal(t, "local dev", result[1].Name)
		assert.Nil(t, result[1].ExpiresAt)
		assert.Nil(t, result[1].LastUsedAt)
	})

	t.Run("returns error on 401", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("unauthorized"))
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "bad-token")
		_, err := client.ListAPITokens()
		require.Error(t, err)
		var apiErr *cli.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 401, apiErr.StatusCode)
	})
}

func TestAPIClient_DeleteAPIToken(t *testing.T) {
	t.Run("deletes token successfully", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "DELETE", r.Method)
			assert.Equal(t, "/auth/tokens/tok_01ABCDEF", r.URL.Path)
			assert.Equal(t, "Bearer test-jwt", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		err := client.DeleteAPIToken("tok_01ABCDEF")
		require.NoError(t, err)
	})

	t.Run("returns ErrTokenNotFound on 404", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("token not found"))
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		err := client.DeleteAPIToken("tok_nonexistent")
		require.ErrorIs(t, err, cli.ErrTokenNotFound)
	})

	t.Run("returns APIError on other errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
		}))
		defer srv.Close()

		client := cli.NewAPIClient(srv.URL, "test-jwt")
		err := client.DeleteAPIToken("tok_01")
		require.Error(t, err)
		var apiErr *cli.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, 500, apiErr.StatusCode)
	})
}
