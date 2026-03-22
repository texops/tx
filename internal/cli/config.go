package cli

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const base62Charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// AllowedCompilers lists valid compiler values for .texops.yaml.
var AllowedCompilers = []string{"pdflatex", "xelatex", "lualatex", "latex", "platex", "uplatex"}

const defaultCompiler = "pdflatex"

// TexliveVersions lists available TexLive distribution versions (newest first).
var TexliveVersions = []string{"texlive:2021", "texlive:2019", "texlive:2017", "texlive:2015", "texlive:2013"}

// generateProjectKey returns a 22-character random base62 string.
func generateProjectKey() (string, error) {
	b := make([]byte, 22)
	max := big.NewInt(int64(len(base62Charset)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("failed to generate project key: %w", err)
		}
		b[i] = base62Charset[n.Int64()]
	}
	return string(b), nil
}

type Document struct {
	Name      string `yaml:"name"`
	Main      string `yaml:"main"`
	Directory string `yaml:"directory,omitempty"`
	Output    string `yaml:"output,omitempty"`
	Texlive   string `yaml:"texlive,omitempty"`
	Compiler  string `yaml:"compiler,omitempty"`
}

type Config struct {
	ProjectKey string     `yaml:"project_key,omitempty"`
	Texlive    string     `yaml:"texlive"`
	Compiler   string     `yaml:"compiler,omitempty"`
	APIURL     string     `yaml:"api_url,omitempty"`
	Documents  []Document `yaml:"documents"`
}

