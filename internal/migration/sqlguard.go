package migration

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"leafygo/internal/domain"
)

const (
	DryRunStatusPassed       = "passed"
	DryRunStatusInconclusive = "inconclusive"
	DryRunStatusSkipped      = "skipped"
)

type DryRunResult struct {
	Status   string `json:"dry_run_status"`
	Warning  string `json:"dry_run_warning,omitempty"`
	SQLState string `json:"sql_state,omitempty"`
}

// DedicatedConnOpener opens a short-lived, non-pooled pgx.Conn to a Target DB.
type DedicatedConnOpener interface {
	OpenDedicatedConn(ctx context.Context, db domain.DBConnection, appName string) (*pgx.Conn, error)
}

type tokenKind int

const (
	tokenKindKeyword tokenKind = iota
	tokenKindQuotedIdent
	tokenKindStringLiteral
	tokenKindDollarString
	tokenKindSymbol
)

type sqlToken struct {
	kind  tokenKind
	value string // Upper-case for keywords; inner body for dollar-quoted strings
	raw   string
}

// Statements splits a SQL string into individual statements on semicolons outside of
// quotes and comments. It trims statements and ignores empty ones.
// It returns an error wrapping ErrInvalidMigrationSQL if quotes, dollar-quotes, or block comments are unclosed.
func Statements(sql string) ([]string, error) {
	var statements []string
	n := len(sql)
	start := 0
	i := 0

	for i < n {
		c := sql[i]

		// Check for line comment --
		if c == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			if i < n && sql[i] == '\n' {
				i++
			}
			continue
		}

		// Check for block comment /* (supports nesting)
		if c == '/' && i+1 < n && sql[i+1] == '*' {
			depth := 1
			i += 2
			for i < n && depth > 0 {
				if sql[i] == '/' && i+1 < n && sql[i+1] == '*' {
					depth++
					i += 2
				} else if sql[i] == '*' && i+1 < n && sql[i+1] == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth > 0 {
				return nil, fmt.Errorf("%w: unclosed block comment in SQL", ErrInvalidMigrationSQL)
			}
			continue
		}

		// Check for escape string E'...' or e'...'
		if (c == 'E' || c == 'e') && i+1 < n && sql[i+1] == '\'' && (i == 0 || !isIdentChar(sql[i-1])) {
			i += 2
			closed := false
			for i < n {
				if sql[i] == '\\' {
					i += 2
					continue
				}
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					closed = true
					i++
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unclosed escape string literal in SQL", ErrInvalidMigrationSQL)
			}
			continue
		}

		// Check for single quote '...'
		if c == '\'' {
			i++
			closed := false
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2 // escaped ''
						continue
					}
					closed = true
					i++
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unclosed string literal in SQL", ErrInvalidMigrationSQL)
			}
			continue
		}

		// Check for double quote "..."
		if c == '"' {
			i++
			closed := false
			for i < n {
				if sql[i] == '"' {
					if i+1 < n && sql[i+1] == '"' {
						i += 2 // escaped ""
						continue
					}
					closed = true
					i++
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unclosed identifier quote in SQL", ErrInvalidMigrationSQL)
			}
			continue
		}

		// Check for dollar quote $tag$...$tag$
		if c == '$' {
			tag, tagEnd, ok := parseDollarTag(sql, i)
			if ok {
				i = tagEnd
				closeTag := tag
				closed := false
				for i < n {
					if strings.HasPrefix(sql[i:], closeTag) {
						i += len(closeTag)
						closed = true
						break
					}
					i++
				}
				if !closed {
					return nil, fmt.Errorf("%w: unclosed dollar quote in SQL", ErrInvalidMigrationSQL)
				}
				continue
			}
		}

		// Semicolon statement boundary
		if c == ';' {
			stmt := strings.TrimSpace(sql[start:i])
			if stmt != "" {
				statements = append(statements, stmt)
			}
			i++
			start = i
			continue
		}

		i++
	}

	// Trailing statement
	if start < n {
		stmt := strings.TrimSpace(sql[start:n])
		if stmt != "" {
			statements = append(statements, stmt)
		}
	}

	return statements, nil
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c >= 0x80
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '$'
}

