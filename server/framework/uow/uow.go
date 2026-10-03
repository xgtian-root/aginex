package uow

import (
	"context"
	"errors"

	"github.com/xgtian-root/aginex/server/framework/audit"
	"gorm.io/gorm"
)

var (
	ErrDatabaseRequired = errors.New("unit of work database is required")
	ErrRecorderRequired = errors.New("unit of work audit recorder is required")
	ErrMutationRequired = errors.New("unit of work mutation is required")
)

// Recorder persists an audit event using the same transaction as the business
// mutation. Implementations must not replace tx with another database handle.
type Recorder interface {
	Record(context.Context, *gorm.DB, audit.Event) error
}

type UnitOfWork struct {
	db       *gorm.DB
	recorder Recorder
}

func New(db *gorm.DB, recorder Recorder) (*UnitOfWork, error) {
	if db == nil {
		return nil, ErrDatabaseRequired
	}
	if recorder == nil {
		return nil, ErrRecorderRequired
	}
	return &UnitOfWork{db: db, recorder: recorder}, nil
}

// Run commits only when both the business mutation and its audit event
// succeed. The mutation returns the event after it has determined generated
// resource identifiers and sanitized before/after values.
func (u *UnitOfWork) Run(
	ctx context.Context,
	mutation func(*gorm.DB) (audit.Event, error),
) error {
	if mutation == nil {
		return ErrMutationRequired
	}
	database, err := u.db.DB()
	if err != nil {
		return err
	}
	return u.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scope := &transactionScope{database: database, pool: tx.Statement.ConnPool}
		scope.active.Store(true)
		defer scope.active.Store(false)
		// Set returns a statement-owned handle. Restore GORM's reusable session
		// semantics so one query's model/conditions cannot leak into the next.
		tx = tx.Set(transactionSetting, scope).Session(&gorm.Session{})
		event, err := mutation(tx)
		if err != nil {
			return err
		}
		if err := event.Validate(); err != nil {
			return err
		}
		return u.recorder.Record(ctx, tx, event)
	})
}
