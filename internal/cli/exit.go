package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
)

const (
	ExitOK          = 0
	ExitFailure     = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitConfig      = 4
	ExitBuildFailed = 5
)

const (
	KindInternal         = "internal"
	KindNetwork          = "network"
	KindTimeout          = "timeout"
	KindUsage            = "usage"
	KindNotAuthenticated = "not_authenticated"
	KindConfig           = "config"
	KindBuildFailed      = "build_failed"
)

// ExitError carries the process exit code and the JSON error code (Kind) for an error.
type ExitError struct {
	Code int
	Kind string
	Err  error
}

func (e *ExitError) Error() string {
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

func usageError(err error) error {
	return &ExitError{Code: ExitUsage, Kind: KindUsage, Err: err}
}

func usageErrorf(format string, args ...any) error {
	return usageError(fmt.Errorf(format, args...))
}

func authError(err error) error {
	return &ExitError{Code: ExitAuth, Kind: KindNotAuthenticated, Err: err}
}

func configError(err error) error {
	return &ExitError{Code: ExitConfig, Kind: KindConfig, Err: err}
}

// AsExitError classifies err: an ExitError anywhere in the chain wins, then a
// 401 API response (not authenticated), then network errors; anything else is
// an internal failure. It returns nil for a nil error.
func AsExitError(err error) *ExitError {
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*ExitError](err); ok {
		return exitErr
	}
	if apiErr, ok := errors.AsType[*APIError](err); ok && apiErr.StatusCode == http.StatusUnauthorized {
		return &ExitError{Code: ExitAuth, Kind: KindNotAuthenticated, Err: err}
	}
	if isNetworkError(err) {
		return &ExitError{Code: ExitFailure, Kind: KindNetwork, Err: err}
	}
	return &ExitError{Code: ExitFailure, Kind: KindInternal, Err: err}
}

func isNetworkError(err error) bool {
	if urlErr, ok := errors.AsType[*url.Error](err); ok && urlErr.Op != "parse" {
		return true
	}
	_, ok := errors.AsType[net.Error](err)
	return ok
}

// buildFailureReason is the server's reason for a failed build; an old server
// without a reason counts as a LaTeX failure when it allocated a build.
func buildFailureReason(done BuildDoneEvent) string {
	switch {
	case done.Reason != "":
		return done.Reason
	case done.BuildID != "":
		return "latex_error"
	}
	return "internal"
}

// buildFailureError maps a failed build's done event to an exit code.
func buildFailureError(done BuildDoneEvent, err error) error {
	switch buildFailureReason(done) {
	case "latex_error", "no_pdf":
		return &ExitError{Code: ExitBuildFailed, Kind: KindBuildFailed, Err: err}
	case "timeout":
		return &ExitError{Code: ExitFailure, Kind: KindTimeout, Err: err}
	}
	return &ExitError{Code: ExitFailure, Kind: KindInternal, Err: err}
}

var exitCodePriority = map[int]int{
	ExitFailure:     4,
	ExitAuth:        3,
	ExitConfig:      2,
	ExitUsage:       1,
	ExitBuildFailed: 0,
}

// buildResultsError summarizes failed documents into one error. A service
// failure anywhere makes the whole build a service failure (exit 1); only
// when every failure is a compile failure does it exit 5.
func buildResultsError(results []docResult) error {
	var worst *ExitError
	for _, r := range results {
		if r.Success {
			continue
		}
		err := r.Err
		if err == nil {
			err = errors.New("build failed")
		}
		exitErr := AsExitError(err)
		if worst == nil || exitCodePriority[exitErr.Code] > exitCodePriority[worst.Code] {
			worst = exitErr
		}
	}
	if worst == nil {
		return nil
	}
	return &ExitError{Code: worst.Code, Kind: worst.Kind, Err: errors.New("one or more documents failed to build")}
}
