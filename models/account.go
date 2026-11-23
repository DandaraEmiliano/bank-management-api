package models

import (
	"time"

	"gorm.io/gorm"
)

type Account struct {
	gorm.Model
	UserID       uint   `json:"user_id" gorm:"index;not null"`
	Name         string `json:"name" gorm:"size:80;not null;default:Conta"`
	BalanceCents int64  `json:"balance_cents" gorm:"not null;default:0;check:account_balance_valid,balance_cents BETWEEN 0 AND 9000000000000"`
}

type Movement struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	UserID         uint      `json:"-" gorm:"index:idx_movements_user_id,priority:1;uniqueIndex:idx_movement_request,priority:1;not null"`
	FromAccountID  *uint     `json:"from_account_id"`
	ToAccountID    *uint     `json:"to_account_id"`
	AmountCents    int64     `json:"amount_cents" gorm:"not null;check:movement_amount_valid,amount_cents BETWEEN 1 AND 9000000000000"`
	Description    string    `json:"description" gorm:"size:160;not null"`
	IdempotencyKey *string   `json:"-" gorm:"size:80;uniqueIndex:idx_movement_request,priority:2"`
	CreatedAt      time.Time `json:"created_at"`
	FromAccount    *Account  `json:"-" gorm:"foreignKey:FromAccountID;constraint:OnDelete:RESTRICT"`
	ToAccount      *Account  `json:"-" gorm:"foreignKey:ToAccountID;constraint:OnDelete:RESTRICT"`
}

const MaxCents int64 = 9_000_000_000_000