func ParseConfig(content string) (Config, error) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return Config{}, fmt.Errorf("invalid config: expected a YAML mapping")
	}
	if raw == nil {
		return Config{}, fmt.Errorf("invalid config: expected a YAML mapping")
	}

	// Detect old-style config with top-level 'main' field
	if _, hasMain := raw["main"]; hasMain {
		return Config{}, fmt.Errorf("config format has changed: replace top-level 'main' with a 'documents' list (see https://api.texops.dev/docs/config)")
	}

	topCompiler := defaultCompiler
	if compRaw, ok := raw["compiler"]; ok && compRaw != nil {
		compStr, ok := compRaw.(string)
		if !ok {
			return Config{}, fmt.Errorf("invalid config: compiler must be a string")
		}
		if compStr != "" {
			if !isValidCompiler(compStr) {
				return Config{}, fmt.Errorf("invalid config: unknown compiler %q (allowed: %s)", compStr, strings.Join(AllowedCompilers, ", "))
			}
			topCompiler = compStr
		}
	}

	tlRaw, ok := raw["texlive"]
	if !ok || tlRaw == nil {
		return Config{}, fmt.Errorf("invalid config: missing required field 'texlive'")
	}
	tl, ok := tlRaw.(string)
	if !ok || tl == "" {
		return Config{}, fmt.Errorf("invalid config: texlive must be a string")
	}

	docsRaw, ok := raw["documents"]
	if !ok || docsRaw == nil {
		return Config{}, fmt.Errorf("invalid config: missing required field 'documents'")
	}
	docsList, ok := docsRaw.([]any)
	if !ok {
		return Config{}, fmt.Errorf("invalid config: 'documents' must be a list")
	}
	if len(docsList) == 0 {
		return Config{}, fmt.Errorf("invalid config: 'documents' must be a non-empty list")
	}

	seen := make(map[string]bool)
	var documents []Document
	for i, item := range docsList {
		docMap, ok := item.(map[string]any)
		if !ok {
			return Config{}, fmt.Errorf("invalid config: documents[%d] must be a mapping", i)
		}

		nameRaw, ok := docMap["name"]
		if !ok || nameRaw == nil {
			return Config{}, fmt.Errorf("invalid config: documents[%d] missing required field 'name'", i)
		}
		name, ok := nameRaw.(string)
		if !ok || name == "" {
			return Config{}, fmt.Errorf("invalid config: documents[%d] 'name' must be a non-empty string", i)
		}
		if seen[name] {
			return Config{}, fmt.Errorf("invalid config: duplicate document name %q", name)
		}
		seen[name] = true

		mainRaw, ok := docMap["main"]
		if !ok || mainRaw == nil {
			return Config{}, fmt.Errorf("invalid config: documents[%d] missing required field 'main'", i)
		}
		mainStr, ok := mainRaw.(string)
		if !ok || mainStr == "" {
			return Config{}, fmt.Errorf("invalid config: documents[%d] 'main' must be a non-empty string", i)
		}
		if escapesRoot(mainStr) {
			return Config{}, fmt.Errorf("invalid config: documents[%d] 'main' must not escape project directory", i)
		}

		var directory string
		if dirRaw, ok := docMap["directory"]; ok && dirRaw != nil {
			dirStr, ok := dirRaw.(string)
			if !ok {
				return Config{}, fmt.Errorf("invalid config: documents[%d] 'directory' must be a string", i)
			}
			if dirStr != "" {
				if escapesRoot(dirStr) {
					return Config{}, fmt.Errorf("invalid config: documents[%d] 'directory' must not escape project directory", i)
				}
				// Normalize "." (and equivalents) to empty, consistent
				// with DiscoverDocuments which never produces ".".
				cleaned := filepath.Clean(dirStr)
				if cleaned != "." {
					directory = cleaned
				}
			}
		}

		output := filepath.Join(directory, defaultOutput(mainStr))
		if outputRaw, ok := docMap["output"]; ok && outputRaw != nil {
			outputStr, ok := outputRaw.(string)
			if !ok {
				return Config{}, fmt.Errorf("invalid config: documents[%d] 'output' must be a string", i)
			}
			if outputStr != "" {
				output = filepath.Clean(outputStr)
			}
		}
		if escapesRoot(output) {
			return Config{}, fmt.Errorf("invalid config: documents[%d] 'output' must not escape project directory", i)
		}

		docTL := tl
		if tlRaw, ok := docMap["texlive"]; ok && tlRaw != nil {
			tlStr, ok := tlRaw.(string)
			if !ok {
				return Config{}, fmt.Errorf("invalid config: documents[%d] 'texlive' must be a string", i)
			}
			if tlStr != "" {
				docTL = tlStr
			}
		}

		docCompiler := topCompiler
		if compRaw, ok := docMap["compiler"]; ok && compRaw != nil {
			compStr, ok := compRaw.(string)
			if !ok {
				return Config{}, fmt.Errorf("invalid config: documents[%d] 'compiler' must be a string", i)
			}
			if compStr != "" {
				if !isValidCompiler(compStr) {
					return Config{}, fmt.Errorf("invalid config: documents[%d] unknown compiler %q (allowed: %s)", i, compStr, strings.Join(AllowedCompilers, ", "))
				}
				docCompiler = compStr
			}
		}

		documents = append(documents, Document{
			Name:      name,
			Main:      mainStr,
			Directory: directory,
			Output:    output,
			Texlive:   docTL,
			Compiler:  docCompiler,
		})
	}

	var projectKey string
	if pkRaw, ok := raw["project_key"]; ok {
		if pkStr, ok := pkRaw.(string); ok {
			projectKey = pkStr
		}
	}

	var apiURL string
	if urlRaw, ok := raw["api_url"]; ok {
		if urlStr, ok := urlRaw.(string); ok {
			apiURL = urlStr
		}
	}

	return Config{
		ProjectKey: projectKey,
		Texlive:    tl,
		Compiler:   topCompiler,
		APIURL:     apiURL,
		Documents:  documents,
	}, nil
}

func isValidCompiler(compiler string) bool {
	return slices.Contains(AllowedCompilers, compiler)
}

// DocumentByName looks up a document by name.
func (c Config) DocumentByName(name string) (Document, bool) {
	for _, doc := range c.Documents {
		if doc.Name == name {
			return doc, true
		}
	}
	return Document{}, false
}

// escapesRoot reports whether a relative path would escape the project root.
func escapesRoot(rel string) bool {
	cleaned := filepath.Clean(rel)
	return filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func defaultOutput(main string) string {
	ext := filepath.Ext(main)
	if ext == "" {
		return main + ".pdf"
	}
	return strings.TrimSuffix(main, ext) + ".pdf"
}

func LoadConfig(dir string) (Config, error) {
	path := filepath.Join(dir, ".texops.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(string(data))
}
