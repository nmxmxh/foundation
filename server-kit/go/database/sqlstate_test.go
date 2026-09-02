package database

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

func TestClassifyMapsSQLStateToDomainKind(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind domainerr.Kind
		wantCode string
	}{
		{"unique", &pgconn.PgError{Code: sqlStateUniqueViolation}, domainerr.KindConflict, "unique_violation"},
		{"foreign_key", &pgconn.PgError{Code: sqlStateForeignKeyViolation}, domainerr.KindConflict, "foreign_key_violation"},
		{"exclusion", &pgconn.PgError{Code: sqlStateExclusionViolation}, domainerr.KindConflict, "exclusion_violation"},
		{"not_null", &pgconn.PgError{Code: sqlStateNotNullViolation}, domainerr.KindValidation, "not_null_violation"},
		{"check", &pgconn.PgError{Code: sqlStateCheckViolation}, domainerr.KindValidation, "check_violation"},
		{"no_rows", pgx.ErrNoRows, domainerr.KindNotFound, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A wrapped error still classifies: repositories return fmt.Errorf
			// chains, not bare pg errors.
			classified := Classify(fmt.Errorf("repo insert: %w", tc.err))
			if got := domainerr.KindOf(classified); got != tc.wantKind {
				t.Fatalf("KindOf = %q, want %q", got, tc.wantKind)
			}
			if got := domainerr.CodeOf(classified); got != tc.wantCode {
				t.Fatalf("CodeOf = %q, want %q", got, tc.wantCode)
			}
			// The original cause is preserved for logs and errors.Is chains.
			if !errors.Is(classified, tc.err) {
				t.Fatalf("Classify() dropped the diagnostic cause")
			}
		})
	}
}

func TestClassifyLeavesUnknownAndTypedErrorsAlone(t *testing.T) {
	if got := Classify(nil); got != nil {
		t.Fatalf("Classify(nil) = %v, want nil", got)
	}

	unknown := errors.New("connection refused")
	if got := Classify(unknown); got != unknown {
		t.Fatalf("Classify(unknown) rewrote an unrecognized error")
	}
	if _, ok := ClassifyKind(unknown); ok {
		t.Fatal("ClassifyKind() recognized an unrelated error")
	}

	// An already-typed domain error is returned unchanged, so classifying at a
	// second layer never double-wraps or reclassifies a deliberate decision.
	typed := domainerr.Forbidden("dish_create_forbidden", "forbidden")
	wrapped := fmt.Errorf("service: %w", typed)
	if got := Classify(wrapped); got != wrapped {
		t.Fatalf("Classify() rewrote an already-typed domain error")
	}
}
