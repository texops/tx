package cli_test

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/texops/tx/internal/cli"
)

// newTestUI creates a UI writing to a buffer (non-TTY mode).
// Uses NewUIWithOptions so both out and errOut go to the same buffer for test capture.
func newTestUI() (*cli.UI, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	return ui, buf
}

func TestNewUI_NonTTY(t *testing.T) {
	ui, _ := newTestUI()
	assert.False(t, ui.IsTTY())
}

func TestUI_Status(t *testing.T) {
	t.Run("prints message with newline", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.Status("Getting session...")
		assert.Equal(t, "Getting session...\n", buf.String())
	})

	t.Run("prints multiple messages", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.Status("first")
		ui.Status("second")
		assert.Equal(t, "first\nsecond\n", buf.String())
	})
}

func TestUI_Success(t *testing.T) {
	t.Run("prints plain message in non-TTY", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.Success("Session acquired")
		assert.Equal(t, "Session acquired\n", buf.String())
	})

	t.Run("does not include checkmark in non-TTY", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.Success("Done")
		assert.NotContains(t, buf.String(), "✓")
	})
}

func TestUI_Log(t *testing.T) {
	t.Run("prints indented line", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.Log("latexmk -pdf main.tex")
		assert.Equal(t, "    latexmk -pdf main.tex\n", buf.String())
	})

	t.Run("preserves 4-space indent", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.Log("line")
		assert.True(t, strings.HasPrefix(buf.String(), "    "))
	})
}

func TestUI_Errorf(t *testing.T) {
	t.Run("prints formatted error message", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.Errorf("build failed: %s", "timeout")
		assert.Equal(t, "build failed: timeout\n", buf.String())
	})

	t.Run("prints plain error without formatting", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.Errorf("something went wrong")
		assert.Equal(t, "something went wrong\n", buf.String())
	})
}

func TestUI_DimInfo(t *testing.T) {
	t.Run("prints plain message in non-TTY", func(t *testing.T) {
		ui, buf := newTestUI()
		ui.DimInfo("4.2 MB")
		assert.Equal(t, "4.2 MB\n", buf.String())
	})
}

func TestUI_Confirm_NonTTY(t *testing.T) {
	t.Run("returns true automatically in non-TTY", func(t *testing.T) {
		ui, buf := newTestUI()
		result, err := ui.Confirm("Upload 50 MB?")
		require.NoError(t, err)
		assert.True(t, result)
		// Should not print prompt in non-TTY
		assert.Empty(t, buf.String())
	})
}

func TestUI_Confirm_WithInput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"empty input (enter) returns true", "\n", true},
		{"y returns true", "y\n", true},
		{"Y returns true", "Y\n", true},
		{"yes returns true", "yes\n", true},
		{"YES returns true", "YES\n", true},
		{"n returns false", "n\n", false},
		{"no returns false", "no\n", false},
		{"arbitrary text returns false", "maybe\n", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			ui := cli.NewUIWithOptions(buf, true, strings.NewReader(tc.input))
			result, err := ui.Confirm("Continue?")
			require.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestUI_Confirm_TTY_PrintsPrompt(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, true, strings.NewReader("y\n"))
	_, err := ui.Confirm("Upload 50 MB?")
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Upload 50 MB?")
	assert.Contains(t, buf.String(), "[Y/n]")
}

// TTY mode tests: when isTTY=true, output should include styled text.
func TestUI_TTY_Status(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, true, nil)
	ui.Status("Getting session...")
	out := buf.String()
	assert.Contains(t, out, "Getting session...")
	// Bold styling produces ANSI escape sequences
	assert.Contains(t, out, "\x1b[")
}

func TestUI_TTY_Success(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, true, nil)
	ui.Success("Build complete")
	out := buf.String()
	assert.Contains(t, out, "✓ Build complete")
	assert.Contains(t, out, "\x1b[")
}

func TestUI_TTY_Log(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, true, nil)
	ui.Log("latexmk -pdf main.tex")
	out := buf.String()
	assert.Contains(t, out, "    latexmk -pdf main.tex")
	// Faint styling produces ANSI escape sequences
	assert.Contains(t, out, "\x1b[")
}

