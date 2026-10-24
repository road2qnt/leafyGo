package target

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type runPhase int

const (
	phaseConnect runPhase = iota
	phaseStatement
	phaseCommit
)

type RunError struct {
	Err       error
	Uncertain bool
}

func (e *RunError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *RunError) Unwrap() error {
	return e.Err
}

func classifyRunError(phase runPhase, err error) error {
	if err == nil {
		return nil
	}
	if phase == phaseCommit {
		return &RunError{
			Err:       err,
			Uncertain: true,
		}
	}
	return &RunError{
		Err:       err,
		Uncertain: false,
	}
}

var (
	reURLPassword  = regexp.MustCompile(`postgres://[^@]+@`)
	reKeyValuePass = regexp.MustCompile(`(?i)password=\S+`)
)

func sanitizeTargetError(err error) string {
	if err == nil {
		return ""
	}

	var raw string
	var pgErr *pgconn.PgError
	var netErr net.Error

	if errors.As(err, &pgErr) {
		var parts []string
		if pgErr.Code != "" {
			parts = append(parts, fmt.Sprintf("[%s]", pgErr.Code))
		}
		if pgErr.Message != "" {
			parts = append(parts, pgErr.Message)
		}
		if pgErr.Detail != "" {
			parts = append(parts, "detail: "+pgErr.Detail)
		}
		if pgErr.Position > 0 {
			parts = append(parts, fmt.Sprintf("position: %d", pgErr.Position))
		}
		raw = strings.Join(parts, "; ")
	} else if errors.As(err, &netErr) {
		raw = "network error: " + err.Error()
	} else {
		raw = err.Error()
	}

	// Redact postgres://user:pass@...
	raw = reURLPassword.ReplaceAllString(raw, "postgres://[REDACTED]@")

	// Redact password=...
	raw = reKeyValuePass.ReplaceAllString(raw, "password=[REDACTED]")

	// Flatten newlines into spaces
	raw = strings.ReplaceAll(raw, "\r\n", " ")
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = strings.ReplaceAll(raw, "\r", " ")

	// Collapse multiple spaces
	raw = strings.Join(strings.Fields(raw), " ")

	// Truncate to max 512 characters
	if len(raw) > 512 {
		raw = raw[:512]
	}

	return raw
}

// ErrorText provides a sanitized human-readable error description for history or logging.
func ErrorText(err error) string {
	return sanitizeTargetError(err)
}
