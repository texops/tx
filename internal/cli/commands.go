package cli

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

var KeyringSet = keyring.Set

var OpenBrowser = openBrowser

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// PollInterval controls how often PollToken is called. Exposed for testing.
var PollInterval = 5 * time.Second

type Options struct {
	Login  LoginCmd  `command:"login" description:"Log in to TexOps"`
	Init   InitCmd   `command:"init" description:"Initialize a new TexOps project"`
	Build  BuildCmd  `command:"build" description:"Build the LaTeX project"`
	Status StatusCmd `command:"status" description:"Show authentication status"`
	Token  TokenCmd  `command:"token" description:"Manage API tokens"`
}

type TokenCmd struct {
	Create TokenCreateCmd `command:"create" description:"Create a new API token"`
	List   TokenListCmd   `command:"list" description:"List API tokens"`
	Delete TokenDeleteCmd `command:"delete" description:"Delete an API token"`
}

type TokenCreateCmd struct {
	Name      string `long:"name" description:"Name for the token"`
	ExpiresIn string `long:"expires-in" description:"Token expiry duration (e.g. 30d, 90d, 1y)"`
	NoExpiry  bool   `long:"no-expiry" description:"Create token with no expiry"`
	UI        *UI    `no-flag:"true"`
}

type TokenListCmd struct {
	UI *UI `no-flag:"true"`
}

type TokenDeleteCmd struct {
	UI *UI `no-flag:"true"`
}

type StatusCmd struct {
	UI *UI `no-flag:"true"`
}

type LoginCmd struct {
	UI *UI `no-flag:"true"`
}

type InitCmd struct {
	DistVersion string `long:"dist-version" default:"texlive:2021" description:"TexLive distribution version"`
	Compiler    string `long:"compiler" default:"pdflatex" description:"LaTeX compiler (pdflatex, xelatex, lualatex, latex, platex, uplatex)"`
	Main        string `long:"main" default:"main.tex" description:"Main TeX file (fallback when no .tex files discovered)"`
	UI          *UI    `no-flag:"true"`
}

type BuildCmd struct {
	Args    struct{ Names []string } `positional-args:"true"`
	NoCache bool                     `long:"no-cache" description:"Clear build cache and rebuild from scratch"`
	Yes     bool                     `short:"y" long:"yes" description:"Skip upload size confirmation prompt"`
	UI      *UI                      `no-flag:"true"`
}

var NewInstanceClientFn = func(instanceURL, jwt string) *InstanceClient {
	return NewInstanceClient(instanceURL, jwt)
}

var RunBuild = runBuild

// docResult tracks the outcome of building a single document.
type docResult struct {
	Name    string
	Output  string
	Success bool
	Err     error
}

func defaultUI() *UI {
	return NewUI(os.Stdout)
}

// formatDate parses an RFC3339 string and formats it as "02 Jan 2006".
// Returns the raw string on parse failure.
func formatDate(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format("02 Jan 2006")
	}
	return s
}

// formatDatePtr returns fallback when s is nil, otherwise delegates to formatDate.
func formatDatePtr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return formatDate(*s)
}

func (cmd *StatusCmd) Execute(args []string) error {
	ui := cmd.UI
	if ui == nil {
		ui = defaultUI()
	}

	token, err := ResolveAuth()
	if err != nil {
		ui.Errorf("Not authenticated.")
		ui.Log("")
		ui.Log("Run 'tx login' to log in to TexOps.")
		return nil
	}

	apiURL := ResolveAPIURL(Config{})
	api := NewAPIClient(apiURL, token)

	sp := ui.Spin("Checking authentication...")
	resp, err := api.Whoami()
	if err != nil {
		sp.Fail("Authentication check failed")
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 401 {
			ui.Errorf("Session expired. Run 'tx login' to re-authenticate.")
			return nil
		}
		ui.Errorf("Error: %s", err)
		return err
	}
	sp.Stop("Connected")

	ui.Success("Authenticated")
	if resp.Email != "" {
		ui.Log(fmt.Sprintf("Email:   %s", resp.Email))
	}

	methodLabel := resp.AuthMethod
	if resp.AuthMethod == "api_token" {
		methodLabel = "API token"
	}
	ui.Log(fmt.Sprintf("Method:  %s", methodLabel))
	ui.Log("Expires: " + formatDatePtr(resp.ExpiresAt, "never"))

	return nil
}

