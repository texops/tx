package cli_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

type tarEntry struct {
	name    string
	content string
}

type uploadRequest struct {
	size    int64
	entries []tarEntry
}

type uploadRecorder struct {
	mu       sync.Mutex
	requests []uploadRequest
	failAt   int
}

func (rec *uploadRecorder) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/projects/prj_123/upload", r.URL.Path)
		assert.Equal(t, "application/x-tar", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)

		var entries []tarEntry
		tr := tar.NewReader(bytes.NewReader(body))
		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if !assert.NoError(t, err) {
				break
			}
			data, err := io.ReadAll(tr)
			assert.NoError(t, err)
			entries = append(entries, tarEntry{name: hdr.Name, content: string(data)})
		}

		rec.mu.Lock()
		rec.requests = append(rec.requests, uploadRequest{size: int64(len(body)), entries: entries})
		n := len(rec.requests)
		rec.mu.Unlock()

		if rec.failAt == n {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"stored": len(entries)})
	}
}

func (rec *uploadRecorder) recorded() []uploadRequest {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]uploadRequest(nil), rec.requests...)
}

func newUploadServer(t *testing.T, rec *uploadRecorder) *cli.InstanceClient {
	t.Helper()
	srv := httptest.NewServer(rec.handler(t))
	t.Cleanup(srv.Close)
	client := cli.NewInstanceClient(srv.URL, "jwt")
	client.SetHTTPClient(srv.Client())
	return client
}

func writeUploadFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
}

func entryNames(req uploadRequest) []string {
	names := make([]string, 0, len(req.entries))
	for _, e := range req.entries {
		names = append(names, e.name)
	}
	return names
}

