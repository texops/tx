package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/texops/tx/internal/cli"
)

func TestParseConfig(t *testing.T) {
	t.Run("parses valid multi-document config", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    output: output.pdf
  - name: slides
    main: slides/slides.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "texlive:2021", config.DistributionVersion)
		require.Len(t, config.Documents, 2)
		assert.Equal(t, "paper", config.Documents[0].Name)
		assert.Equal(t, "paper.tex", config.Documents[0].Main)
		assert.Equal(t, "output.pdf", config.Documents[0].Output)
		assert.Equal(t, "texlive:2021", config.Documents[0].DistributionVersion)
		assert.Equal(t, "slides", config.Documents[1].Name)
		assert.Equal(t, "slides/slides.tex", config.Documents[1].Main)
	})

	t.Run("defaults output to main with .pdf extension per document", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2019"
documents:
  - name: thesis
    main: thesis.tex
  - name: appendix
    main: appendix.latex
`)
		require.NoError(t, err)
		assert.Equal(t, "thesis.pdf", config.Documents[0].Output)
		assert.Equal(t, "appendix.pdf", config.Documents[1].Output)
	})

	t.Run("inherits top-level distribution_version", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "texlive:2021", config.Documents[0].DistributionVersion)
	})

	t.Run("per-document distribution_version overrides top-level", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
  - name: legacy
    main: legacy.tex
    distribution_version: "texlive:2013"
`)
		require.NoError(t, err)
		assert.Equal(t, "texlive:2021", config.Documents[0].DistributionVersion)
		assert.Equal(t, "texlive:2013", config.Documents[1].DistributionVersion)
	})

	t.Run("rejects duplicate document names", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
  - name: paper
    main: other.tex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate document name")
	})

	t.Run("rejects old-style top-level main field", func(t *testing.T) {
		_, err := cli.ParseConfig(`distribution_version: "texlive:2021"
main: paper.tex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config format has changed")
		assert.Contains(t, err.Error(), "documents")
	})

	t.Run("rejects missing distribution_version", func(t *testing.T) {
		_, err := cli.ParseConfig(`
documents:
  - name: paper
    main: paper.tex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required field 'distribution_version'")
	})

	t.Run("rejects non-string distribution_version", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: 2021
documents:
  - name: paper
    main: paper.tex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "distribution_version must be a string")
	})

	t.Run("rejects missing documents field", func(t *testing.T) {
		_, err := cli.ParseConfig(`distribution_version: "texlive:2021"`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required field 'documents'")
	})

	t.Run("rejects empty documents list", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents: []
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "non-empty list")
	})

	t.Run("rejects document without name", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - main: paper.tex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required field 'name'")
	})

	t.Run("rejects document without main", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required field 'main'")
	})

	t.Run("rejects empty string", func(t *testing.T) {
		_, err := cli.ParseConfig("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected a YAML mapping")
	})

	t.Run("rejects non-mapping YAML", func(t *testing.T) {
		_, err := cli.ParseConfig("- item1\n- item2")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected a YAML mapping")
	})

	t.Run("parses api_url", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
api_url: "https://custom.example.com"
documents:
  - name: paper
    main: paper.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "https://custom.example.com", config.APIURL)
	})

	t.Run("rejects main that escapes project directory", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: ../../etc/evil.tex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not escape project directory")
	})

	t.Run("rejects absolute main path", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: /etc/passwd
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not escape project directory")
	})

	t.Run("rejects output that escapes project directory", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    output: ../../exploit.pdf
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not escape project directory")
	})

	t.Run("allows subdirectory main and output paths", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: slides
    main: subdir/slides.tex
    output: output/slides.pdf
`)
		require.NoError(t, err)
		assert.Equal(t, "subdir/slides.tex", config.Documents[0].Main)
		assert.Equal(t, "output/slides.pdf", config.Documents[0].Output)
	})

	t.Run("accepts any distribution version string", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2013"
documents:
  - name: paper
    main: paper.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "texlive:2013", config.DistributionVersion)
	})

	t.Run("rejects non-string output", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    output: 42
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'output' must be a string")
	})

	t.Run("rejects non-string per-document distribution_version", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    distribution_version: 2021
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'distribution_version' must be a string")
	})

	t.Run("parses document with directory field", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    directory: chapters/paper
`)
		require.NoError(t, err)
		require.Len(t, config.Documents, 1)
		assert.Equal(t, "paper.tex", config.Documents[0].Main)
		assert.Equal(t, "chapters/paper", config.Documents[0].Directory)
	})

	t.Run("directory defaults to empty when omitted", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "", config.Documents[0].Directory)
	})

	t.Run("rejects directory that escapes project root via traversal", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    directory: ../../etc
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'directory' must not escape project directory")
	})

	t.Run("rejects absolute directory path", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    directory: /etc/secrets
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'directory' must not escape project directory")
	})

	t.Run("rejects non-string directory", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    directory: 42
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'directory' must be a string")
	})

	t.Run("existing configs without directory still parse correctly", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    output: output.pdf
  - name: slides
    main: slides/slides.tex
`)
		require.NoError(t, err)
		require.Len(t, config.Documents, 2)
		assert.Equal(t, "paper.tex", config.Documents[0].Main)
		assert.Equal(t, "", config.Documents[0].Directory)
		assert.Equal(t, "output.pdf", config.Documents[0].Output)
		assert.Equal(t, "slides/slides.tex", config.Documents[1].Main)
		assert.Equal(t, "", config.Documents[1].Directory)
		assert.Equal(t, "slides/slides.pdf", config.Documents[1].Output)
	})

	t.Run("output includes directory to prevent collisions", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    directory: chapters/paper
`)
		require.NoError(t, err)
		assert.Equal(t, "chapters/paper/paper.pdf", config.Documents[0].Output)
	})

	t.Run("output has no directory prefix when directory is empty", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "paper.pdf", config.Documents[0].Output)
	})
}

