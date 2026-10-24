package target

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRunErrorUnwrap(t *testing.T) {
	orig := errors.New("underlying query error")
	re := &RunError{Err: orig, Uncertain: true}

	if re.Error() != "underlying query error" {
		t.Errorf("expected Error() 'underlying query error', got %q", re.Error())
	}
	if !errors.Is(re, orig) {
		t.Errorf("expected errors.Is to find underlying error")
	}
	if !re.Uncertain {
		t.Errorf("expected Uncertain to be true")
	}
}

func TestClassifyRunError(t *testing.T) {
	err := errors.New("something broke")

	e1 := classifyRunError(phaseConnect, err)
	var re1 *RunError
	if !errors.As(e1, &re1) || re1.Uncertain {
		t.Errorf("expected connect error to have Uncertain=false, got %v", e1)
	}

	e2 := classifyRunError(phaseStatement, err)
	var re2 *RunError
	if !errors.As(e2, &re2) || re2.Uncertain {
		t.Errorf("expected statement error to have Uncertain=false, got %v", e2)
	}

	e3 := classifyRunError(phaseCommit, err)
	var re3 *RunError
	if !errors.As(e3, &re3) || !re3.Uncertain {
		t.Errorf("expected commit error to have Uncertain=true, got %v", e3)
	}

	if classifyRunError(phaseCommit, nil) != nil {
		t.Errorf("expected nil error to classify as nil")
	}
}

func TestSanitizeTargetError(t *testing.T) {
	t.Run("redact URL credentials", func(t *testing.T) {
		err := errors.New("failed to connect to postgres://secretuser:supersecretpass@localhost:5432/mydb?sslmode=disable")
		got := sanitizeTargetError(err)
		if strings.Contains(got, "supersecretpass") || strings.Contains(got, "secretuser") {
			t.Errorf("leaked credentials: %s", got)
		}
		if !strings.Contains(got, "postgres://[REDACTED]@localhost:5432/mydb") {
			t.Errorf("expected redacted url, got %s", got)
		}
	})

	t.Run("redact password key-value", func(t *testing.T) {
		err := errors.New("connection string invalid: host=localhost password=mysecretpassword user=dbuser")
		got := sanitizeTargetError(err)
		if strings.Contains(got, "mysecretpassword") {
			t.Errorf("leaked password: %s", got)
		}
		if !strings.Contains(got, "password=[REDACTED]") {
			t.Errorf("expected password=[REDACTED], got %s", got)
		}
	})

	t.Run("flatten newlines", func(t *testing.T) {
		err := errors.New("line 1\nline 2\r\nline 3\rline 4")
		got := sanitizeTargetError(err)
		if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
			t.Errorf("contains newlines: %q", got)
		}
		if got != "line 1 line 2 line 3 line 4" {
			t.Errorf("unexpected output: %q", got)
		}
	})

	t.Run("truncate to 512 characters", func(t *testing.T) {
		err := errors.New(strings.Repeat("a", 1000))
		got := sanitizeTargetError(err)
		if len(got) != 512 {
			t.Errorf("expected len 512, got %d", len(got))
		}
	})

	t.Run("pgconn.PgError formatting", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Code:     "42P01",
			Message:  "relation \"users\" does not exist",
			Detail:   "Table was dropped",
			Position: 15,
		}
		got := sanitizeTargetError(pgErr)
		if !strings.Contains(got, "[42P01]") {
			t.Errorf("missing code: %s", got)
		}
		if !strings.Contains(got, "relation \"users\" does not exist") {
			t.Errorf("missing message: %s", got)
		}
		if !strings.Contains(got, "detail: Table was dropped") {
			t.Errorf("missing detail: %s", got)
		}
		if !strings.Contains(got, "position: 15") {
			t.Errorf("missing position: %s", got)
		}
	})

	t.Run("nil error", func(t *testing.T) {
		if got := sanitizeTargetError(nil); got != "" {
			t.Errorf("expected empty string for nil error, got %q", got)
		}
	})
}
