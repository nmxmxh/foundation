package database

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/domainerr"
)

// Postgres integrity-constraint SQLSTATE codes (class 23) that map to a
// distinct client-facing outcome. Codes are stable across Postgres versions;
// see https://www.postgresql.org/docs/current/errcodes-appendix.html.
const (
	sqlStateNotNullViolation    = "23502" // a required column was NULL
	sqlStateForeignKeyViolation = "23503" // a referenced row is missing
	sqlStateUniqueViolation     = "23505" // a unique index rejected a duplicate
	sqlStateCheckViolation      = "23514" // a CHECK constraint failed
	sqlStateExclusionViolation  = "23P01" // an exclusion constraint conflicted
)

// ClassifyKind maps an infrastructure error to a stable domain error Kind. It
// recognizes Postgres integrity-constraint violations and pgx's no-rows
// sentinel; ok is false for anything it does not recognize, so callers keep
// their own fallback rather than mislabeling an unknown failure.
//
// The classification is deliberately conservative: a unique violation is a
// Conflict, a missing referenced row (foreign key) or exclusion conflict is a
// Conflict, and a NULL or CHECK violation is a Validation error. This is the
// mapping services would otherwise hand-roll — and several collapse distinct
// causes (conflict, not-found, foreign key) into one generic class, which is
// exactly what a shared classifier prevents.
func ClassifyKind(err error) (domainerr.Kind, bool) {
	if err == nil {
		return "", false
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domainerr.KindNotFound, true
	}
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return "", false
	}
	switch pgErr.Code {
	case sqlStateUniqueViolation, sqlStateForeignKeyViolation, sqlStateExclusionViolation:
		return domainerr.KindConflict, true
	case sqlStateNotNullViolation, sqlStateCheckViolation:
		return domainerr.KindValidation, true
	default:
		return "", false
	}
}

// classifyCode returns the stable domainerr Code for a recognized error, so a
// classified error carries the specific cause (not just its HTTP-status Kind).
func classifyCode(err error) string {
	if errors.Is(err, pgx.ErrNoRows) {
		return "not_found"
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case sqlStateUniqueViolation:
			return "unique_violation"
		case sqlStateForeignKeyViolation:
			return "foreign_key_violation"
		case sqlStateExclusionViolation:
			return "exclusion_violation"
		case sqlStateNotNullViolation:
			return "not_null_violation"
		case sqlStateCheckViolation:
			return "check_violation"
		}
	}
	return "unknown_error"
}

// Classify wraps a recognized infrastructure error as a typed *domainerr.Error,
// preserving the original as the diagnostic Cause. It is idempotent: a nil
// error stays nil and an error that already carries a *domainerr.Error is
// returned unchanged, so a service can classify defensively at any layer
// without double-wrapping. An unrecognized error is returned unchanged so the
// caller's own handling still applies.
//
// The message is intentionally generic — WriteHTTP never serializes the cause,
// so the raw Postgres detail (which can name columns or constraint values)
// never reaches the client, while the cause remains available for logs and
// errors.Is/As chains.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*domainerr.Error](err); ok {
		return err
	}
	kind, ok := ClassifyKind(err)
	if !ok {
		return err
	}
	return domainerr.New(kind, classifyCode(err), classifyMessage(kind), err)
}

func classifyMessage(kind domainerr.Kind) string {
	switch kind {
	case domainerr.KindConflict:
		return "the request conflicts with existing data"
	case domainerr.KindValidation:
		return "the request violates a data constraint"
	case domainerr.KindNotFound:
		return "the requested record was not found"
	default:
		return "operation failed"
	}
}