func parseDollarTag(s string, start int) (tag string, nextIdx int, ok bool) {
	if s[start] != '$' {
		return "", start, false
	}
	idx := start + 1
	n := len(s)
	// Dollar tag cannot start with a digit (otherwise $1 is a positional parameter, not a dollar tag)
	if idx < n && s[idx] >= '0' && s[idx] <= '9' {
		return "", start, false
	}
	for idx < n && s[idx] != '$' {
		c := s[idx]
		isAlphaNumOrUnderscore := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if !isAlphaNumOrUnderscore {
			return "", start, false
		}
		idx++
	}
	if idx < n && s[idx] == '$' {
		return s[start : idx+1], idx + 1, true
	}
	return "", start, false
}

// lexStatement tokenizes a SQL fragment into keywords, literals, quoted identifiers,
// dollar-quoted strings, and symbols, skipping all line and nested block comments.
func lexStatement(sql string) ([]sqlToken, error) {
	var tokens []sqlToken
	n := len(sql)
	i := 0

	for i < n {
		c := sql[i]

		// Whitespace
		if unicode.IsSpace(rune(c)) {
			i++
			continue
		}

		// Line comment --
		if c == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			if i < n && sql[i] == '\n' {
				i++
			}
			continue
		}

		// Nested block comment /* ... */
		if c == '/' && i+1 < n && sql[i+1] == '*' {
			depth := 1
			i += 2
			for i < n && depth > 0 {
				if sql[i] == '/' && i+1 < n && sql[i+1] == '*' {
					depth++
					i += 2
				} else if sql[i] == '*' && i+1 < n && sql[i+1] == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			}
			if depth > 0 {
				return nil, fmt.Errorf("%w: unclosed block comment in SQL", ErrInvalidMigrationSQL)
			}
			continue
		}

		// Escape string literal E'...' / e'...'
		if (c == 'E' || c == 'e') && i+1 < n && sql[i+1] == '\'' {
			start := i
			i += 2
			closed := false
			for i < n {
				if sql[i] == '\\' {
					i += 2
					continue
				}
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					closed = true
					i++
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unclosed escape string literal in SQL", ErrInvalidMigrationSQL)
			}
			tokens = append(tokens, sqlToken{
				kind:  tokenKindStringLiteral,
				value: sql[start:i],
				raw:   sql[start:i],
			})
			continue
		}

		// Single-quoted string literal '...'
		if c == '\'' {
			start := i
			i++
			closed := false
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					closed = true
					i++
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unclosed string literal in SQL", ErrInvalidMigrationSQL)
			}
			tokens = append(tokens, sqlToken{
				kind:  tokenKindStringLiteral,
				value: sql[start:i],
				raw:   sql[start:i],
			})
			continue
		}

		// Double-quoted identifier "..."
		if c == '"' {
			start := i
			i++
			closed := false
			for i < n {
				if sql[i] == '"' {
					if i+1 < n && sql[i+1] == '"' {
						i += 2
						continue
					}
					closed = true
					i++
					break
				}
				i++
			}
			if !closed {
				return nil, fmt.Errorf("%w: unclosed identifier quote in SQL", ErrInvalidMigrationSQL)
			}
			tokens = append(tokens, sqlToken{
				kind:  tokenKindQuotedIdent,
				value: sql[start+1 : i-1],
				raw:   sql[start:i],
			})
			continue
		}

		// Dollar-quoted string $tag$...$tag$
		if c == '$' {
			tag, tagEnd, ok := parseDollarTag(sql, i)
			if ok {
				start := i
				bodyStart := tagEnd
				i = tagEnd
				closed := false
				var bodyEnd int
				for i < n {
					if strings.HasPrefix(sql[i:], tag) {
						bodyEnd = i
						i += len(tag)
						closed = true
						break
					}
					i++
				}
				if !closed {
					return nil, fmt.Errorf("%w: unclosed dollar quote in SQL", ErrInvalidMigrationSQL)
				}
				tokens = append(tokens, sqlToken{
					kind:  tokenKindDollarString,
					value: sql[bodyStart:bodyEnd],
					raw:   sql[start:i],
				})
				continue
			}
		}

		// Unquoted keyword or identifier
		if isIdentStart(c) {
			start := i
			i++
			for i < n && isIdentChar(sql[i]) {
				i++
			}
			word := sql[start:i]
			tokens = append(tokens, sqlToken{
				kind:  tokenKindKeyword,
				value: strings.ToUpper(word),
				raw:   word,
			})
			continue
		}

		// Symbol / number / punctuation
		tokens = append(tokens, sqlToken{
			kind:  tokenKindSymbol,
			value: string(c),
			raw:   string(c),
		})
		i++
	}

	return tokens, nil
}