func TestUI_TTY_Errorf(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, true, nil)
	ui.Errorf("build failed: %s", "timeout")
	out := buf.String()
	assert.Contains(t, out, "build failed: timeout")
	assert.Contains(t, out, "\x1b[")
}

func TestUI_TTY_DimInfo(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, true, nil)
	ui.DimInfo("12.4s")
	out := buf.String()
	assert.Contains(t, out, "12.4s")
	assert.Contains(t, out, "\x1b[")
}

// Spinner tests — non-TTY mode

func TestSpinner_NonTTY_Spin(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	_ = ui.Spin("Getting session...")
	assert.Equal(t, "Getting session...\n", buf.String())
}

func TestSpinner_NonTTY_Stop(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	sp := ui.Spin("Getting session...")
	buf.Reset() // clear the Spin output
	sp.Stop("Session acquired")
	assert.Equal(t, "Session acquired\n", buf.String())
}

func TestSpinner_NonTTY_Fail(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	sp := ui.Spin("Getting session...")
	buf.Reset()
	sp.Fail("connection timeout")
	assert.Equal(t, "connection timeout\n", buf.String())
}

func TestSpinner_NonTTY_FullFlow(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	sp := ui.Spin("Collecting files...")
	sp.Stop("Found 23 files (4.2 MB)")
	output := buf.String()
	assert.Contains(t, output, "Collecting files...")
	assert.Contains(t, output, "Found 23 files (4.2 MB)")
}

func TestSpinner_NonTTY_FailFlow(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	sp := ui.Spin("Syncing with instance...")
	sp.Fail("network error")
	output := buf.String()
	assert.Contains(t, output, "Syncing with instance...")
	assert.Contains(t, output, "network error")
}

func TestSpinner_NonTTY_MultipleSpinners(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)

	sp1 := ui.Spin("Step 1...")
	sp1.Stop("Step 1 done")

	sp2 := ui.Spin("Step 2...")
	sp2.Stop("Step 2 done")

	output := buf.String()
	assert.Contains(t, output, "Step 1...")
	assert.Contains(t, output, "Step 1 done")
	assert.Contains(t, output, "Step 2...")
	assert.Contains(t, output, "Step 2 done")
}

// Progress bar tests — non-TTY mode

func TestProgress_NonTTY_PrintsLabel(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	_ = ui.Progress("Uploading 5 files", 1024*1024)
	assert.Contains(t, buf.String(), "Uploading 5 files")
	assert.Contains(t, buf.String(), "1.0 MB")
}

func TestProgress_NonTTY_Done(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	pb := ui.Progress("Uploading", 1024)
	pb.Done()
	assert.Contains(t, buf.String(), "Upload complete")
}

func TestProgress_NonTTY_MilestoneOutput(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	pb := ui.Progress("Uploading", 100)

	// Create a reader with 100 bytes
	data := strings.Repeat("x", 100)
	r := pb.Reader(strings.NewReader(data))

	// Read all content to trigger milestone callbacks
	var out bytes.Buffer
	_, err := out.ReadFrom(r)
	require.NoError(t, err)

	pb.Done()

	output := buf.String()
	assert.Contains(t, output, "Upload: 25%")
	assert.Contains(t, output, "Upload: 50%")
	assert.Contains(t, output, "Upload: 75%")
	assert.Contains(t, output, "Upload: 100%")
	assert.Contains(t, output, "Upload complete")
}

func TestProgress_NonTTY_NoDuplicateMilestones(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	pb := ui.Progress("Uploading", 100)

	data := strings.Repeat("x", 100)
	r := pb.Reader(strings.NewReader(data))

	var out bytes.Buffer
	_, err := out.ReadFrom(r)
	require.NoError(t, err)
	pb.Done()

	output := buf.String()
	// Each milestone should appear exactly once
	assert.Equal(t, 1, strings.Count(output, "Upload: 25%"))
	assert.Equal(t, 1, strings.Count(output, "Upload: 50%"))
	assert.Equal(t, 1, strings.Count(output, "Upload: 75%"))
	assert.Equal(t, 1, strings.Count(output, "Upload: 100%"))
}

