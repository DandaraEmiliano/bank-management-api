package repository

import (
	"bank-management/models"
	"context"
	"errors"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func ConnectDB() (*gorm.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL é obrigatória")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(8)
	pool.SetMaxIdleConns(4)
	pool.SetConnMaxLifetime(15 * time.Minute)
	return db, nil
}

func Migrate(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(77400311)).Error; err != nil {
			return err
		}
		legacy := tx.Migrator().HasColumn("accounts", "balance") && !tx.Migrator().HasColumn("accounts", "balance_cents")
		if err := tx.AutoMigrate(&models.User{}, &models.Account{}, &models.Movement{}); err != nil {
			return err
		}
		if legacy {
			// A coluna anterior fica disponível para auditoria. A migração aborta se um saldo
			// legado violar o novo intervalo em centavos, preservando o banco original.
			return tx.Exec("UPDATE accounts SET balance_cents = ROUND(balance::numeric * 100)::bigint").Error
		}
		return nil
	})
}
