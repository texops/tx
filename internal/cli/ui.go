package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"
)

type uiStyles struct {
	status       lipgloss.Style
	success      lipgloss.Style
	logLine      lipgloss.Style
	errMsg       lipgloss.Style
	dimInfo      lipgloss.Style
	progressFrom string
	progressTo   string
}

type UI struct {
	isTTY      bool
	stdinIsTTY bool
	out        io.Writer
	ttyOut     io.Writer // original *os.File for Bubble Tea (needs Fd() for TTY detection)
	errOut     io.Writer
	in         io.Reader
	styles     uiStyles
}

func newStyles(hasDarkBG bool, r *lipgloss.Renderer) uiStyles {
	pick := func(light, dark string) string {
		if hasDarkBG {
			return dark
		}
		return light
	}
	newStyle := lipgloss.NewStyle
	if r != nil {
		newStyle = r.NewStyle
	}
	return uiStyles{
		status:       newStyle().Bold(true),
		success:      newStyle().Bold(true).Foreground(lipgloss.Color(pick("#007700", "#00CC00"))),
		logLine:      newStyle().Faint(true),
		errMsg:       newStyle().Bold(true).Foreground(lipgloss.Color(pick("#990000", "#CC0000"))),
		dimInfo:      newStyle().Faint(true),
		progressFrom: pick("#3A36B0", "#5A56E0"),
		progressTo:   pick("#BB4FD8", "#EE6FF8"),
	}
}

func NewUI(out io.Writer) *UI {
	isTTY := false
	if f, ok := out.(*os.File); ok {
		isTTY = isatty.IsTerminal(f.Fd())
	}
	stdinIsTTY := isatty.IsTerminal(os.Stdin.Fd())

	hasDarkBG := true // safe default for non-TTY
	if isTTY {
		hasDarkBG = lipgloss.HasDarkBackground()
	}

	return &UI{
		isTTY:      isTTY,
		stdinIsTTY: stdinIsTTY,
		out:        out,
		ttyOut:     out, // preserve original writer for Bubble Tea TTY detection
		errOut:     os.Stderr,
		in:         os.Stdin,
		styles:     newStyles(hasDarkBG, nil),
	}
}

// NewUIWithOptions creates a UI with explicit TTY mode and input reader.
// Used in tests to simulate TTY/non-TTY behavior.
// Error output goes to out for easy test capture.
func NewUIWithOptions(out io.Writer, isTTY bool, in io.Reader) *UI {
	return newUIWithOptions(out, isTTY, isTTY, in)
}

// NewUIWithTTYOptions creates a UI with separate output-TTY and stdin-TTY flags.
// Use this when testing code that distinguishes IsTTY() from IsInteractive().
func NewUIWithTTYOptions(out io.Writer, outIsTTY, stdinIsTTY bool, in io.Reader) *UI {
	return newUIWithOptions(out, outIsTTY, stdinIsTTY, in)
}

func newUIWithOptions(out io.Writer, isTTY, stdinIsTTY bool, in io.Reader) *UI {
	r := lipgloss.NewRenderer(out)
	r.SetColorProfile(termenv.TrueColor)
	return &UI{
		isTTY:      isTTY,
		stdinIsTTY: stdinIsTTY,
		out:        out,
		ttyOut:     out,
		errOut:     out,
		in:         in,
		styles:     newStyles(true, r),
	}
}

func (ui *UI) IsTTY() bool {
	return ui.isTTY
}

func (ui *UI) IsInteractive() bool {
	return ui.isTTY && ui.stdinIsTTY
}

func (ui *UI) Out() io.Writer {
	return ui.out
}

func (ui *UI) Status(msg string) {
	if ui.isTTY {
		fmt.Fprintln(ui.out, ui.styles.status.Render(msg))
	} else {
		fmt.Fprintln(ui.out, msg)
	}
}