func TestProgress_NonTTY_FullFlow(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, false, nil)
	pb := ui.Progress("Uploading 3 files", 2048)

	data := strings.Repeat("y", 2048)
	r := pb.Reader(strings.NewReader(data))

	var out bytes.Buffer
	_, err := out.ReadFrom(r)
	require.NoError(t, err)
	pb.Done()

	output := buf.String()
	assert.Contains(t, output, "Uploading 3 files (2.0 KB)")
	assert.Contains(t, output, "Upload complete")
}

// selectModel tests

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// sendKey sends a key message to a selectModel and returns the updated model.
func sendKey(m cli.SelectModel, key string) cli.SelectModel {
	result, _ := m.Update(keyMsg(key))
	return result.(cli.SelectModel)
}

func TestSelectModel_Navigation(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)
	options := []string{"30 days", "90 days", "1 year", "No expiry"}

	t.Run("starts at first option", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		view := m.View()
		assert.Contains(t, view, "> 30 days")
		assert.Contains(t, view, "  90 days")
	})

	t.Run("down moves cursor", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "down")
		assert.Contains(t, m.View(), "  30 days")
		assert.Contains(t, m.View(), "> 90 days")
	})

	t.Run("up moves cursor", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "down")
		m = sendKey(m, "down")
		m = sendKey(m, "up")
		assert.Contains(t, m.View(), "> 90 days")
	})

	t.Run("j moves down", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "j")
		assert.Contains(t, m.View(), "> 90 days")
	})

	t.Run("k moves up", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "j")
		m = sendKey(m, "k")
		assert.Contains(t, m.View(), "> 30 days")
	})

	t.Run("does not move past top", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "up")
		assert.Contains(t, m.View(), "> 30 days")
	})

	t.Run("does not move past bottom", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "down")
		m = sendKey(m, "down")
		m = sendKey(m, "down")
		m = sendKey(m, "down")
		assert.Contains(t, m.View(), "> No expiry")
	})
}

func TestSelectModel_Selection(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)
	options := []string{"30 days", "90 days", "1 year"}

	t.Run("enter selects current option", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "down")
		result, cmd := m.Update(keyMsg("enter"))
		state := cli.GetSelectModelState(result.(cli.SelectModel))
		assert.True(t, state.Finished)
		assert.False(t, state.Cancelled)
		assert.Equal(t, 1, state.Cursor)
		assert.NotNil(t, cmd)
	})

	t.Run("view returns empty after selection", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "enter")
		assert.Empty(t, m.View())
	})
}

func TestSelectModel_Cancel(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)
	options := []string{"30 days", "90 days"}

	t.Run("esc cancels", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		result, cmd := m.Update(keyMsg("esc"))
		state := cli.GetSelectModelState(result.(cli.SelectModel))
		assert.True(t, state.Cancelled)
		assert.False(t, state.Finished)
		assert.NotNil(t, cmd)
	})

	t.Run("q cancels", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		result, _ := m.Update(keyMsg("q"))
		state := cli.GetSelectModelState(result.(cli.SelectModel))
		assert.True(t, state.Cancelled)
	})

	t.Run("ctrl+c cancels", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		result, cmd := m.Update(keyMsg("ctrl+c"))
		state := cli.GetSelectModelState(result.(cli.SelectModel))
		assert.True(t, state.Cancelled)
		assert.False(t, state.Finished)
		assert.NotNil(t, cmd)
	})

	t.Run("view returns empty after cancel", func(t *testing.T) {
		m := cli.NewSelectModel(options, styles)
		m = sendKey(m, "esc")
		assert.Empty(t, m.View())
	})
}

func TestSelectModel_Init(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)
	m := cli.NewSelectModel([]string{"a", "b"}, styles)
	cmd := m.Init()
	assert.Nil(t, cmd)
}