// StripLeadingCommentsAndWhitespace removes leading whitespace and comments from a statement.
func StripLeadingCommentsAndWhitespace(sql string) string {
	n := len(sql)
	i := 0
	for i < n {
		if unicode.IsSpace(rune(sql[i])) {
			i++
			continue
		}

		// Line comment
		if sql[i] == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			if i < n && sql[i] == '\n' {
				i++
			}
			continue
		}

		// Block comment (supports nesting)
		if sql[i] == '/' && i+1 < n && sql[i+1] == '*' {
			depth := 1
			i += 2
			for i < n && depth > 0 {
				if sql[i] == '/' && i+1 < n && sql[i+1] == '*' {
					depth++
					i += 2
				} else if sql[i] == '*' && i+1 < n && sql[i+1] == '/' {
					depth--
					i += 2
				} else {
					i++
				}
			}
			continue
		}

		break
	}

	return strings.TrimSpace(sql[i:])
}

// extractLeadingKeywords returns the leading consecutive keyword tokens of a statement,
// plus all tokens in the statement.
func extractKeywords(tokens []sqlToken) []string {
	var kws []string
	for _, t := range tokens {
		if t.kind == tokenKindKeyword {
			kws = append(kws, t.value)
		}
	}
	return kws
}

func checkTopLevelTxControl(tokens []sqlToken) error {
	if len(tokens) == 0 || tokens[0].kind != tokenKindKeyword {
		return nil
	}
	kw0 := tokens[0].value
	var kw1, kw2 string
	if len(tokens) > 1 && tokens[1].kind == tokenKindKeyword {
		kw1 = tokens[1].value
	}
	if len(tokens) > 2 && tokens[2].kind == tokenKindKeyword {
		kw2 = tokens[2].value
	}

	switch kw0 {
	case "BEGIN":
		return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "BEGIN")
	case "START":
		if kw1 == "TRANSACTION" {
			return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "START TRANSACTION")
		}
	case "COMMIT":
		if kw1 == "PREPARED" {
			return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "COMMIT PREPARED")
		}
		return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "COMMIT")
	case "END":
		return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "END")
	case "ROLLBACK":
		if kw1 == "PREPARED" {
			return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "ROLLBACK PREPARED")
		}
		if kw1 == "TO" {
			if kw2 == "SAVEPOINT" {
				return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "ROLLBACK TO SAVEPOINT")
			}
			return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "ROLLBACK TO")
		}
		return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "ROLLBACK")
	case "ABORT":
		return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "ABORT")
	case "SAVEPOINT":
		return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "SAVEPOINT")
	case "RELEASE":
		if kw1 == "SAVEPOINT" {
			return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "RELEASE SAVEPOINT")
		}
		return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "RELEASE")
	case "PREPARE":
		if kw1 == "TRANSACTION" {
			return fmt.Errorf("%w: transaction control statement %q is not permitted in migrations", ErrInvalidMigrationSQL, "PREPARE TRANSACTION")
		}
	}
	return nil
}

