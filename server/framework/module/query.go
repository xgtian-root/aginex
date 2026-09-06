package module

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrQueryDatabaseRequired = errors.New(
		"module: query database is required",
	)
	ErrAuthorizationQueryUnscoped = errors.New(
		"module: authorization query executed without its registered scope",
	)
)

// QueryDatabase is the read-only database capability exposed to application
// modules. It intentionally does not expose the underlying *gorm.DB, raw SQL,
// migrations, connection pools, sessions, or mutation methods. Business
// writes must use services.Runtime.Writes.
type QueryDatabase struct {
	db            *gorm.DB
	authorization RequestAuthorization
	mode          AuthorizationMode
	guarded       bool
}

// NewQueryDatabase wraps a GORM database in the module read capability.
func NewQueryDatabase(db *gorm.DB) (*QueryDatabase, error) {
	if db == nil {
		return nil, ErrQueryDatabaseRequired
	}
	result := &QueryDatabase{db: db}
	if db.Statement != nil && db.Statement.Context != nil {
		ctx := db.Statement.Context
		result.authorization, result.guarded =
			RequestAuthorizationFromContext(ctx)
		if result.guarded {
			result.mode, result.guarded =
				requestAuthorizationModeFromContext(ctx)
			result.guarded = result.guarded &&
				(result.mode == AuthorizationObject ||
					result.mode == AuthorizationQuery)
		}
	}
	return result, nil
}

// Model starts a typed query. The model value is never returned to callers.
func (database *QueryDatabase) Model(value any) Query {
	if database == nil || database.db == nil {
		return Query{}
	}
	return Query{
		db:            database.db.Model(value),
		authorization: database.authorization,
		mode:          database.mode,
		guarded:       database.guarded,
	}
}

// Query is an immutable, fluent, read-only GORM query. Builder methods retain
// authorization state and expose no escape hatch to the underlying database.
type Query struct {
	db            *gorm.DB
	authorization RequestAuthorization
	mode          AuthorizationMode
	guarded       bool
	scoped        bool
	scopeContext  context.Context
}

// QueryResult is the bounded result returned by terminal query operations.
type QueryResult struct {
	RowsAffected int64
	Error        error
}

func (query Query) Where(predicate any, args ...any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Where(predicate, args...)
	})
}

func (query Query) Not(predicate any, args ...any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Not(predicate, args...)
	})
}

func (query Query) Or(predicate any, args ...any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Or(predicate, args...)
	})
}

func (query Query) Select(fields any, args ...any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Select(fields, args...)
	})
}

func (query Query) Distinct(args ...any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Distinct(args...)
	})
}

func (query Query) Order(value any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Order(value)
	})
}

func (query Query) Limit(limit int) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Limit(limit)
	})
}

func (query Query) Offset(offset int) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Offset(offset)
	})
}

func (query Query) Group(name string) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Group(name)
	})
}

func (query Query) Having(predicate any, args ...any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Having(predicate, args...)
	})
}

func (query Query) Joins(value string, args ...any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Joins(value, args...)
	})
}

func (query Query) Preload(name string, args ...any) Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Preload(name, args...)
	})
}

// LockForUpdate applies a row lock without exposing arbitrary clauses.
func (query Query) LockForUpdate() Query {
	return query.derive(func(db *gorm.DB) *gorm.DB {
		return db.Clauses(clause.Locking{Strength: "UPDATE"})
	})
}

// Scopes composes safe Query-to-Query transformations.
func (query Query) Scopes(scopes ...func(Query) Query) Query {
	for _, scope := range scopes {
		if scope != nil {
			query = scope(query)
		}
	}
	return query
}

func (query Query) First(destination any, conditions ...any) QueryResult {
	return query.execute(func(db *gorm.DB) *gorm.DB {
		return db.First(destination, conditions...)
	})
}

func (query Query) Take(destination any, conditions ...any) QueryResult {
	return query.execute(func(db *gorm.DB) *gorm.DB {
		return db.Take(destination, conditions...)
	})
}

