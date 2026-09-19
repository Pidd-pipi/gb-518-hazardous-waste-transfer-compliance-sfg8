package repository

import (
	"context"

	"gorm.io/gorm"
)

type txContextKey struct{}

// WithTx stores an ongoing GORM transaction on the context so every repository
// that shares the context joins the same unit of work.
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

func txFromContext(ctx context.Context) *gorm.DB {
	tx, _ := ctx.Value(txContextKey{}).(*gorm.DB)
	return tx
}

// conn returns the ongoing transaction for ctx when one exists, otherwise the
// pool connection. This keeps cross-aggregate writes (联单状态 + 资质快照 + 审计)
// atomic without leaking GORM into the service layer.
func conn(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx := txFromContext(ctx); tx != nil {
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}

// TxManager runs a function inside a single database transaction, propagating
// the transaction through the context to all repositories.
type TxManager struct {
	db *gorm.DB
}

func NewTxManager(db *gorm.DB) *TxManager { return &TxManager{db: db} }

func (m *TxManager) InTx(ctx context.Context, fn func(txCtx context.Context) error) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(WithTx(ctx, tx))
	})
}

// inUnitOfWork runs fn against the ambient transaction stored on ctx. When no
// ambient transaction exists it opens one for the two statements, so the same
// audited-write method works both standalone and inside a larger aggregate
// transaction without nesting savepoints.
func inUnitOfWork(ctx context.Context, db *gorm.DB, fn func(q *gorm.DB) error) error {
	if tx := txFromContext(ctx); tx != nil {
		return fn(tx.WithContext(ctx))
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(tx) })
}
