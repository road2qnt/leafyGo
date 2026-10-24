package migration

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Statements splits a SQL string into individual statements on semicolons outside of
// quotes and comments. It trims statements and ignores empty ones.
// It returns an error if quotes, dollar-quotes, or block comments are unclosed.
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
				return nil, errors.New("unclosed block comment in SQL")
			}
			continue
		}

		// Check for escape string E'...' or e'...'
		if (c == 'E' || c == 'e') && i+1 < n && sql[i+1] == '\'' {
			i += 2
			closed := false
			for i < n {
				if sql[i] == '\\' {
					i += 2
					continue
				}
				if sql[i] == '\'' {
					closed = true
					i++
					break
				}
				i++
			}
			if !closed {
				return nil, errors.New("unclosed escape string literal in SQL")
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
				return nil, errors.New("unclosed string literal in SQL")
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
				return nil, errors.New("unclosed identifier quote in SQL")
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
					return nil, errors.New("unclosed dollar quote in SQL")
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

func parseDollarTag(s string, start int) (tag string, nextIdx int, ok bool) {
	if s[start] != '$' {
		return "", start, false
	}
	idx := start + 1
	n := len(s)
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

var txControlPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^BEGIN\b`),
	regexp.MustCompile(`(?i)^START\s+TRANSACTION\b`),
	regexp.MustCompile(`(?i)^COMMIT\b`),
	regexp.MustCompile(`(?i)^END\b`),
	regexp.MustCompile(`(?i)^ROLLBACK\b`),
	regexp.MustCompile(`(?i)^ABORT\b`),
	regexp.MustCompile(`(?i)^SAVEPOINT\b`),
	regexp.MustCompile(`(?i)^RELEASE\b`),
	regexp.MustCompile(`(?i)^PREPARE\s+TRANSACTION\b`),
	regexp.MustCompile(`(?i)^COMMIT\s+PREPARED\b`),
	regexp.MustCompile(`(?i)^ROLLBACK\s+PREPARED\b`),
}

// RejectTxControl inspects SQL statements and rejects transaction control statements.
// Note: "BEGIN ATOMIC" in PG14+ function bodies is a documented false positive and will be rejected.
func RejectTxControl(statements []string) error {
	for _, stmt := range statements {
		clean := StripLeadingCommentsAndWhitespace(stmt)
		if clean == "" {
			continue
		}

		for _, pat := range txControlPatterns {
			if pat.MatchString(clean) {
				return fmt.Errorf("transaction control statement %q is not permitted in migrations", pat.FindString(clean))
			}
		}
	}
	return nil
}

var reExplainable = regexp.MustCompile(`^(?i)(SELECT|INSERT|UPDATE|DELETE|MERGE|VALUES|WITH)\b`)

// IsExplainable checks if the first statement keyword is one of SELECT, INSERT, UPDATE, DELETE, MERGE, VALUES, WITH.
// Leading whitespace and comments are ignored.
func IsExplainable(sql string) bool {
	clean := StripLeadingCommentsAndWhitespace(sql)
	return reExplainable.MatchString(clean)
}