func (ui *UI) Success(msg string) {
	if ui.isTTY {
		fmt.Fprintln(ui.out, ui.styles.success.Render("✓ "+msg))
	} else {
		fmt.Fprintln(ui.out, msg)
	}
}

func (ui *UI) Log(line string) {
	if ui.isTTY {
		fmt.Fprintln(ui.out, ui.styles.logLine.Render("    "+line))
	} else {
		fmt.Fprintln(ui.out, "    "+line)
	}
}

func (ui *UI) Errorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if ui.isTTY {
		fmt.Fprintln(ui.errOut, ui.styles.errMsg.Render(msg))
	} else {
		fmt.Fprintln(ui.errOut, msg)
	}
}

func (ui *UI) DimInfo(msg string) {
	if ui.isTTY {
		fmt.Fprintln(ui.out, ui.styles.dimInfo.Render(msg))
	} else {
		fmt.Fprintln(ui.out, msg)
	}
}

// Select displays an interactive selection list. In TTY mode, uses a simple numbered
// list with keyboard input. In non-TTY mode, returns an error.
func (ui *UI) Select(label string, options []string) (int, error) {
	if !ui.isTTY {
		return -1, fmt.Errorf("interactive selection requires a terminal (TTY)")
	}

	fmt.Fprintln(ui.out, label)
	for i, opt := range options {
		fmt.Fprintf(ui.out, "  %d) %s\n", i+1, opt)
	}
	fmt.Fprintf(ui.out, "Choose [1-%d]: ", len(options))

	scanner := bufio.NewScanner(ui.in)
	if scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		var choice int
		if _, err := fmt.Sscanf(text, "%d", &choice); err != nil {
			return -1, fmt.Errorf("invalid selection: %q", text)
		}
		if choice < 1 || choice > len(options) {
			return -1, fmt.Errorf("selection out of range: %d", choice)
		}
		return choice - 1, nil
	}
	if err := scanner.Err(); err != nil {
		return -1, fmt.Errorf("failed to read input: %w", err)
	}
	return -1, fmt.Errorf("no input received")
}

func (ui *UI) Confirm(msg string) (bool, error) {
	if !ui.isTTY {
		return true, nil
	}

	fmt.Fprintf(ui.out, "%s [Y/n] ", msg)

	scanner := bufio.NewScanner(ui.in)
	if scanner.Scan() {
		answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return answer == "" || answer == "y" || answer == "yes", nil
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("failed to read input: %w", err)
	}
	return false, nil
}

// Spinner represents an in-progress async operation with animated feedback.
type Spinner struct {
	ui      *UI
	program *tea.Program
	done    chan struct{}
}

// Spin starts an animated spinner with the given message.
// In non-TTY mode, it just prints the message.
// Call Stop() or Fail() on the returned Spinner to finish.
func (ui *UI) Spin(msg string) *Spinner {
	s := &Spinner{ui: ui, done: make(chan struct{})}

	if !ui.isTTY {
		fmt.Fprintln(ui.out, msg)
		return s
	}

	model := newSpinnerModel(msg, ui.styles)
	p := tea.NewProgram(
		model,
		tea.WithOutput(ui.ttyOut),
		tea.WithInput(nil),
	)
	s.program = p

	go func() {
		defer close(s.done)
		_, _ = p.Run()
	}()

	return s
}

// Stop ends the spinner and prints a success message.
func (s *Spinner) Stop(successMsg string) {
	if s.program != nil {
		s.program.Send(spinnerDoneMsg{
			text:  "✓ " + successMsg,
			style: s.ui.styles.success,
		})
		<-s.done
	} else {
		// non-TTY
		fmt.Fprintln(s.ui.out, successMsg)
	}
}

// Fail ends the spinner and prints an error message.
func (s *Spinner) Fail(errMsg string) {
	if s.program != nil {
		s.program.Send(spinnerDoneMsg{
			text:  errMsg,
			style: s.ui.styles.errMsg,
		})
		<-s.done
	} else {
		// non-TTY
		fmt.Fprintln(s.ui.errOut, errMsg)
	}
}

