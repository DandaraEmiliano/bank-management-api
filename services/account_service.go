package services

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"bank-management/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAmount       = errors.New("Informe um valor em reais com até duas casas decimais.")
	ErrAccounts     = errors.New("Escolha contas válidas e diferentes.")
	ErrBalance      = errors.New("Saldo insuficiente.")
	ErrBalanceLimit = errors.New("O lançamento excede o limite de saldo da conta.")
	ErrKeyConflict  = errors.New("Esta chave já foi usada em outro lançamento.")
	moneyPattern    = regexp.MustCompile(`^[0-9]{1,11}(\.[0-9]{1,2})?$`)
)

func ReadMoney(legacy json.RawMessage, cents *int64) (int64, error) {
	if cents != nil && len(legacy) > 0 {
		return 0, ErrAmount
	}
	if cents != nil {
		if *cents < 0 || *cents > models.MaxCents {
			return 0, ErrAmount
		}
		return *cents, nil
	}
	if len(legacy) == 0 {
		return 0, nil
	}
	value := string(legacy)
	if !moneyPattern.MatchString(value) {
		return 0, ErrAmount
	}
	whole, fraction, _ := strings.Cut(value, ".")
	reais, _ := strconv.ParseInt(whole, 10, 64)
	centavos, _ := strconv.ParseInt((fraction + "00")[:2], 10, 64)
	amount := reais*100 + centavos
	if amount > models.MaxCents {
		return 0, ErrAmount
	}
	return amount, nil
}

func PostMovement(db *gorm.DB, movement *models.Movement) (bool, error) {
	from, to := movement.FromAccountID, movement.ToAccountID
	if from == nil && to == nil || from != nil && to != nil && *from == *to {
		return false, ErrAccounts
	}
	if movement.AmountCents <= 0 || movement.AmountCents > models.MaxCents {
		return false, ErrAmount
	}
	replayed := false
	err := db.Transaction(func(tx *gorm.DB) error {
		var owner models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id").First(&owner, movement.UserID).Error; err != nil {
			return err
		}
		if movement.IdempotencyKey != nil {
			var saved models.Movement
			err := tx.Where("user_id = ? AND idempotency_key = ?", movement.UserID, *movement.IdempotencyKey).First(&saved).Error
			if err == nil {
				if !sameAccount(from, saved.FromAccountID) || !sameAccount(to, saved.ToAccountID) ||
					saved.AmountCents != movement.AmountCents || saved.Description != movement.Description {
					return ErrKeyConflict
				}
				*movement, replayed = saved, true
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if from != nil {
			if err := changeBalance(tx, movement.UserID, *from, -movement.AmountCents); err != nil {
				return err
			}
		}
		if to != nil {
			if err := changeBalance(tx, movement.UserID, *to, movement.AmountCents); err != nil {
				return err
			}
		}
		return tx.Create(movement).Error
	})
	return replayed, err
}

func sameAccount(a, b *uint) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func changeBalance(tx *gorm.DB, owner, id uint, delta int64) error {
	var account models.Account
	if err := tx.Where("user_id = ?", owner).First(&account, id).Error; err != nil {
		return err
	}
	balance := account.BalanceCents + delta
	switch {
	case balance < 0:
		return ErrBalance
	case balance > models.MaxCents:
		return ErrBalanceLimit
	}
	return tx.Model(&account).Update("balance_cents", balance).Error
}