func TestInstanceClient_Upload_Chunking(t *testing.T) {
	t.Run("small upload sends a single request", func(t *testing.T) {
		dir := t.TempDir()
		writeUploadFiles(t, dir, map[string]string{
			"main.tex":      "\\documentclass{article}",
			"sec/intro.tex": "Hello",
		})
		rec := &uploadRecorder{}
		client := newUploadServer(t, rec)

		err := client.Upload(t.Context(), "prj_123", dir, []string{"main.tex", "sec/intro.tex"}, nil)
		require.NoError(t, err)

		reqs := rec.recorded()
		require.Len(t, reqs, 1)
		assert.Equal(t, []tarEntry{
			{name: "main.tex", content: "\\documentclass{article}"},
			{name: "sec/intro.tex", content: "Hello"},
		}, reqs[0].entries)
	})

	t.Run("files exceeding the chunk limit are split across requests", func(t *testing.T) {
		cli.SetUploadChunkBytes(t, 100)
		dir := t.TempDir()
		files := map[string]string{
			"a.tex":     strings.Repeat("a", 40),
			"b.tex":     strings.Repeat("b", 40),
			"c.tex":     strings.Repeat("c", 40),
			"img/d.png": strings.Repeat("d", 20),
			"e.tex":     strings.Repeat("e", 50),
		}
		writeUploadFiles(t, dir, files)
		rec := &uploadRecorder{}
		client := newUploadServer(t, rec)

		err := client.Upload(t.Context(), "prj_123", dir, []string{"a.tex", "b.tex", "c.tex", "img/d.png", "e.tex"}, nil)
		require.NoError(t, err)

		reqs := rec.recorded()
		require.Len(t, reqs, 3)
		assert.Equal(t, []string{"a.tex", "b.tex"}, entryNames(reqs[0]))
		assert.Equal(t, []string{"c.tex", "img/d.png"}, entryNames(reqs[1]))
		assert.Equal(t, []string{"e.tex"}, entryNames(reqs[2]))

		received := map[string]string{}
		for _, req := range reqs {
			for _, e := range req.entries {
				_, dup := received[e.name]
				assert.False(t, dup, "file %s uploaded more than once", e.name)
				received[e.name] = e.content
			}
		}
		assert.Equal(t, files, received)
	})

	t.Run("files totaling exactly the chunk limit share a request", func(t *testing.T) {
		cli.SetUploadChunkBytes(t, 100)
		dir := t.TempDir()
		writeUploadFiles(t, dir, map[string]string{
			"a.tex": strings.Repeat("a", 60),
			"b.tex": strings.Repeat("b", 40),
		})
		rec := &uploadRecorder{}
		client := newUploadServer(t, rec)

		err := client.Upload(t.Context(), "prj_123", dir, []string{"a.tex", "b.tex"}, nil)
		require.NoError(t, err)

		reqs := rec.recorded()
		require.Len(t, reqs, 1)
		assert.Equal(t, []string{"a.tex", "b.tex"}, entryNames(reqs[0]))
	})

	t.Run("file larger than the chunk limit is sent alone", func(t *testing.T) {
		cli.SetUploadChunkBytes(t, 100)
		dir := t.TempDir()
		big := strings.Repeat("x", 250)
		writeUploadFiles(t, dir, map[string]string{
			"small1.tex": "one",
			"big.pdf":    big,
			"small2.tex": "two",
		})
		rec := &uploadRecorder{}
		client := newUploadServer(t, rec)

		err := client.Upload(t.Context(), "prj_123", dir, []string{"small1.tex", "big.pdf", "small2.tex"}, nil)
		require.NoError(t, err)

		reqs := rec.recorded()
		require.Len(t, reqs, 3)
		assert.Equal(t, []tarEntry{{name: "small1.tex", content: "one"}}, reqs[0].entries)
		assert.Equal(t, []tarEntry{{name: "big.pdf", content: big}}, reqs[1].entries)
		assert.Equal(t, []tarEntry{{name: "small2.tex", content: "two"}}, reqs[2].entries)
	})

	t.Run("failing chunk stops the upload and returns its error", func(t *testing.T) {
		cli.SetUploadChunkBytes(t, 100)
		dir := t.TempDir()
		writeUploadFiles(t, dir, map[string]string{
			"a.tex": strings.Repeat("a", 80),
			"b.tex": strings.Repeat("b", 80),
			"c.tex": strings.Repeat("c", 80),
		})
		rec := &uploadRecorder{failAt: 2}
		client := newUploadServer(t, rec)

		err := client.Upload(t.Context(), "prj_123", dir, []string{"a.tex", "b.tex", "c.tex"}, nil)
		require.Error(t, err)
		assert.Equal(t, "upload failed (500): boom", err.Error())

		reqs := rec.recorded()
		require.Len(t, reqs, 2)
		assert.Equal(t, []string{"a.tex"}, entryNames(reqs[0]))
		assert.Equal(t, []string{"b.tex"}, entryNames(reqs[1]))
	})

	t.Run("progress is monotonic across chunks and ends at total", func(t *testing.T) {
		cli.SetUploadChunkBytes(t, 1000)
		dir := t.TempDir()
		longName := strings.Repeat("very-long-directory-name/", 6) + "figure.png"
		writeUploadFiles(t, dir, map[string]string{
			"a.tex":  strings.Repeat("a", 700),
			"b.tex":  strings.Repeat("b", 700),
			longName: strings.Repeat("c", 1500),
			"d.tex":  strings.Repeat("d", 10),
		})
		rec := &uploadRecorder{}
		client := newUploadServer(t, rec)

		var sents, totals []int64
		err := client.Upload(t.Context(), "prj_123", dir, []string{"a.tex", "b.tex", longName, "d.tex"}, func(sent, total int64) {
			sents = append(sents, sent)
			totals = append(totals, total)
		})
		require.NoError(t, err)

		reqs := rec.recorded()
		require.Len(t, reqs, 4)
		var bodyTotal int64
		for _, req := range reqs {
			bodyTotal += req.size
		}

		require.NotEmpty(t, sents)
		for i, total := range totals {
			assert.Equal(t, bodyTotal, total)
			assert.LessOrEqual(t, sents[i], total)
			if i > 0 {
				assert.GreaterOrEqual(t, sents[i], sents[i-1])
			}
		}
		assert.Equal(t, bodyTotal, sents[len(sents)-1])
	})
}