// Cancel stops the spinner without printing anything.
func (s *Spinner) Cancel() {
	if s.program != nil {
		s.program.Send(spinnerDoneMsg{cancelled: true})
		<-s.done
	}
}

// spinnerDoneMsg is sent to the bubbletea program to signal completion.
type spinnerDoneMsg struct {
	text      string
	style     lipgloss.Style
	cancelled bool
}

// spinnerModel is the bubbletea model for an inline spinner.
type spinnerModel struct {
	spinner  spinner.Model
	message  string
	styles   uiStyles
	finished bool
	finalMsg string
}

func newSpinnerModel(msg string, styles uiStyles) spinnerModel {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return spinnerModel{
		spinner: sp,
		message: msg,
		styles:  styles,
	}
}

func (m spinnerModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinnerDoneMsg:
		m.finished = true
		if !msg.cancelled {
			m.finalMsg = msg.style.Render(msg.text)
		}
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m spinnerModel) View() string {
	if m.finished {
		return m.finalMsg
	}
	return m.spinner.View() + " " + m.message
}

// progressReader wraps an io.Reader and tracks bytes read, calling onUpdate
// with the fraction (0.0-1.0) of total bytes consumed.
type progressReader struct {
	reader   io.Reader
	total    int64
	read     int64
	onUpdate func(fraction float64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	pr.read += int64(n)
	if pr.onUpdate != nil && pr.total > 0 {
		fraction := float64(pr.read) / float64(pr.total)
		if fraction > 1.0 {
			fraction = 1.0
		}
		pr.onUpdate(fraction)
	}
	return n, err
}

// ProgressBar represents an in-progress upload with byte-level tracking.
type ProgressBar struct {
	ui      *UI
	program *tea.Program
	done    chan struct{}
	total   int64
	label   string

	// Non-TTY milestone tracking
	mu            sync.Mutex
	lastMilestone int // last printed percentage milestone (25, 50, 75, 100)
}

// Progress starts an animated progress bar with the given label and total bytes.
// In non-TTY mode, it prints periodic percentage milestones.
// Call Reader() to wrap an io.Reader for tracking, then Done() when finished.
func (ui *UI) Progress(label string, total int64) *ProgressBar {
	pb := &ProgressBar{
		ui:    ui,
		done:  make(chan struct{}),
		total: total,
		label: label,
	}

	if !ui.isTTY {
		fmt.Fprintf(ui.out, "%s (%s)...\n", label, FormatSize(total))
		return pb
	}

	model := newProgressModel(label, ui.styles)
	p := tea.NewProgram(
		model,
		tea.WithOutput(ui.ttyOut),
		tea.WithInput(nil),
	)
	pb.program = p

	go func() {
		defer close(pb.done)
		_, _ = p.Run()
	}()

	return pb
}

// Update sends a progress fraction (0.0-1.0) to the progress bar.
func (pb *ProgressBar) Update(fraction float64) {
	if pb.program != nil {
		pb.program.Send(progressUpdateMsg{fraction: fraction})
	} else {
		// Non-TTY: print milestones at 25%, 50%, 75%, 100%
		pct := int(fraction * 100)
		pb.mu.Lock()
		defer pb.mu.Unlock()
		for _, milestone := range []int{25, 50, 75, 100} {
			if pct >= milestone && pb.lastMilestone < milestone {
				fmt.Fprintf(pb.ui.out, "Upload: %d%%\n", milestone)
				pb.lastMilestone = milestone
			}
		}
	}
}

// Reader wraps an io.Reader to feed progress updates to the progress bar.
func (pb *ProgressBar) Reader(r io.Reader) io.Reader {
	return &progressReader{
		reader: r,
		total:  pb.total,
		onUpdate: func(fraction float64) {
			pb.Update(fraction)
		},
	}
}

// Done stops the progress bar and prints a completion line.
func (pb *ProgressBar) Done() {
	if pb.program != nil {
		pb.program.Send(progressDoneMsg{success: true})
		<-pb.done
	} else {
		fmt.Fprintln(pb.ui.out, "Upload complete")
	}
}

// Abort stops the progress bar without a success message.
// Use this when the operation has failed.
func (pb *ProgressBar) Abort() {
	if pb.program != nil {
		pb.program.Send(progressDoneMsg{success: false})
		<-pb.done
	}
}

// progressUpdateMsg sends a new fraction (0.0-1.0) to the bubbletea progress model.
type progressUpdateMsg struct {
	fraction float64
}

// progressDoneMsg signals the progress bar to finish.
type progressDoneMsg struct {
	success bool
}

// progressModel is the bubbletea model for an inline progress bar.
type progressModel struct {
	progress progress.Model
	label    string
	fraction float64
	finished bool
	success  bool
	styles   uiStyles
}

func newProgressModel(label string, styles uiStyles) progressModel {
	p := progress.New(
		progress.WithGradient(string(styles.progressFrom), string(styles.progressTo)),
		progress.WithWidth(30),
	)
	return progressModel{
		progress: p,
		label:    label,
		styles:   styles,
	}
}

func (m progressModel) Init() tea.Cmd {
	return nil
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressUpdateMsg:
		m.fraction = msg.fraction
		return m, m.progress.SetPercent(msg.fraction)
	case progressDoneMsg:
		m.finished = true
		m.success = msg.success
		return m, tea.Quit
	case progress.FrameMsg:
		pm, cmd := m.progress.Update(msg)
		m.progress = pm.(progress.Model)
		return m, cmd
	}
	return m, nil
}

