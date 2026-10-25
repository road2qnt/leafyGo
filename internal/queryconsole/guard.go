package queryconsole

import (
	"fmt"
	"regexp"
	"strings"

	"leafygo/internal/migration"
)

var reAllowlist = regexp.MustCompile(`^(?i)(SELECT|WITH|TABLE|VALUES|SHOW|EXPLAIN)\b`)

// Validate inspects the user SQL before target connection.
// It enforces single-statement rules, size constraints, and the read-only first-keyword allowlist.
func Validate(sql string, maxBytes int64) (string, error) {
	if int64(len(sql)) > maxBytes {
		return "", fmt.Errorf("%w: SQL must be at most %d bytes", ErrValidation, maxBytes)
	}
	if strings.TrimSpace(sql) == "" {
		return "", fmt.Errorf("%w: SQL must not be empty", ErrValidation)
	}
	if strings.ContainsRune(sql, 0) {
		return "", fmt.Errorf("%w: SQL must not contain NUL characters", ErrValidation)
	}

	stmts, err := migration.Statements(sql)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrValidation, err.Error())
	}
	if len(stmts) == 0 {
		return "", fmt.Errorf("%w: SQL must not be empty", ErrValidation)
	}
	if len(stmts) > 1 {
		return "", fmt.Errorf("%w: SQL console only runs one statement at a time. This input has %d statements", ErrValidation, len(stmts))
	}

	if err := migration.RejectTxControl(stmts); err != nil {
		return "", &TargetError{
			Message:          MsgWriteRejection,
			IsWriteRejection: true,
		}
	}

	clean := migration.StripLeadingCommentsAndWhitespace(stmts[0])
	if clean == "" {
		return "", fmt.Errorf("%w: SQL must not be empty", ErrValidation)
	}
	if !reAllowlist.MatchString(clean) {
		return "", &TargetError{
			Message:          MsgWriteRejection,
			IsWriteRejection: true,
		}
	}

	return stmts[0], nil
}