func TestParseConfigCompiler(t *testing.T) {
	t.Run("omitted compiler defaults to pdflatex", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "pdflatex", config.Compiler)
		assert.Equal(t, "pdflatex", config.Documents[0].Compiler)
	})

	t.Run("top-level compiler is inherited by documents", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
compiler: xelatex
documents:
  - name: paper
    main: paper.tex
  - name: slides
    main: slides.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "xelatex", config.Compiler)
		assert.Equal(t, "xelatex", config.Documents[0].Compiler)
		assert.Equal(t, "xelatex", config.Documents[1].Compiler)
	})

	t.Run("per-document compiler overrides top-level", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
compiler: xelatex
documents:
  - name: paper
    main: paper.tex
  - name: slides
    main: slides.tex
    compiler: lualatex
`)
		require.NoError(t, err)
		assert.Equal(t, "xelatex", config.Compiler)
		assert.Equal(t, "xelatex", config.Documents[0].Compiler)
		assert.Equal(t, "lualatex", config.Documents[1].Compiler)
	})

	t.Run("all valid compilers are accepted at top level", func(t *testing.T) {
		for _, compiler := range cli.AllowedCompilers {
			config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
compiler: ` + compiler + `
documents:
  - name: paper
    main: paper.tex
`)
			require.NoError(t, err, "compiler %q should be accepted", compiler)
			assert.Equal(t, compiler, config.Compiler)
			assert.Equal(t, compiler, config.Documents[0].Compiler)
		}
	})

	t.Run("all valid compilers are accepted per document", func(t *testing.T) {
		for _, compiler := range cli.AllowedCompilers {
			config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    compiler: ` + compiler + `
`)
			require.NoError(t, err, "compiler %q should be accepted", compiler)
			assert.Equal(t, compiler, config.Documents[0].Compiler)
		}
	})

	t.Run("rejects invalid top-level compiler", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
compiler: mslatex
documents:
  - name: paper
    main: paper.tex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown compiler")
		assert.Contains(t, err.Error(), "mslatex")
	})

	t.Run("rejects invalid per-document compiler", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    compiler: badtex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown compiler")
		assert.Contains(t, err.Error(), "badtex")
	})

	t.Run("rejects non-string top-level compiler", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
compiler: 42
documents:
  - name: paper
    main: paper.tex
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "compiler must be a string")
	})

	t.Run("rejects non-string per-document compiler", func(t *testing.T) {
		_, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
    compiler: 42
`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "'compiler' must be a string")
	})
}