func checkNonTransactionalStatement(tokens []sqlToken) error {
	if len(tokens) == 0 || tokens[0].kind != tokenKindKeyword {
		return nil
	}
	kw0 := tokens[0].value
	var kw1, kw2, kw3 string
	if len(tokens) > 1 && tokens[1].kind == tokenKindKeyword {
		kw1 = tokens[1].value
	}
	if len(tokens) > 2 && tokens[2].kind == tokenKindKeyword {
		kw2 = tokens[2].value
	}
	if len(tokens) > 3 && tokens[3].kind == tokenKindKeyword {
		kw3 = tokens[3].value
	}

	switch kw0 {
	case "CREATE":
		if kw1 == "INDEX" && kw2 == "CONCURRENTLY" {
			return fmt.Errorf("%w: CREATE INDEX CONCURRENTLY cannot run inside a transaction block", ErrNonTransactionalStmt)
		}
		if kw1 == "UNIQUE" && kw2 == "INDEX" && kw3 == "CONCURRENTLY" {
			return fmt.Errorf("%w: CREATE UNIQUE INDEX CONCURRENTLY cannot run inside a transaction block", ErrNonTransactionalStmt)
		}
		if kw1 == "DATABASE" {
			return fmt.Errorf("%w: CREATE DATABASE cannot run inside a transaction block", ErrNonTransactionalStmt)
		}
		if kw1 == "TABLESPACE" {
			return fmt.Errorf("%w: CREATE TABLESPACE cannot run inside a transaction block", ErrNonTransactionalStmt)
		}
	case "DROP":
		if kw1 == "INDEX" && kw2 == "CONCURRENTLY" {
			return fmt.Errorf("%w: DROP INDEX CONCURRENTLY cannot run inside a transaction block", ErrNonTransactionalStmt)
		}
		if kw1 == "DATABASE" {
			return fmt.Errorf("%w: DROP DATABASE cannot run inside a transaction block", ErrNonTransactionalStmt)
		}
		if kw1 == "TABLESPACE" {
			return fmt.Errorf("%w: DROP TABLESPACE cannot run inside a transaction block", ErrNonTransactionalStmt)
		}
	case "REINDEX":
		for _, tok := range tokens[1:] {
			if tok.kind == tokenKindKeyword && tok.value == "CONCURRENTLY" {
				return fmt.Errorf("%w: REINDEX CONCURRENTLY cannot run inside a transaction block", ErrNonTransactionalStmt)
			}
		}
	case "VACUUM":
		return fmt.Errorf("%w: VACUUM cannot run inside a transaction block", ErrNonTransactionalStmt)
	case "CLUSTER":
		return fmt.Errorf("%w: CLUSTER cannot run inside a transaction block", ErrNonTransactionalStmt)
	case "ALTER":
		if kw1 == "SYSTEM" {
			return fmt.Errorf("%w: ALTER SYSTEM cannot run inside a transaction block", ErrNonTransactionalStmt)
		}
	}
	return nil
}

// inspectDoBlockBody recursively scans the dollar-quoted body of a DO $$ ... $$ block
// using the same lexical tokenizer to reject COMMIT, ROLLBACK, or other transaction control statements.
func inspectDoBlockBody(body string) error {
	tokens, err := lexStatement(body)
	if err != nil {
		return err
	}
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if tok.kind == tokenKindDollarString {
			// Recursively inspect nested dollar-quoted block if it follows DO
			if i > 0 && tokens[i-1].kind == tokenKindKeyword && tokens[i-1].value == "DO" {
				if err := inspectDoBlockBody(tok.value); err != nil {
					return err
				}
			}
			continue
		}
		if tok.kind != tokenKindKeyword {
			continue
		}
		// Inside PL/pgSQL DO blocks, BEGIN and END are block delimiters, while COMMIT, ROLLBACK,
		// START TRANSACTION, SAVEPOINT, RELEASE, ABORT, and PREPARE TRANSACTION are transaction control.
		var nextKW string
		if i+1 < len(tokens) && tokens[i+1].kind == tokenKindKeyword {
			nextKW = tokens[i+1].value
		}
		switch tok.value {
		case "COMMIT":
			return fmt.Errorf("%w: transaction control statement %q inside DO block is not permitted", ErrInvalidMigrationSQL, "COMMIT")
		case "ROLLBACK":
			return fmt.Errorf("%w: transaction control statement %q inside DO block is not permitted", ErrInvalidMigrationSQL, "ROLLBACK")
		case "ABORT":
			return fmt.Errorf("%w: transaction control statement %q inside DO block is not permitted", ErrInvalidMigrationSQL, "ABORT")
		case "SAVEPOINT":
			return fmt.Errorf("%w: transaction control statement %q inside DO block is not permitted", ErrInvalidMigrationSQL, "SAVEPOINT")
		case "RELEASE":
			return fmt.Errorf("%w: transaction control statement %q inside DO block is not permitted", ErrInvalidMigrationSQL, "RELEASE")
		case "START":
			if nextKW == "TRANSACTION" {
				return fmt.Errorf("%w: transaction control statement %q inside DO block is not permitted", ErrInvalidMigrationSQL, "START TRANSACTION")
			}
		case "PREPARE":
			if nextKW == "TRANSACTION" {
				return fmt.Errorf("%w: transaction control statement %q inside DO block is not permitted", ErrInvalidMigrationSQL, "PREPARE TRANSACTION")
			}
		}
	}
	return nil
}

