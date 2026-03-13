package cli

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