func (cmd *LoginCmd) Execute(args []string) error {
	ui := cmd.UI
	if ui == nil {
		ui = defaultUI()
	}

	apiURL := ResolveAPIURL(Config{})
	api := NewUnauthenticatedAPIClient(apiURL)

	// Step 1: Request device code
	sp := ui.Spin("Requesting login code...")
	dcResp, err := api.RequestDeviceCode()
	if err != nil {
		sp.Fail(fmt.Sprintf("Failed to request login code: %s", err))
		return err
	}
	sp.Stop("Login code received")

	// Step 2: Show user code and verification URL
	ui.Status(fmt.Sprintf("Your login code: %s", dcResp.UserCode))
	ui.DimInfo(fmt.Sprintf("Open %s and enter the code above", dcResp.VerificationURL))

	// Try to open browser
	if err := OpenBrowser(dcResp.VerificationURL); err != nil {
		ui.DimInfo("Could not open browser automatically")
	}

	// Step 3: Poll for authorization
	sp = ui.Spin("Waiting for authorization...")
	deadline := time.Now().Add(time.Duration(dcResp.ExpiresIn) * time.Second)

	var tokenResp TokenResponse
	for {
		if time.Now().After(deadline) {
			sp.Fail("Login code expired")
			return fmt.Errorf("login code expired; please run 'tx login' again")
		}

		time.Sleep(PollInterval)

		tokenResp, err = api.PollToken(dcResp.DeviceCode)
		if err == nil {
			break
		}
		if errors.Is(err, ErrAuthorizationPending) {
			continue
		}
		if errors.Is(err, ErrDeviceCodeExpired) {
			sp.Fail("Login code expired")
			return fmt.Errorf("login code expired; please run 'tx login' again")
		}
		sp.Fail(fmt.Sprintf("Token polling failed: %s", err))
		return err
	}
	sp.Stop("Authorized")

	// Step 4: Store JWT
	if err := storeJWT(tokenResp.JWT); err != nil {
		return err
	}

	ui.Success("Logged in successfully")
	return nil
}