// RejectTxControl inspects SQL statements using the PostgreSQL lexical tokenizer and rejects:
//  1. Transaction control statements (BEGIN, START TRANSACTION, COMMIT, END, ROLLBACK, ABORT,
//     SAVEPOINT, RELEASE [SAVEPOINT], ROLLBACK TO [SAVEPOINT], PREPARE TRANSACTION,
//     COMMIT PREPARED, ROLLBACK PREPARED), including COMMIT/ROLLBACK inside DO $$ ... $$ blocks
//     -> ErrInvalidMigrationSQL (422 INVALID_MIGRATION_SQL).
//  2. Non-transactional statements (CREATE [UNIQUE] INDEX CONCURRENTLY, DROP INDEX CONCURRENTLY,
//     REINDEX ... CONCURRENTLY, VACUUM, CLUSTER, CREATE/DROP DATABASE, CREATE/DROP TABLESPACE,
//     ALTER SYSTEM) -> ErrNonTransactionalStmt (422 NON_TRANSACTIONAL_STATEMENT).
func RejectTxControl(statements []string) error {
	for _, stmt := range statements {
		tokens, err := lexStatement(stmt)
		if err != nil {
			return err
		}
		if len(tokens) == 0 {
			continue
		}

		if err := checkTopLevelTxControl(tokens); err != nil {
			return err
		}
		if err := checkNonTransactionalStatement(tokens); err != nil {
			return err
		}

		// Recursive scan of DO $$ ... $$ anonymous blocks
		if tokens[0].kind == tokenKindKeyword && tokens[0].value == "DO" {
			for _, tok := range tokens[1:] {
				if tok.kind == tokenKindDollarString {
					if err := inspectDoBlockBody(tok.value); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// ValidateMigrationSQL splits a SQL script into statements and runs RejectTxControl on all statements.
func ValidateMigrationSQL(sqlText string) ([]string, error) {
	stmts, err := Statements(sqlText)
	if err != nil {
		return nil, err
	}
	if len(stmts) == 0 {
		return nil, fmt.Errorf("%w: migration SQL cannot be empty", ErrInvalidMigrationSQL)
	}
	if err := RejectTxControl(stmts); err != nil {
		return nil, err
	}
	return stmts, nil
}

var reExplainable = regexp.MustCompile(`^(?i)(SELECT|INSERT|UPDATE|DELETE|MERGE|VALUES|WITH)\b`)

// IsExplainable checks if the first statement keyword is one of SELECT, INSERT, UPDATE, DELETE, MERGE, VALUES, WITH.
// Leading whitespace and comments are ignored.
func IsExplainable(sql string) bool {
	clean := StripLeadingCommentsAndWhitespace(sql)
	return reExplainable.MatchString(clean)
}

var validIdentifierPartPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

// SanitizeTableIdentifier validates and quotes a table identifier (either "table" -> public."table"
// or "schema.table" -> "schema"."table") using pgx.Identifier.Sanitize().
func SanitizeTableIdentifier(tableName string) (schemaName, relName, sanitized string, err error) {
	raw := strings.TrimSpace(tableName)
	if raw == "" || strings.ContainsRune(raw, 0) {
		return "", "", "", fmt.Errorf("%w: table_name is required", ErrInvalidBatchIdentifier)
	}
	parts := strings.Split(raw, ".")
	switch len(parts) {
	case 1:
		schemaName = "public"
		relName = strings.TrimSpace(parts[0])
	case 2:
		schemaName = strings.TrimSpace(parts[0])
		relName = strings.TrimSpace(parts[1])
	default:
		return "", "", "", fmt.Errorf("%w: invalid table_name %q", ErrInvalidBatchIdentifier, tableName)
	}
	if schemaName == "" || relName == "" || !validIdentifierPartPattern.MatchString(schemaName) || !validIdentifierPartPattern.MatchString(relName) {
		return "", "", "", fmt.Errorf("%w: invalid table_name %q", ErrInvalidBatchIdentifier, tableName)
	}
	sanitized = pgx.Identifier{schemaName, relName}.Sanitize()
	return schemaName, relName, sanitized, nil
}

// SanitizeColumnIdentifier validates and quotes a single column identifier using pgx.Identifier.Sanitize().
func SanitizeColumnIdentifier(colName string) (string, error) {
	raw := strings.TrimSpace(colName)
	if raw == "" || strings.ContainsRune(raw, 0) || strings.Contains(raw, ".") || !validIdentifierPartPattern.MatchString(raw) {
		return "", fmt.Errorf("%w: invalid column identifier %q", ErrInvalidBatchIdentifier, colName)
	}
	return pgx.Identifier{raw}.Sanitize(), nil
}

var (
	reBraceCursorFrom = regexp.MustCompile(`\{\{\s*cursor_from\s*\}\}`)
	reBraceCursorTo   = regexp.MustCompile(`\{\{\s*cursor_to\s*\}\}`)
	reVarCursorFrom   = regexp.MustCompile(`\$cursor_from\b`)
	reVarCursorTo     = regexp.MustCompile(`\$cursor_to\b`)
	rePositionalParam = regexp.MustCompile(`\$(\d+)\b`)
)

func hasPositionalParam(sqlText, num string) bool {
	matches := rePositionalParam.FindAllStringSubmatch(sqlText, -1)
	for _, m := range matches {
		if len(m) > 1 && m[1] == num {
			return true
		}
	}
	return false
}

// ValidateBatchSQLPlaceholders verifies that batch SQL includes both lower and upper cursor bound placeholders:
// either {{cursor_from}} & {{cursor_to}}, $cursor_from & $cursor_to, or $1 & $2.
func ValidateBatchSQLPlaceholders(sqlText string) error {
	hasBrace := reBraceCursorFrom.MatchString(sqlText) && reBraceCursorTo.MatchString(sqlText)
	hasVar := reVarCursorFrom.MatchString(sqlText) && reVarCursorTo.MatchString(sqlText)
	hasPos := hasPositionalParam(sqlText, "1") && hasPositionalParam(sqlText, "2")
	if !hasBrace && !hasVar && !hasPos {
		return fmt.Errorf("%w: batch SQL must contain cursor bound placeholders ({{cursor_from}} and {{cursor_to}} or $1 and $2)", ErrInvalidMigrationSQL)
	}
	return nil
}

// BindBatchSQL substitutes cursor_from and cursor_to values into a batch SQL template.
func BindBatchSQL(sqlText string, cursorFrom, cursorTo int64) string {
	fromStr := strconv.FormatInt(cursorFrom, 10)
	toStr := strconv.FormatInt(cursorTo, 10)

	out := reBraceCursorFrom.ReplaceAllString(sqlText, fromStr)
	out = reBraceCursorTo.ReplaceAllString(out, toStr)
	out = reVarCursorFrom.ReplaceAllString(out, fromStr)
	out = reVarCursorTo.ReplaceAllString(out, toStr)
	out = rePositionalParam.ReplaceAllStringFunc(out, func(match string) string {
		switch match {
		case "$1":
			return fromStr
		case "$2":
			return toStr
		default:
			return match
		}
	})
	return out
}

func isIntegerCatalogType(dataType, udtName string) bool {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "smallint", "integer", "bigint", "int2", "int4", "int8":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(udtName)) {
	case "smallint", "integer", "bigint", "int2", "int4", "int8":
		return true
	}
	return false
}

// ValidateBatchCatalog checks Target DB information_schema.columns to verify that
// batch_config.table_name, cursor_col, and optional updated_at_col exist, and that
// cursor_col is an integer column (smallint/integer/bigint).
func ValidateBatchCatalog(ctx context.Context, q interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}, bc *domain.BatchConfig) error {
	if bc == nil {
		return fmt.Errorf("%w: batch_config is required for batch execution mode", ErrInvalidBatchIdentifier)
	}
	if bc.BatchSize <= 0 {
		return fmt.Errorf("%w: batch_size must be greater than 0", ErrInvalidMigrationSQL)
	}

	schemaName, tableName, _, err := SanitizeTableIdentifier(bc.TableName)
	if err != nil {
		return err
	}
	if _, err := SanitizeColumnIdentifier(bc.CursorCol); err != nil {
		return err
	}
	cursorCol := strings.TrimSpace(bc.CursorCol)

	var dataType, udtName string
	err = q.QueryRow(ctx, `
		select lower(data_type), lower(udt_name)
		from information_schema.columns
		where lower(table_schema) = lower($1)
		  and lower(table_name) = lower($2)
		  and lower(column_name) = lower($3)`,
		schemaName, tableName, cursorCol).Scan(&dataType, &udtName)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: table %q or cursor column %q not found in target database", ErrInvalidBatchIdentifier, bc.TableName, bc.CursorCol)
	}
	if err != nil {
		return fmt.Errorf("query target catalog for cursor_col: %w", err)
	}

	if !isIntegerCatalogType(dataType, udtName) {
		return fmt.Errorf("%w: cursor_col %q has type %s (%s); must be smallint, integer, or bigint", ErrInvalidBatchCursorType, bc.CursorCol, dataType, udtName)
	}

	if updatedAtCol := strings.TrimSpace(bc.UpdatedAtCol); updatedAtCol != "" {
		if _, err := SanitizeColumnIdentifier(updatedAtCol); err != nil {
			return err
		}
		var exists bool
		err := q.QueryRow(ctx, `
			select exists (
				select 1 from information_schema.columns
				where lower(table_schema) = lower($1)
				  and lower(table_name) = lower($2)
				  and lower(column_name) = lower($3)
			)`, schemaName, tableName, updatedAtCol).Scan(&exists)
		if err != nil {
			return fmt.Errorf("query target catalog for updated_at_col: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: updated_at_col %q not found on table %q", ErrInvalidBatchIdentifier, updatedAtCol, bc.TableName)
		}
	}

	return nil
}

// DryRunGuard enforces at most 1 active dry-run per Target DB (sync.Map keyed by db_id)
// and an optional per-user cooldown window (1 dry-run / 2s per user_id).
type DryRunGuard struct {
	dbInFlight   sync.Map // map[uuid.UUID]struct{}
	userInFlight sync.Map // map[uuid.UUID]struct{}
	userLastRun  sync.Map // map[uuid.UUID]time.Time
	userCooldown time.Duration
}

func NewDryRunGuard(userCooldown time.Duration) *DryRunGuard {
	return &DryRunGuard{userCooldown: userCooldown}
}

func (g *DryRunGuard) Acquire(dbID, userID uuid.UUID) (func(), error) {
	if _, loaded := g.dbInFlight.LoadOrStore(dbID, struct{}{}); loaded {
		return nil, ErrDryRunBusy
	}

	if userID != uuid.Nil {
		if _, loaded := g.userInFlight.LoadOrStore(userID, struct{}{}); loaded {
			g.dbInFlight.Delete(dbID)
			return nil, ErrDryRunBusy
		}
		if g.userCooldown > 0 {
			if prev, ok := g.userLastRun.Load(userID); ok {
				if time.Since(prev.(time.Time)) < g.userCooldown {
					g.userInFlight.Delete(userID)
					g.dbInFlight.Delete(dbID)
					return nil, ErrDryRunBusy
				}
			}
			g.userLastRun.Store(userID, time.Now())
		}
	}

	var once sync.Once
	release := func() {
		once.Do(func() {
			if userID != uuid.Nil {
				if g.userCooldown > 0 {
					g.userLastRun.Store(userID, time.Now())
				}
				g.userInFlight.Delete(userID)
			}
			g.dbInFlight.Delete(dbID)
		})
	}
	return release, nil
}

// ValidateDefinitionLexical validates sql_up, sql_down, and batch placeholders lexically
// before opening any connection to the Target DB.
func ValidateDefinitionLexical(def domain.MigrationDefinition, actionType string) error {
	if actionType == domain.ActionResolveUncertain {
		return nil
	}

	if _, err := ValidateMigrationSQL(def.SQLUp); err != nil {
		return err
	}

	needDown := !def.IsIrreversible || actionType == domain.ActionRollback || actionType == domain.ActionBatchRevert
	if needDown {
		if _, err := ValidateMigrationSQL(def.SQLDown); err != nil {
			return err
		}
	}

	if def.ExecutionMode == domain.ExecutionModeBatch {
		if def.BatchConfig == nil {
			return fmt.Errorf("%w: batch_config is required for batch execution mode", ErrInvalidBatchIdentifier)
		}
		if def.BatchConfig.BatchSize <= 0 {
			return fmt.Errorf("%w: batch_size must be > 0", ErrInvalidMigrationSQL)
		}
		if _, _, _, err := SanitizeTableIdentifier(def.BatchConfig.TableName); err != nil {
			return err
		}
		if _, err := SanitizeColumnIdentifier(def.BatchConfig.CursorCol); err != nil {
			return err
		}
		if strings.TrimSpace(def.BatchConfig.UpdatedAtCol) != "" {
			if _, err := SanitizeColumnIdentifier(def.BatchConfig.UpdatedAtCol); err != nil {
				return err
			}
		}
		if err := ValidateBatchSQLPlaceholders(def.SQLUp); err != nil {
			return err
		}
		if needDown {
			if err := ValidateBatchSQLPlaceholders(def.SQLDown); err != nil {
				return err
			}
		}
	}

	return nil
}

func classifyDryRunExecError(err error) (DryRunResult, error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "57014", "55P03":
			return DryRunResult{
				Status:   DryRunStatusInconclusive,
				Warning:  fmt.Sprintf("dry-run inconclusive (SQLSTATE %s): %s", pgErr.Code, pgErr.Message),
				SQLState: pgErr.Code,
			}, nil
		default:
			return DryRunResult{}, fmt.Errorf("%w: %s (SQLSTATE %s)", ErrInvalidMigrationSQL, pgErr.Message, pgErr.Code)
		}
	}
	return DryRunResult{}, fmt.Errorf("%w: %v", ErrInvalidMigrationSQL, err)
}