func (m progressModel) View() string {
	if m.finished {
		if m.success {
			return m.styles.success.Render("✓ Upload complete")
		}
		return ""
	}

	pct := int(m.fraction * 100)
	info := fmt.Sprintf("%s %d%%", m.label, pct)
	return m.progress.ViewAs(m.fraction) + " " + info
}

// SelectDocuments shows an interactive checkbox list for selecting documents.
// All documents are pre-selected. Returns the selected documents.
// In non-TTY mode, returns all documents without interaction.
func (ui *UI) SelectDocuments(docs []Document) ([]Document, error) {
	if !ui.isTTY {
		return docs, nil
	}

	items := make([]checkboxItem, len(docs))
	for i, doc := range docs {
		label := doc.Main
		if doc.Directory != "" {
			label = filepath.Join(doc.Directory, doc.Main)
		}
		items[i] = checkboxItem{label: label, selected: true}
	}

	model := newCheckboxModel(items, ui.styles)
	p := tea.NewProgram(
		model,
		tea.WithOutput(ui.ttyOut),
		tea.WithInput(ui.in),
	)

	result, err := p.Run()
	if err != nil {
		return nil, err
	}

	m := result.(checkboxModel)
	if m.cancelled {
		return nil, nil
	}

	var selected []Document
	for i, item := range m.items {
		if item.selected {
			selected = append(selected, docs[i])
		}
	}
	return selected, nil
}

// checkboxItem represents a single item in a checkbox list.
type checkboxItem struct {
	label    string
	selected bool
}

// checkboxModel is the bubbletea model for an interactive checkbox list.
type checkboxModel struct {
	items     []checkboxItem
	cursor    int
	styles    uiStyles
	finished  bool
	cancelled bool
}

func newCheckboxModel(items []checkboxItem, styles uiStyles) checkboxModel {
	return checkboxModel{
		items:  items,
		styles: styles,
	}
}

func (m checkboxModel) Init() tea.Cmd {
	return nil
}

