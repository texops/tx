package cli

import (
	"context"
	"io"
)

var (
	HasDocumentclass   = hasDocumentclass
	ParseDuration      = parseDuration
	FormatDate         = formatDate
	FormatDatePtr      = formatDatePtr
	NewSelectModel     = newSelectModel
	NewTextInputModel  = newTextInputModel
	NewUIStyles        = newStyles
	GenerateProjectKey = generateProjectKey
	GenerateConfigYAML = generateConfigYAML
)

func WriteFilePreserveInode(r io.Reader, outputPath string) error {
	return writeFilePreserveInode(r, outputPath)
}

// DocResult is exported for testing.
type DocResult = docResult

// WatchAndBuildWith wraps watchAndBuildWith for testing.
func WatchAndBuildWith(ctx context.Context, dir string, ui *UI, build func(context.Context) ([]DocResult, error)) error {
	return watchAndBuildWith(ctx, dir, ui, build, nil)
}

// SelectModel is an alias for selectModel, exported for testing.
type SelectModel = selectModel

// SelectModelState exposes internal fields of selectModel for test assertions.
type SelectModelState struct {
	Cursor    int
	Finished  bool
	Cancelled bool
}

func GetSelectModelState(m selectModel) SelectModelState {
	return SelectModelState{
		Cursor:    m.cursor,
		Finished:  m.finished,
		Cancelled: m.cancelled,
	}
}

// TextInputModel is an alias for textInputModel, exported for testing.
type TextInputModel = textInputModel

// TextInputModelState exposes internal fields of textInputModel for test assertions.
type TextInputModelState struct {
	Value     string
	Err       string
	Finished  bool
	Cancelled bool
}

func GetTextInputModelState(m textInputModel) TextInputModelState {
	return TextInputModelState{
		Value:     m.textInput.Value(),
		Err:       m.err,
		Finished:  m.finished,
		Cancelled: m.cancelled,
	}
}
