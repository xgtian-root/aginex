package uow

import (
	"database/sql"
	"errors"
	"sync/atomic"

	"gorm.io/gorm"
)

var ErrActiveTransactionRequired = errors.New("unit of work active transaction from the same database is required")

const transactionSetting = "aginex:uow:transaction"

type transactionScope struct {
	database *sql.DB
	pool     gorm.ConnPool
	active   atomic.Bool
}

// RequireTransaction verifies that tx belongs to an active Run callback on
// db's connection pool. It also rejects a handle manually committed or rolled
// back inside the callback. Clones made with WithContext retain the scope.
func RequireTransaction(db, tx *gorm.DB) error {
	if db == nil || tx == nil || tx.Statement == nil {
		return ErrActiveTransactionRequired
	}
	value, ok := tx.Get(transactionSetting)
	scope, valid := value.(*transactionScope)
	if !ok || !valid || scope == nil || !scope.active.Load() ||
		tx.Statement.ConnPool != scope.pool {
		return ErrActiveTransactionRequired
	}
	database, err := db.DB()
	if err != nil || database != scope.database {
		return ErrActiveTransactionRequired
	}
	if err := tx.Exec("SELECT 1").Error; err != nil {
		return errors.Join(ErrActiveTransactionRequired, err)
	}
	return nil
}
