package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

func pdfServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, err := w.Write(content)
		require.NoError(t, err)
	}))
}

func failingPDFServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "build not found"})
	}))
}

func TestDownloadPDF_CreatesNewFile(t *testing.T) {
	t.Run("creates file when it does not exist", func(t *testing.T) {
		content := []byte("%PDF-1.4 test content")
		srv := pdfServer(t, content)
		defer srv.Close()

		dir := t.TempDir()
		outputPath := filepath.Join(dir, "output.pdf")

		client := cli.NewInstanceClient(srv.URL, "test-jwt")
		err := client.DownloadPDF(t.Context(), "prj_abc123", "bld_abc123", outputPath)
		require.NoError(t, err)

		got, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, content, got)
	})
}

func TestDownloadPDF_PreservesInode(t *testing.T) {
	t.Run("preserves inode when file already exists", func(t *testing.T) {
		dir := t.TempDir()
		outputPath := filepath.Join(dir, "output.pdf")

		require.NoError(t, os.WriteFile(outputPath, []byte("old content"), 0o600))

		infoBefore, err := os.Stat(outputPath)
		require.NoError(t, err)
		inoBefore := infoBefore.Sys().(*syscall.Stat_t).Ino

		content := []byte("%PDF-1.4 new content")
		srv := pdfServer(t, content)
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "test-jwt")
		err = client.DownloadPDF(t.Context(), "prj_abc123", "bld_abc123", outputPath)
		require.NoError(t, err)

		got, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, content, got)

		infoAfter, err := os.Stat(outputPath)
		require.NoError(t, err)
		inoAfter := infoAfter.Sys().(*syscall.Stat_t).Ino

		assert.Equal(t, inoBefore, inoAfter, "inode should be preserved after download")
	})
}

func TestWriteFilePreserveInode_TempFileLocality(t *testing.T) {
	t.Run("creates temp file in target directory", func(t *testing.T) {
		dir := t.TempDir()
		outputPath := filepath.Join(dir, "output.pdf")

		content := []byte("%PDF-1.4 test content")
		err := cli.WriteFilePreserveInode(strings.NewReader(string(content)), outputPath)
		require.NoError(t, err)

		got, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, content, got)

		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range entries {
			assert.False(t, strings.HasSuffix(e.Name(), ".tmp"), "temp file should be cleaned up: %s", e.Name())
		}
	})
}

func TestDownloadPDF_FailureLeavesOriginalIntact(t *testing.T) {
	t.Run("server error leaves original file intact", func(t *testing.T) {
		dir := t.TempDir()
		outputPath := filepath.Join(dir, "output.pdf")
		originalContent := []byte("original PDF content")

		require.NoError(t, os.WriteFile(outputPath, originalContent, 0o600))

		srv := failingPDFServer(t)
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "test-jwt")
		err := client.DownloadPDF(t.Context(), "prj_abc123", "bld_abc123", outputPath)
		require.Error(t, err)

		got, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, originalContent, got, "original file should be untouched after failed download")

		entries, dirErr := os.ReadDir(dir)
		require.NoError(t, dirErr)
		for _, e := range entries {
			assert.False(t, strings.HasSuffix(e.Name(), ".tmp"), "temp file should be cleaned up: %s", e.Name())
		}
	})
}

func TestDownloadLog(t *testing.T) {
	t.Run("saves the log with the instance JWT", func(t *testing.T) {
		var gotPath, gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte("This is pdfTeX\n! Undefined control sequence.\n"))
		}))
		defer srv.Close()
		outputPath := filepath.Join(t.TempDir(), "paper.log")

		client := cli.NewInstanceClient(srv.URL+"/sandbox/2025", "instance-jwt")
		err := client.DownloadLog(t.Context(), "/projects/prj_abc/builds/bld_abc/log", outputPath)

		require.NoError(t, err, "GET %s", gotPath)
		assert.Equal(t, "/sandbox/2025/projects/prj_abc/builds/bld_abc/log", gotPath)
		assert.Equal(t, "Bearer instance-jwt", gotAuth)
		got, err := os.ReadFile(outputPath)
		require.NoError(t, err)
		assert.Equal(t, "This is pdfTeX\n! Undefined control sequence.\n", string(got))
	})

	t.Run("expired log is an API error with the server message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusGone)
			w.Write([]byte(`{"error":"build not found or expired"}`))
		}))
		defer srv.Close()
		outputPath := filepath.Join(t.TempDir(), "paper.log")

		client := cli.NewInstanceClient(srv.URL, "instance-jwt")
		err := client.DownloadLog(t.Context(), "/projects/prj_abc/builds/bld_abc/log", outputPath)

		require.Error(t, err)
		assert.Equal(t, "log download failed (410): build not found or expired", err.Error())
		assert.NoFileExists(t, outputPath)
	})

	t.Run("log URL outside the instance is refused without a request", func(t *testing.T) {
		var requests int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
		}))
		defer srv.Close()

		client := cli.NewInstanceClient(srv.URL, "instance-jwt")
		err := client.DownloadLog(t.Context(), "/../../projects/prj_abc/builds/bld_abc/log", filepath.Join(t.TempDir(), "paper.log"))

		require.Error(t, err)
		assert.Equal(t, `invalid log URL "/../../projects/prj_abc/builds/bld_abc/log"`, err.Error())
		assert.Zero(t, requests)
	})
}
