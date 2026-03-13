package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgressReader_ByteCounting(t *testing.T) {
	data := []byte("hello world") // 11 bytes
	var fractions []float64
	pr := &progressReader{
		reader: bytes.NewReader(data),
		total:  int64(len(data)),
		onUpdate: func(fraction float64) {
			fractions = append(fractions, fraction)
		},
	}

	buf := make([]byte, 5)
	n, err := pr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, int64(5), pr.read)

	n, err = pr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, int64(10), pr.read)

	n, err = pr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(11), pr.read)

	// Each Read should have triggered a callback
	assert.Len(t, fractions, 3)
	assert.InDelta(t, 5.0/11.0, fractions[0], 0.001)
	assert.InDelta(t, 10.0/11.0, fractions[1], 0.001)
	assert.InDelta(t, 1.0, fractions[2], 0.001)
}

func TestProgressReader_ZeroTotal(t *testing.T) {
	data := []byte("hi")
	var called bool
	pr := &progressReader{
		reader: bytes.NewReader(data),
		total:  0,
		onUpdate: func(fraction float64) {
			called = true
		},
	}

	buf := make([]byte, 10)
	n, err := pr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	// Callback should NOT be called when total is 0 (avoid divide-by-zero)
	assert.False(t, called)
}

func TestProgressReader_NilCallback(t *testing.T) {
	data := []byte("test")
	pr := &progressReader{
		reader:   bytes.NewReader(data),
		total:    int64(len(data)),
		onUpdate: nil,
	}

	buf := make([]byte, 10)
	n, err := pr.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, int64(4), pr.read)
}

func TestProgressReader_FractionClampedToOne(t *testing.T) {
	// Simulate a case where more bytes are read than expected total
	data := []byte("hello")
	var lastFraction float64
	pr := &progressReader{
		reader: bytes.NewReader(data),
		total:  3, // less than actual data
		onUpdate: func(fraction float64) {
			lastFraction = fraction
		},
	}

	buf := make([]byte, 10)
	pr.Read(buf)
	assert.Equal(t, 1.0, lastFraction)
}

func TestProgressReader_FullContent(t *testing.T) {
	data := strings.Repeat("x", 1024)
	var callCount int
	pr := &progressReader{
		reader: strings.NewReader(data),
		total:  1024,
		onUpdate: func(fraction float64) {
			callCount++
		},
	}

	// Read all content
	var buf bytes.Buffer
	n, err := buf.ReadFrom(pr)
	require.NoError(t, err)
	assert.Equal(t, int64(1024), n)
	assert.True(t, callCount > 0, "callback should have been called at least once")
	assert.Equal(t, int64(1024), pr.read)
}

func TestProgressModel_Init(t *testing.T) {
	model := newProgressModel("Uploading", newStyles(true, nil))
	cmd := model.Init()
	assert.Nil(t, cmd, "Init should return nil (no initial animation)")
}

func TestProgressModel_Update_ProgressUpdate(t *testing.T) {
	model := newProgressModel("Uploading", newStyles(true, nil))
	updated, cmd := model.Update(progressUpdateMsg{fraction: 0.5})
	pm := updated.(progressModel)

	assert.InDelta(t, 0.5, pm.fraction, 0.001)
	assert.False(t, pm.finished)
	assert.NotNil(t, cmd, "SetPercent should return an animation command")
}

func TestProgressModel_Update_DoneMsg_Success(t *testing.T) {
	model := newProgressModel("Uploading", newStyles(true, nil))
	updated, cmd := model.Update(progressDoneMsg{success: true})
	pm := updated.(progressModel)

	assert.True(t, pm.finished)
	assert.True(t, pm.success)
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	assert.True(t, ok, "done msg should produce QuitMsg")
}

func TestProgressModel_Update_DoneMsg_Abort(t *testing.T) {
	model := newProgressModel("Uploading", newStyles(true, nil))
	updated, cmd := model.Update(progressDoneMsg{success: false})
	pm := updated.(progressModel)

	assert.True(t, pm.finished)
	assert.False(t, pm.success)
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	assert.True(t, ok, "abort done msg should produce QuitMsg")
}

func TestProgressModel_Update_FrameMsg(t *testing.T) {
	model := newProgressModel("Uploading", newStyles(true, nil))
	// First set a percent to get animation going
	updated, cmd := model.Update(progressUpdateMsg{fraction: 0.5})
	pm := updated.(progressModel)
	require.NotNil(t, cmd)

	// The command should produce a FrameMsg
	msg := cmd()
	frameMsg, ok := msg.(progress.FrameMsg)
	assert.True(t, ok, "SetPercent cmd should produce a FrameMsg")

	// Update with the FrameMsg
	updated2, _ := pm.Update(frameMsg)
	pm2 := updated2.(progressModel)
	assert.False(t, pm2.finished)
}

func TestProgressModel_View_InProgress(t *testing.T) {
	model := newProgressModel("Uploading 5 files", newStyles(true, nil))
	model.fraction = 0.5
	view := model.View()
	assert.Contains(t, view, "Uploading 5 files")
	assert.Contains(t, view, "50%")
}

func TestProgressModel_View_Finished_Success(t *testing.T) {
	model := newProgressModel("Uploading", newStyles(true, nil))
	model.finished = true
	model.success = true
	view := model.View()
	assert.Contains(t, view, "Upload complete")
}

func TestProgressModel_View_Finished_Abort(t *testing.T) {
	model := newProgressModel("Uploading", newStyles(true, nil))
	model.finished = true
	model.success = false
	view := model.View()
	assert.NotContains(t, view, "Upload complete")
}