// DiscoverDocuments recursively finds .tex files containing \documentclass
// in dir, respecting .gitignore and .txignore patterns. Returns a Document
// for each discovered file with name derived from the path stem.
func DiscoverDocuments(dir string) ([]Document, error) {
	ig := loadGitignore(dir)
	txignoreCache := make(map[string]*txignoreEntry)

	if entry, err := loadTxignore(dir); err != nil {
		return nil, err
	} else if entry != nil {
		txignoreCache["."] = entry
	}

	var candidates []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		checkPath := rel
		if info.IsDir() {
			checkPath = rel + "/"
		}

		if ig.MatchesPath(checkPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if matchesTxignore(txignoreCache, rel, info.IsDir()) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			entry, loadErr := loadTxignore(filepath.Join(dir, rel))
			if loadErr != nil {
				return loadErr
			}
			if entry != nil {
				txignoreCache[rel] = entry
			}
			return nil
		}

		if !info.Mode().IsRegular() {
			return nil
		}
		if filepath.Ext(rel) != ".tex" {
			return nil
		}

		if hasDocumentclass(filepath.Join(dir, rel)) {
			candidates = append(candidates, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Derive unique names using a two-pass approach:
	// Pass 1: Compute natural stems and count duplicates. Record all natural stems
	// so that suffix generation never steals a name that belongs to another file's
	// natural stem (even if that stem is itself duplicated).
	// Pass 2: Assign names — unique stems keep their name directly; for duplicate
	// stems, the first occurrence keeps the base name and subsequent ones get
	// suffixed names, skipping any naturally-occurring stem.
	stems := make([]string, len(candidates))
	stemCount := make(map[string]int)
	for i, rel := range candidates {
		stems[i] = deriveDocumentName(rel)
		stemCount[stems[i]]++
	}

	// naturalStems records every stem that appears as a file's natural name.
	// Suffix generation must never produce a name in this set (unless it's the
	// stem being suffixed, which is already handled by the taken map).
	naturalStems := make(map[string]bool, len(stems))
	for _, stem := range stems {
		naturalStems[stem] = true
	}

	// taken tracks names that have been assigned to a document.
	taken := make(map[string]bool)
	// Pre-assign unique stems so they are claimed before suffix generation runs.
	for _, stem := range stems {
		if stemCount[stem] == 1 {
			taken[stem] = true
		}
	}

	docs := make([]Document, len(candidates))
	for i, rel := range candidates {
		name := stems[i]
		if stemCount[name] > 1 {
			// Duplicate stem: first occurrence keeps the base name, rest get suffixes.
			if !taken[name] {
				taken[name] = true
			} else {
				base := name
				for n := 2; ; n++ {
					name = fmt.Sprintf("%s_%d", base, n)
					if !taken[name] && !naturalStems[name] {
						break
					}
				}
				taken[name] = true
			}
		}
		docDir := filepath.Dir(rel)
		docMain := filepath.Base(rel)
		if docDir == "." {
			docDir = ""
		}
		docs[i] = Document{
			Name:      name,
			Main:      docMain,
			Directory: docDir,
		}
	}

	return docs, nil
}

// hasDocumentclass checks if a .tex file contains \documentclass in its first 50 lines.
func hasDocumentclass(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line length
	for i := 0; i < 50 && scanner.Scan(); i++ {
		if strings.Contains(scanner.Text(), `\documentclass`) {
			return true
		}
	}
	return false
}

// deriveDocumentName derives a document name from a relative path.
// e.g., "paper.tex" -> "paper", "slides/slides.tex" -> "slides".
func deriveDocumentName(relPath string) string {
	base := filepath.Base(relPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (cmd *InitCmd) Execute(args []string) error {
	ui := cmd.UI
	if ui == nil {
		ui = defaultUI()
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	if !isValidCompiler(cmd.Compiler) {
		return fmt.Errorf("invalid compiler %q: allowed values are %s", cmd.Compiler, strings.Join(AllowedCompilers, ", "))
	}

	configPath := filepath.Join(dir, ".texops.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf(".texops.yaml already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot check .texops.yaml: %w", err)
	}

	return initProject(dir, cmd.DistVersion, cmd.Compiler, cmd.Main, ui)
}

func initProject(dir, distVersion, compiler, mainFallback string, ui *UI) error {
	configPath := filepath.Join(dir, ".texops.yaml")

	discovered, err := DiscoverDocuments(dir)
	if err != nil {
		return fmt.Errorf("failed to discover documents: %w", err)
	}

	var selected []Document
	if len(discovered) > 0 {
		if ui.IsTTY() {
			selected, err = ui.SelectDocuments(discovered)
			if err != nil {
				return fmt.Errorf("document selection failed: %w", err)
			}
			if len(selected) == 0 {
				return fmt.Errorf("no documents selected")
			}
		} else {
			selected = discovered
		}
	} else {
		name := deriveDocumentName(mainFallback)
		selected = []Document{
			{Name: name, Main: mainFallback},
		}
	}

	projectKey, err := generateProjectKey()
	if err != nil {
		return err
	}

	configContent := generateConfigYAML(projectKey, distVersion, compiler, selected)
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		return fmt.Errorf("failed to write .texops.yaml: %w", err)
	}

	if len(discovered) > 0 {
		ui.Success(fmt.Sprintf("Created .texops.yaml with %d document(s)", len(selected)))
	} else {
		ui.Success("Created .texops.yaml")
	}
	return nil
}

// generateConfigYAML produces .texops.yaml content from a project key, distribution version, compiler, and documents.
func generateConfigYAML(projectKey, distVersion, compiler string, docs []Document) string {
	var b strings.Builder
	fmt.Fprintf(&b, "project_key: %q\n", projectKey)
	fmt.Fprintf(&b, "distribution_version: %q\n", distVersion)
	fmt.Fprintf(&b, "compiler: %q\n", compiler)
	fmt.Fprintf(&b, "\ndocuments:\n")
	for _, doc := range docs {
		fmt.Fprintf(&b, "  - name: %q\n    main: %q\n", doc.Name, doc.Main)
		if doc.Directory != "" {
			fmt.Fprintf(&b, "    directory: %q\n", doc.Directory)
		}
	}
	return b.String()
}

func (cmd *BuildCmd) Execute(args []string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	ui := cmd.UI
	if ui == nil {
		ui = defaultUI()
	}
	return RunBuild(dir, cmd.Args.Names, cmd.NoCache, cmd.Yes, ui)
}

var errInitDeclined = errors.New("no project config found; run `tx init` to set up your project")

func runBuild(dir string, names []string, noCache bool, skipConfirm bool, ui *UI) error {
	buildStart := time.Now()

	configPath := filepath.Join(dir, ".texops.yaml")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if !ui.IsTTY() {
			return errInitDeclined
		}
		confirmed, confirmErr := ui.Confirm("No .texops.yaml found. Initialize project now?")
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return errInitDeclined
		}
		if initErr := initProject(dir, "texlive:2021", "pdflatex", "main.tex", ui); initErr != nil {
			return initErr
		}
		configData, err = os.ReadFile(configPath)
		if err != nil {
			return err
		}
	}

	config, err := ParseConfig(string(configData))
	if err != nil {
		return err
	}

	// Resolve document list: filter by name or use all
	var docs []Document
	if len(names) > 0 {
		for _, name := range names {
			doc, ok := config.DocumentByName(name)
			if !ok {
				return fmt.Errorf("unknown document %q; available: %s", name, docNames(config.Documents))
			}
			docs = append(docs, doc)
		}
	} else {
		docs = config.Documents
	}

	token, err := ResolveAuth()
	if err != nil {
		return err
	}

	api := NewAPIClient(ResolveAPIURL(config), token)

	if config.ProjectKey == "" {
		key, err := generateProjectKey()
		if err != nil {
			return fmt.Errorf("generating project_key: %w", err)
		}
		line := fmt.Sprintf("project_key: %q\n", key)
		var updated []byte
		if bytes.HasPrefix(configData, []byte("---\n")) {
			// Insert after YAML document marker to keep valid single-document YAML
			marker := []byte("---\n")
			updated = append(marker, append([]byte(line), configData[len(marker):]...)...)
		} else {
			updated = append([]byte(line), configData...)
		}
		if err := os.WriteFile(configPath, updated, 0o644); err != nil {
			return fmt.Errorf("updating .texops.yaml with project_key: %w", err)
		}
		config.ProjectKey = key
	}

	sp := ui.Spin("Resolving project...")
	project, err := api.CreateProject(filepath.Base(dir), config.DistributionVersion, config.ProjectKey)
	if err != nil {
		sp.Fail(fmt.Sprintf("Failed to resolve project: %s", err))
		return err
	}
	sp.Stop("Project ready")
	projectID := project.ID

	// Collect files once (shared across all version groups)
	sp = ui.Spin("Collecting files...")
	files, err := CollectFiles(dir)
	if err != nil {
		sp.Fail(fmt.Sprintf("Failed to collect files: %s", err))
		return err
	}
	totalSize := TotalSize(files)
	sp.Stop(fmt.Sprintf("Found %d files (%s)", len(files), FormatSize(totalSize)))

	// Group documents by effective distribution_version
	type versionGroup struct {
		version string
		docs    []Document
	}
	groupMap := make(map[string]*versionGroup)
	var groupOrder []string
	for _, doc := range docs {
		v := doc.DistributionVersion
		if _, ok := groupMap[v]; !ok {
			groupMap[v] = &versionGroup{version: v}
			groupOrder = append(groupOrder, v)
		}
		groupMap[v].docs = append(groupMap[v].docs, doc)
	}

	var results []docResult

	for _, version := range groupOrder {
		group := groupMap[version]

		// Get session for this version group
		sp = ui.Spin("Getting session...")
		session, err := api.GetSession(projectID, version)
		if err != nil {
			sp.Fail(fmt.Sprintf("Failed to get session: %s", err))
			// Mark all docs in this group as failed
			for _, doc := range group.docs {
				results = append(results, docResult{Name: doc.Name, Output: doc.Output, Err: err})
			}
			continue
		}
		sp.Stop("Session acquired")

		inst := NewInstanceClientFn(session.InstanceURL, session.JWT)

		// Sync files once per version group
		sp = ui.Spin("Syncing with instance...")
		syncResult, err := inst.Sync(projectID, files)
		if err != nil {
			sp.Fail(fmt.Sprintf("Sync failed: %s", err))
			for _, doc := range group.docs {
				results = append(results, docResult{Name: doc.Name, Output: doc.Output, Err: err})
			}
			continue
		}

		if err := handleUpload(ui, inst, projectID, dir, files, syncResult, skipConfirm, sp); err != nil {
			// User cancellation should abort the entire build
			if strings.Contains(err.Error(), "cancelled by user") {
				return err
			}
			for _, doc := range group.docs {
				results = append(results, docResult{Name: doc.Name, Output: doc.Output, Err: err})
			}
			continue
		}

		// Build each document in this group
		for _, doc := range group.docs {
			r := buildDocument(ui, inst, projectID, dir, doc, noCache)
			results = append(results, r)
		}
	}

	// Print build summary
	succeeded := 0
	failed := 0
	for _, r := range results {
		if r.Success {
			succeeded++
		} else {
			failed++
		}
	}
	elapsed := time.Since(buildStart)
	ui.Log("")
	ui.Status(fmt.Sprintf("Build complete: %d succeeded, %d failed (%.1fs)", succeeded, failed, elapsed.Seconds()))
	for _, r := range results {
		if r.Success {
			ui.Log(fmt.Sprintf("  %s => %s", r.Name, r.Output))
		} else {
			ui.Log(fmt.Sprintf("  %s !! FAILED", r.Name))
		}
	}

	// Return error if any document failed
	for _, r := range results {
		if !r.Success {
			return fmt.Errorf("one or more documents failed to build")
		}
	}

	return nil
}

// handleUpload processes file sync results and uploads missing files.
func handleUpload(ui *UI, inst *InstanceClient, projectID, dir string, files []FileEntry, syncResult SyncResult, skipConfirm bool, sp *Spinner) error {
	if len(syncResult.Missing) > 0 {
		knownPaths := make(map[string]bool)
		filesByPath := make(map[string]FileEntry)
		for _, f := range files {
			knownPaths[f.Path] = true
			filesByPath[f.Path] = f
		}
		var validMissing []string
		for _, p := range syncResult.Missing {
			if knownPaths[p] {
				validMissing = append(validMissing, p)
			}
		}
		if len(validMissing) != len(syncResult.Missing) {
			ui.DimInfo("Warning: server requested files outside the project; ignoring unknown paths")
		}

		var uploadFiles []FileEntry
		for _, p := range validMissing {
			if f, ok := filesByPath[p]; ok {
				uploadFiles = append(uploadFiles, f)
			}
		}
		uploadSize := TotalSize(uploadFiles)

		sp.Stop(fmt.Sprintf("%d files to upload (%s)", len(validMissing), FormatSize(uploadSize)))

		const sizeThreshold = 50_000_000
		if uploadSize > sizeThreshold && !skipConfirm {
			confirmed, err := ui.Confirm(fmt.Sprintf("Upload size is %s. Continue?", FormatSize(uploadSize)))
			if err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("upload cancelled by user")
			}
		}

		uploadLabel := fmt.Sprintf("Uploading %d files", len(validMissing))
		pb := ui.Progress(uploadLabel, uploadSize)
		if err := inst.Upload(projectID, dir, validMissing, func(sent, total int64) {
			if total > 0 {
				pb.Update(float64(sent) / float64(total))
			}
		}); err != nil {
			pb.Abort()
			ui.Errorf("Upload failed: %s", err)
			return err
		}
		pb.Done()
	} else {
		sp.Stop("All files up to date")
	}
	return nil
}

// buildDocument builds a single document and returns the result.
func buildDocument(ui *UI, inst *InstanceClient, projectID, dir string, doc Document, noCache bool) docResult {
	displayMain := doc.Main
	if doc.Directory != "" {
		displayMain = filepath.Join(doc.Directory, doc.Main)
	}
	ui.Status(fmt.Sprintf("Building %q (%s)...", doc.Name, displayMain))
	var buildOptions map[string]string
	if noCache {
		buildOptions = map[string]string{"no_cache": "true"}
	}
	compileStart := time.Now()
	result, err := inst.Build(projectID, doc.Main, doc.Directory, doc.DistributionVersion, doc.Compiler, buildOptions, func(line string) {
		ui.Log(line)
	})
	if err != nil {
		ui.Errorf("Build request failed: %s", err)
		return docResult{Name: doc.Name, Output: doc.Output, Err: err}
	}

	if result.Status == "success" && result.PdfURL != "" {
		compileElapsed := time.Since(compileStart)
		ui.Success(fmt.Sprintf("Build complete (%.1fs)", compileElapsed.Seconds()))
		outputPath := filepath.Join(dir, doc.Output)
		if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
			return docResult{Name: doc.Name, Output: doc.Output, Err: fmt.Errorf("cannot create output directory: %w", err)}
		}
		if result.BuildID == "" {
			return docResult{Name: doc.Name, Output: doc.Output, Err: fmt.Errorf("server did not return build_id in done event")}
		}

		sp := ui.Spin(fmt.Sprintf("Downloading %s...", doc.Output))
		if err := inst.DownloadPDF(projectID, result.BuildID, outputPath); err != nil {
			sp.Fail(fmt.Sprintf("Download failed: %s", err))
			return docResult{Name: doc.Name, Output: doc.Output, Err: err}
		}

		info, _ := os.Stat(outputPath)
		var sizeInfo string
		if info != nil {
			sizeInfo = fmt.Sprintf("%s (%s, %.1fs)", doc.Output, FormatSize(info.Size()), compileElapsed.Seconds())
		} else {
			sizeInfo = fmt.Sprintf("%s (%.1fs)", doc.Output, compileElapsed.Seconds())
		}
		sp.Stop(sizeInfo)
		return docResult{Name: doc.Name, Output: doc.Output, Success: true}
	}

	msg := result.Message
	if msg == "" {
		msg = "unknown error"
	}
	ui.Errorf("Build failed: %s", msg)
	return docResult{Name: doc.Name, Output: doc.Output, Err: fmt.Errorf("build failed: %s", msg)}
}