func (query Query) Last(destination any, conditions ...any) QueryResult {
	return query.execute(func(db *gorm.DB) *gorm.DB {
		return db.Last(destination, conditions...)
	})
}

func (query Query) Find(destination any, conditions ...any) QueryResult {
	return query.execute(func(db *gorm.DB) *gorm.DB {
		return db.Find(destination, conditions...)
	})
}

func (query Query) Scan(destination any) QueryResult {
	return query.execute(func(db *gorm.DB) *gorm.DB {
		return db.Scan(destination)
	})
}

func (query Query) Count(count *int64) QueryResult {
	return query.execute(func(db *gorm.DB) *gorm.DB {
		return db.Count(count)
	})
}

func (query Query) Pluck(column string, destination any) QueryResult {
	return query.execute(func(db *gorm.DB) *gorm.DB {
		return db.Pluck(column, destination)
	})
}

func (query Query) Rows() (*sql.Rows, error) {
	database, err := query.executionDatabase()
	if err != nil {
		return nil, err
	}
	rows, err := database.Rows()
	query.recordExecution()
	return rows, err
}

func (query Query) derive(
	transform func(*gorm.DB) *gorm.DB,
) Query {
	if query.db == nil || transform == nil {
		return query
	}
	query.db = transform(query.db)
	return query
}

func (query Query) execute(
	operation func(*gorm.DB) *gorm.DB,
) QueryResult {
	if query.db == nil || operation == nil {
		return QueryResult{Error: ErrQueryDatabaseRequired}
	}
	database, err := query.executionDatabase()
	if err != nil {
		return QueryResult{Error: err}
	}
	result := operation(database)
	query.recordExecution()
	if result == nil {
		return QueryResult{Error: ErrQueryDatabaseRequired}
	}
	return QueryResult{
		RowsAffected: result.RowsAffected,
		Error:        result.Error,
	}
}

func (query Query) executionDatabase() (*gorm.DB, error) {
	if query.db == nil {
		return nil, ErrQueryDatabaseRequired
	}
	if query.guarded && query.authorization.usage == nil {
		return nil, fmt.Errorf(
			"%w: authorization decision is unavailable",
			ErrAuthorizationQueryUnscoped,
		)
	}
	if query.guarded && query.authorization.Violated() {
		return nil, ErrAuthorizationQueryUnscoped
	}
	if query.guarded && !query.scoped {
		query.authorization.usage.queryViolation.Store(true)
		return nil, ErrAuthorizationQueryUnscoped
	}
	if !query.scoped {
		return query.db, nil
	}
	ctx := query.scopeContext
	if ctx == nil {
		return nil, fmt.Errorf(
			"%w: authorization scope context is unavailable",
			ErrAuthorizationQueryUnscoped,
		)
	}
	candidate := query.db.Session(&gorm.Session{Initialized: true})
	userWhere, hasUserWhere := candidate.Statement.Clauses["WHERE"]
	delete(candidate.Statement.Clauses, "WHERE")
	scoped, err := query.authorization.policy.Scope(
		ctx,
		query.authorization.actor,
		query.authorization.scope,
		query.authorization.permission,
		candidate.WithContext(ctx),
	)
	if err != nil {
		return nil, err
	}
	if scoped == nil {
		return nil, fmt.Errorf(
			"%w: authorization policy returned a nil query",
			ErrInvalid,
		)
	}
	if hasUserWhere {
		where, ok := userWhere.Expression.(clause.Where)
		if !ok {
			return nil, fmt.Errorf(
				"%w: unsupported query predicate",
				ErrAuthorizationQueryUnscoped,
			)
		}
		scoped = scoped.Where(clause.And(where.Exprs...))
	}
	return scoped, nil
}

func (query Query) recordExecution() {
	if query.scoped &&
		query.authorization.usage != nil &&
		!query.authorization.Violated() {
		query.authorization.usage.query.Store(true)
	}
}