// RunPreReviewDryRun executes the full Task 3 pre-review validation pipeline:
//  1. Lexical SQL guard (RejectTxControl, DO $$ ... $$ recursive scan, non-transactional statements)
//  2. Per-DB single-flight & per-user cooldown guard (429 DRY_RUN_BUSY)
//  3. Dedicated short-lived pgx.Conn with application_name = 'leafygo-dryrun-<proposal_id>'
//  4. Batch catalog & integer cursor type verification (422 INVALID_BATCH_IDENTIFIER / INVALID_BATCH_CURSOR_TYPE)
//  5. Dry-run execution inside BEGIN ... ROLLBACK with SET LOCAL lock_timeout = '1s' and statement_timeout = '3s',
//     treating SQLSTATE 57014 and 55P03 as dry_run_status = "inconclusive" and other SQL errors as 422 INVALID_MIGRATION_SQL.
func RunPreReviewDryRun(
	ctx context.Context,
	opener DedicatedConnOpener,
	guard *DryRunGuard,
	db domain.DBConnection,
	userID, proposalID uuid.UUID,
	def domain.MigrationDefinition,
	actionType string,
) (DryRunResult, error) {
	if actionType == domain.ActionResolveUncertain {
		return DryRunResult{Status: DryRunStatusSkipped}, nil
	}

	if err := ValidateDefinitionLexical(def, actionType); err != nil {
		return DryRunResult{}, err
	}

	if guard != nil {
		release, err := guard.Acquire(db.ID, userID)
		if err != nil {
			return DryRunResult{}, err
		}
		defer release()
	}

	appName := fmt.Sprintf("leafygo-dryrun-%s", proposalID.String())
	conn, err := opener.OpenDedicatedConn(ctx, db, appName)
	if err != nil {
		return DryRunResult{}, err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()

	if def.ExecutionMode == domain.ExecutionModeBatch {
		if err := ValidateBatchCatalog(ctx, conn, def.BatchConfig); err != nil {
			return DryRunResult{}, err
		}
	}

	if _, err := conn.Exec(ctx, "BEGIN;", pgx.QueryExecModeSimpleProtocol); err != nil {
		return DryRunResult{}, fmt.Errorf("dry-run begin: %w", err)
	}

	rolledBack := false
	rollbackTx := func() {
		if !rolledBack {
			rbCtx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 3*time.Second)
			defer cancel()
			_, _ = conn.Exec(rbCtx, "ROLLBACK;", pgx.QueryExecModeSimpleProtocol)
			rolledBack = true
		}
	}
	defer rollbackTx()

	if _, err := conn.Exec(ctx, "SET LOCAL lock_timeout = '1s'; SET LOCAL statement_timeout = '3s';", pgx.QueryExecModeSimpleProtocol); err != nil {
		return classifyDryRunExecError(err)
	}

	var scripts []string
	switch def.ExecutionMode {
	case domain.ExecutionModeBatch:
		// Bound-zero execution: cursor_from = 1, cursor_to = 0
		upSQL := BindBatchSQL(def.SQLUp, 1, 0)
		downSQL := BindBatchSQL(def.SQLDown, 1, 0)
		if actionType == domain.ActionRollback || actionType == domain.ActionBatchRevert {
			scripts = append(scripts, downSQL)
		} else {
			scripts = append(scripts, upSQL)
			if !def.IsIrreversible && strings.TrimSpace(def.SQLDown) != "" {
				scripts = append(scripts, downSQL)
			}
		}
	default:
		if actionType == domain.ActionRollback {
			scripts = append(scripts, def.SQLDown)
		} else {
			scripts = append(scripts, def.SQLUp)
			if !def.IsIrreversible && strings.TrimSpace(def.SQLDown) != "" {
				scripts = append(scripts, def.SQLDown)
			}
		}
	}

	for _, script := range scripts {
		if _, err := conn.Exec(ctx, script, pgx.QueryExecModeSimpleProtocol); err != nil {
			rollbackTx()
			return classifyDryRunExecError(err)
		}
	}

	rollbackTx()
	return DryRunResult{Status: DryRunStatusPassed}, nil
}