// docNames returns a comma-separated list of document names.
func docNames(docs []Document) string {
	names := make([]string, len(docs))
	for i, d := range docs {
		names[i] = d.Name
	}
	return strings.Join(names, ", ")
}

// parseDuration parses a duration string like "30d", "90d", "1y", "365d" into seconds.
func parseDuration(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	suffix := s[len(s)-1:]
	numStr := s[:len(s)-1]
	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("duration must be positive: %q", s)
	}
	const maxDays = 10 * 365 // 10 years
	switch suffix {
	case "d":
		if n > maxDays {
			return 0, fmt.Errorf("duration too large: %q (max %dd)", s, maxDays)
		}
		return n * 86400, nil
	case "y":
		if n > 10 {
			return 0, fmt.Errorf("duration too large: %q (max 10y)", s)
		}
		return n * 365 * 86400, nil
	default:
		return 0, fmt.Errorf("unsupported duration suffix %q (use 'd' for days or 'y' for years)", suffix)
	}
}

var expiryOptions = []struct {
	label   string
	seconds int64
}{
	{"30 days", 30 * 86400},
	{"90 days", 90 * 86400},
	{"1 year", 365 * 86400},
	{"No expiry", 0},
}

func (cmd *TokenCreateCmd) Execute(args []string) error {
	ui := cmd.UI
	if ui == nil {
		ui = defaultUI()
	}

	// Determine name
	cmd.Name = strings.TrimSpace(cmd.Name)
	if cmd.Name == "" {
		if !ui.IsTTY() {
			return fmt.Errorf("specify --name in non-interactive mode")
		}
		name, err := ui.TextInput("Token name:")
		if err != nil {
			return fmt.Errorf("failed to read token name: %w", err)
		}
		cmd.Name = name
	}

	// Determine expiry
	if cmd.NoExpiry && cmd.ExpiresIn != "" {
		return fmt.Errorf("--no-expiry and --expires-in are mutually exclusive")
	}
	var expiresIn *int64
	if cmd.NoExpiry {
		// Explicit no-expiry: leave expiresIn as nil
	} else if cmd.ExpiresIn != "" {
		seconds, err := parseDuration(cmd.ExpiresIn)
		if err != nil {
			return err
		}
		expiresIn = &seconds
	} else {
		if !ui.IsTTY() {
			return fmt.Errorf("specify --expires-in or --no-expiry in non-interactive mode")
		}
		// Interactive selection
		labels := make([]string, len(expiryOptions))
		for i, opt := range expiryOptions {
			labels[i] = opt.label
		}
		idx, err := ui.SelectOne("Token expiry:", labels)
		if err != nil {
			return fmt.Errorf("failed to select expiry: %w", err)
		}
		if expiryOptions[idx].seconds > 0 {
			s := expiryOptions[idx].seconds
			expiresIn = &s
		}
		// else: "No expiry" selected, leave expiresIn nil
	}

	token, err := ResolveAuth()
	if err != nil {
		return err
	}

	apiURL := ResolveAPIURL(Config{})
	api := NewAPIClient(apiURL, token)

	sp := ui.Spin("Creating token...")
	resp, err := api.CreateAPIToken(cmd.Name, expiresIn)
	if err != nil {
		sp.Fail("Failed to create token")
		if errors.Is(err, ErrTokenConflict) {
			ui.Errorf("A token named %q already exists.", cmd.Name)
			return err
		}
		ui.Errorf("Error: %s", err)
		return err
	}
	sp.Stop("Token created")

	ui.Log("")
	ui.Status(resp.Token)
	ui.Log("")
	ui.DimInfo("This token won't be shown again. Copy it now.")
	ui.DimInfo("Expires: " + formatDatePtr(resp.ExpiresAt, "never"))

	return nil
}