func (m checkboxModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case " ":
			m.items[m.cursor].selected = !m.items[m.cursor].selected
		case "a":
			allSelected := true
			for _, item := range m.items {
				if !item.selected {
					allSelected = false
					break
				}
			}
			for i := range m.items {
				m.items[i].selected = !allSelected
			}
		case "enter":
			m.finished = true
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m checkboxModel) View() string {
	if m.finished || m.cancelled {
		return ""
	}

	var b strings.Builder
	b.WriteString("Select documents to include (space=toggle, a=all/none, enter=confirm):\n\n")

	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		check := "[ ]"
		if item.selected {
			check = "[x]"
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", cursor, check, item.label))
	}

	return b.String()
}

// selectModel is the bubbletea model for an interactive single-select list.
type selectModel struct {
	options   []string
	cursor    int
	styles    uiStyles
	finished  bool
	cancelled bool
}

func newSelectModel(options []string, styles uiStyles) selectModel {
	return selectModel{
		options: options,
		styles:  styles,
	}
}

func (m selectModel) Init() tea.Cmd {
	return nil
}

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case "enter":
			m.finished = true
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectModel) View() string {
	if m.finished || m.cancelled {
		return ""
	}

	var b strings.Builder
	b.WriteString("Use arrow keys to select, enter to confirm:\n\n")

	for i, opt := range m.options {
		if i == m.cursor {
			b.WriteString(fmt.Sprintf("> %s\n", opt))
		} else {
			b.WriteString(fmt.Sprintf("  %s\n", opt))
		}
	}

	return b.String()
}

// SelectOne displays an interactive single-select list using Bubble Tea.
// Returns the index of the selected option, or an error if cancelled or non-TTY.
func (ui *UI) SelectOne(label string, options []string) (int, error) {
	if !ui.isTTY {
		return -1, fmt.Errorf("interactive selection requires a terminal (TTY)")
	}
	if len(options) == 0 {
		return -1, fmt.Errorf("no options to select from")
	}

	fmt.Fprintln(ui.out, label)
	model := newSelectModel(options, ui.styles)
	p := tea.NewProgram(model, tea.WithOutput(ui.ttyOut), tea.WithInput(ui.in))
	result, err := p.Run()
	if err != nil {
		return -1, err
	}

	m := result.(selectModel)
	if m.cancelled {
		return -1, fmt.Errorf("selection cancelled")
	}
	return m.cursor, nil
}

// textInputModel is the bubbletea model for an interactive text input.
type textInputModel struct {
	textInput textinput.Model
	styles    uiStyles
	err       string
	finished  bool
	cancelled bool
}

func newTextInputModel(styles uiStyles) textInputModel {
	ti := textinput.New()
	ti.Focus()
	ti.CharLimit = 256
	return textInputModel{
		textInput: ti,
		styles:    styles,
	}
}

func (m textInputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m textInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			value := strings.TrimSpace(m.textInput.Value())
			if value == "" {
				m.err = "name cannot be empty"
				return m, nil
			}
			m.textInput.SetValue(value)
			m.err = ""
			m.finished = true
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m textInputModel) View() string {
	if m.finished || m.cancelled {
		return ""
	}

	var b strings.Builder
	b.WriteString(m.textInput.View())
	if m.err != "" {
		b.WriteString("\n")
		b.WriteString(m.styles.errMsg.Render(m.err))
	}
	return b.String()
}

// TextInput displays an interactive text input prompt using Bubble Tea.
// Returns the entered text, or an error if cancelled or non-TTY.
func (ui *UI) TextInput(label string) (string, error) {
	if !ui.isTTY {
		return "", fmt.Errorf("interactive input requires a terminal (TTY)")
	}

	fmt.Fprintln(ui.out, label)
	model := newTextInputModel(ui.styles)
	p := tea.NewProgram(model, tea.WithOutput(ui.ttyOut), tea.WithInput(ui.in))
	result, err := p.Run()
	if err != nil {
		return "", err
	}

	m := result.(textInputModel)
	if m.cancelled {
		return "", fmt.Errorf("input cancelled")
	}
	return m.textInput.Value(), nil
}