func TestSelectOne_NonTTY_ReturnsError(t *testing.T) {
	ui, _ := newTestUI()
	_, err := ui.SelectOne("Pick one:", []string{"a", "b"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TTY")
}

func TestSelectOne_EmptyOptions_ReturnsError(t *testing.T) {
	buf := &bytes.Buffer{}
	ui := cli.NewUIWithOptions(buf, true, nil)
	_, err := ui.SelectOne("Pick one:", []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no options")
}

// textInputModel tests

// sendKeyToTextInput sends a key message to a textInputModel and returns the updated model.
func sendKeyToTextInput(m cli.TextInputModel, key string) cli.TextInputModel {
	result, _ := m.Update(keyMsg(key))
	return result.(cli.TextInputModel)
}

// typeText simulates typing a string character by character into a textInputModel.
func typeText(m cli.TextInputModel, text string) cli.TextInputModel {
	for _, ch := range text {
		m = sendKeyToTextInput(m, string(ch))
	}
	return m
}

func TestTextInputModel_TypeAndSubmit(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)

	t.Run("typing and pressing enter submits value", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		m = typeText(m, "my-ci-token")
		result, cmd := m.Update(keyMsg("enter"))
		state := cli.GetTextInputModelState(result.(cli.TextInputModel))
		assert.True(t, state.Finished)
		assert.False(t, state.Cancelled)
		assert.Equal(t, "my-ci-token", state.Value)
		assert.Empty(t, state.Err)
		assert.NotNil(t, cmd)
	})

	t.Run("value is trimmed on submission", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		m = typeText(m, "  spaced  ")
		result, _ := m.Update(keyMsg("enter"))
		state := cli.GetTextInputModelState(result.(cli.TextInputModel))
		assert.True(t, state.Finished)
		assert.Equal(t, "spaced", state.Value)
	})
}

func TestTextInputModel_EmptySubmit(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)

	t.Run("empty enter shows error", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		result, cmd := m.Update(keyMsg("enter"))
		state := cli.GetTextInputModelState(result.(cli.TextInputModel))
		assert.False(t, state.Finished)
		assert.False(t, state.Cancelled)
		assert.Equal(t, "name cannot be empty", state.Err)
		assert.Nil(t, cmd)
	})

	t.Run("spaces-only enter shows error", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		m = typeText(m, "   ")
		result, cmd := m.Update(keyMsg("enter"))
		state := cli.GetTextInputModelState(result.(cli.TextInputModel))
		assert.False(t, state.Finished)
		assert.Equal(t, "name cannot be empty", state.Err)
		assert.Nil(t, cmd)
	})

	t.Run("error clears after typing and submitting", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		// First: empty submit shows error
		m = sendKeyToTextInput(m, "enter")
		state := cli.GetTextInputModelState(m)
		assert.Equal(t, "name cannot be empty", state.Err)
		// Then: type something and submit
		m = typeText(m, "valid-name")
		result, _ := m.Update(keyMsg("enter"))
		state = cli.GetTextInputModelState(result.(cli.TextInputModel))
		assert.True(t, state.Finished)
		assert.Empty(t, state.Err)
	})
}

func TestTextInputModel_Cancel(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)

	t.Run("esc cancels", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		result, cmd := m.Update(keyMsg("esc"))
		state := cli.GetTextInputModelState(result.(cli.TextInputModel))
		assert.True(t, state.Cancelled)
		assert.False(t, state.Finished)
		assert.NotNil(t, cmd)
	})

	t.Run("ctrl+c cancels", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		result, cmd := m.Update(keyMsg("ctrl+c"))
		state := cli.GetTextInputModelState(result.(cli.TextInputModel))
		assert.True(t, state.Cancelled)
		assert.False(t, state.Finished)
		assert.NotNil(t, cmd)
	})

	t.Run("view returns empty after cancel", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		m = sendKeyToTextInput(m, "esc")
		assert.Empty(t, m.View())
	})
}

func TestTextInputModel_View(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)

	t.Run("view renders text input", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		view := m.View()
		// The textinput.Model renders a cursor/prompt area
		assert.NotEmpty(t, view)
	})

	t.Run("view shows error message", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		m = sendKeyToTextInput(m, "enter")
		view := m.View()
		assert.Contains(t, view, "name cannot be empty")
	})

	t.Run("view returns empty when finished", func(t *testing.T) {
		m := cli.NewTextInputModel(styles)
		m = typeText(m, "test")
		m = sendKeyToTextInput(m, "enter")
		assert.Empty(t, m.View())
	})
}

func TestTextInputModel_Init(t *testing.T) {
	styles := cli.NewUIStyles(true, nil)
	m := cli.NewTextInputModel(styles)
	cmd := m.Init()
	assert.NotNil(t, cmd) // textinput.Blink cmd
}

func TestTextInput_NonTTY_ReturnsError(t *testing.T) {
	ui, _ := newTestUI()
	_, err := ui.TextInput("Token name:")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TTY")
}