func (cmd *TokenListCmd) Execute(args []string) error {
	ui := cmd.UI
	if ui == nil {
		ui = defaultUI()
	}

	token, err := ResolveAuth()
	if err != nil {
		return err
	}

	apiURL := ResolveAPIURL(Config{})
	api := NewAPIClient(apiURL, token)

	sp := ui.Spin("Loading tokens...")
	tokens, err := api.ListAPITokens()
	if err != nil {
		sp.Fail("Failed to list tokens")
		ui.Errorf("Error: %s", err)
		return err
	}
	sp.Stop(fmt.Sprintf("%d token(s)", len(tokens)))

	if len(tokens) == 0 {
		ui.DimInfo("No tokens found. Create one with 'tx token create --name <name>'.")
		return nil
	}

	// Print table
	ui.Log(fmt.Sprintf("%-20s %-12s %-14s %-14s %-12s", "NAME", "PREFIX", "EXPIRES", "LAST USED", "CREATED"))
	for _, tok := range tokens {
		expires := formatDatePtr(tok.ExpiresAt, "never")
		lastUsed := formatDatePtr(tok.LastUsedAt, "never")
		created := formatDate(tok.CreatedAt)
		ui.Log(fmt.Sprintf("%-20s %-12s %-14s %-14s %-12s", tok.Name, tok.Prefix, expires, lastUsed, created))
	}

	return nil
}