func TestParseConfigProjectKey(t *testing.T) {
	t.Run("parses project_key when present", func(t *testing.T) {
		config, err := cli.ParseConfig(`
project_key: "k7Gx9mR2pL4wN8qY5vBt3a"
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "k7Gx9mR2pL4wN8qY5vBt3a", config.ProjectKey)
	})

	t.Run("project_key is empty when absent", func(t *testing.T) {
		config, err := cli.ParseConfig(`
distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`)
		require.NoError(t, err)
		assert.Equal(t, "", config.ProjectKey)
	})
}

func TestGenerateProjectKey(t *testing.T) {
	t.Run("returns 22-char base62 string", func(t *testing.T) {
		key, err := cli.GenerateProjectKey()
		require.NoError(t, err)
		assert.Len(t, key, 22)
		for _, c := range key {
			assert.True(t, (c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'),
				"character %q is not base62", c)
		}
	})

	t.Run("generates unique keys", func(t *testing.T) {
		key1, err := cli.GenerateProjectKey()
		require.NoError(t, err)
		key2, err := cli.GenerateProjectKey()
		require.NoError(t, err)
		assert.NotEqual(t, key1, key2)
	})
}

func TestGenerateConfigYAML(t *testing.T) {
	t.Run("includes project_key in output", func(t *testing.T) {
		docs := []cli.Document{
			{Name: "paper", Main: "paper.tex"},
		}
		content := cli.GenerateConfigYAML("testkey123", "texlive:2021", "pdflatex", docs)
		assert.Contains(t, content, `project_key: "testkey123"`)
		assert.Contains(t, content, `distribution_version: "texlive:2021"`)
		assert.Contains(t, content, `compiler: "pdflatex"`)
		assert.Contains(t, content, `name: "paper"`)
		assert.Contains(t, content, `main: "paper.tex"`)
	})

	t.Run("field ordering is project_key, distribution_version, compiler, documents", func(t *testing.T) {
		docs := []cli.Document{
			{Name: "paper", Main: "paper.tex"},
		}
		content := cli.GenerateConfigYAML("abc123", "texlive:2021", "pdflatex", docs)
		pkIdx := strings.Index(content, "project_key:")
		dvIdx := strings.Index(content, "distribution_version:")
		compIdx := strings.Index(content, "compiler:")
		docsIdx := strings.Index(content, "documents:")
		assert.Greater(t, dvIdx, pkIdx, "project_key should appear before distribution_version")
		assert.Greater(t, compIdx, dvIdx, "compiler should appear after distribution_version")
		assert.Greater(t, docsIdx, compIdx, "documents should appear after compiler")
	})
}

func TestDocumentByName(t *testing.T) {
	config := cli.Config{
		DistributionVersion: "texlive:2021",
		Documents: []cli.Document{
			{Name: "paper", Main: "paper.tex", Output: "paper.pdf", DistributionVersion: "texlive:2021"},
			{Name: "slides", Main: "slides.tex", Output: "slides.pdf", DistributionVersion: "texlive:2021"},
		},
	}

	t.Run("finds existing document", func(t *testing.T) {
		doc, ok := config.DocumentByName("paper")
		assert.True(t, ok)
		assert.Equal(t, "paper.tex", doc.Main)
	})

	t.Run("returns false for missing document", func(t *testing.T) {
		_, ok := config.DocumentByName("nonexistent")
		assert.False(t, ok)
	})
}

func TestLoadConfig(t *testing.T) {
	t.Run("loads config from directory", func(t *testing.T) {
		dir := t.TempDir()
		content := `distribution_version: "texlive:2021"
documents:
  - name: paper
    main: paper.tex
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".texops.yaml"), []byte(content), 0600))

		config, err := cli.LoadConfig(dir)
		require.NoError(t, err)
		assert.Equal(t, "texlive:2021", config.DistributionVersion)
		require.Len(t, config.Documents, 1)
		assert.Equal(t, "paper.tex", config.Documents[0].Main)
	})

	t.Run("returns error when file missing", func(t *testing.T) {
		_, err := cli.LoadConfig(t.TempDir())
		require.Error(t, err)
	})
}

func TestOldFormatWithProjectIDProducesMigrationError(t *testing.T) {
	t.Run("old format with project_id and main triggers migration error", func(t *testing.T) {
		content := `distribution_version: "texlive:2021"
main: paper.tex
project_id: prj_abc123
`
		_, err := cli.ParseConfig(content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config format has changed")
		assert.Contains(t, err.Error(), "documents")
	})

	t.Run("old format with only main (no project_id) triggers migration error", func(t *testing.T) {
		content := `distribution_version: "texlive:2021"
main: paper.tex
`
		_, err := cli.ParseConfig(content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config format has changed")
	})

	t.Run("old format with main and output triggers migration error", func(t *testing.T) {
		content := `distribution_version: "texlive:2021"
main: paper.tex
output: result.pdf
`
		_, err := cli.ParseConfig(content)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config format has changed")
	})
}
