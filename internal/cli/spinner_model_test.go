package cli

import (
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpinnerModel_Init(t *testing.T) {
	model := newSpinnerModel("Loading...", newStyles(true, nil))
	cmd := model.Init()
	require.NotNil(t, cmd, "Init should return a tick command")

	// The command should produce a spinner.TickMsg
	msg := cmd()
	_, ok := msg.(spinner.TickMsg)
	assert.True(t, ok, "Init command should produce a spinner.TickMsg")
}

func TestSpinnerModel_View_ShowsSpinnerAndMessage(t *testing.T) {
	model := newSpinnerModel("Getting session...", newStyles(true, nil))
	view := model.View()
	assert.Contains(t, view, "Getting session...")
}

func TestSpinnerModel_Update_DoneMsg(t *testing.T) {
	model := newSpinnerModel("Loading...", newStyles(true, nil))
	styles := newStyles(true, nil)

	updated, cmd := model.Update(spinnerDoneMsg{
		text:  "✓ Done",
		style: styles.success,
	})
	sm := updated.(spinnerModel)

	assert.True(t, sm.finished, "model should be finished after done msg")
	assert.Contains(t, sm.finalMsg, "✓ Done")

	// The command should be tea.Quit
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	assert.True(t, ok, "done msg should produce QuitMsg")
}

func TestSpinnerModel_View_AfterDone(t *testing.T) {
	model := newSpinnerModel("Loading...", newStyles(true, nil))
	styles := newStyles(true, nil)

	updated, _ := model.Update(spinnerDoneMsg{
		text:  "✓ Complete",
		style: styles.success,
	})
	sm := updated.(spinnerModel)

	view := sm.View()
	assert.Contains(t, view, "✓ Complete")
	assert.NotContains(t, view, "Loading...")
}

func TestSpinnerModel_Update_TickMsg(t *testing.T) {
	model := newSpinnerModel("Loading...", newStyles(true, nil))

	// Simulate a tick using the inner spinner's Tick method
	updated, cmd := model.Update(model.spinner.Tick())
	sm := updated.(spinnerModel)

	assert.False(t, sm.finished, "model should not be finished after tick")
	assert.NotNil(t, cmd, "tick should produce another command")
}