func (cmd *TokenDeleteCmd) Execute(args []string) error {
	ui := cmd.UI
	if ui == nil {
		ui = defaultUI()
	}

	authToken, err := ResolveAuth()
	if err != nil {
		return err
	}

	if len(args) == 0 && !ui.IsTTY() {
		return fmt.Errorf("specify token name as argument in non-interactive mode")
	}

	apiURL := ResolveAPIURL(Config{})
	api := NewAPIClient(apiURL, authToken)

	sp := ui.Spin("Loading tokens...")
	tokens, err := api.ListAPITokens()
	if err != nil {
		sp.Fail("Failed to load tokens")
		ui.Errorf("Error listing tokens: %s", err)
		return err
	}
	sp.Stop(fmt.Sprintf("%d token(s)", len(tokens)))

	var tokenID string
	var tokenName string

	if len(args) > 0 {
		// Name provided as argument - look up the token by name
		tokenName = args[0]
		for _, tok := range tokens {
			if tok.Name == tokenName {
				tokenID = tok.ID
				break
			}
		}
		if tokenID == "" {
			return fmt.Errorf("token %q not found", tokenName)
		}
	} else {
		if len(tokens) == 0 {
			ui.DimInfo("No tokens to delete.")
			return nil
		}
		labels := make([]string, len(tokens))
		for i, tok := range tokens {
			labels[i] = fmt.Sprintf("%s (%s)", tok.Name, tok.Prefix)
		}
		idx, err := ui.Select("Select token to delete:", labels)
		if err != nil {
			return fmt.Errorf("failed to select token: %w", err)
		}
		tokenID = tokens[idx].ID
		tokenName = tokens[idx].Name
	}

	// Confirm deletion
	confirmed, err := ui.Confirm(fmt.Sprintf("Delete token %q?", tokenName))
	if err != nil {
		return err
	}
	if !confirmed {
		ui.DimInfo("Cancelled.")
		return nil
	}

	sp = ui.Spin("Deleting token...")
	err = api.DeleteAPIToken(tokenID)
	if err != nil {
		sp.Fail("Failed to delete token")
		if errors.Is(err, ErrTokenNotFound) {
			ui.Errorf("Token not found (may have been already deleted).")
			return err
		}
		ui.Errorf("Error: %s", err)
		return err
	}
	sp.Stop("Token deleted")

	return nil
}
