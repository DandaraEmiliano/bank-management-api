package api

import (
	"bank-management/middleware"
	"bank-management/models"
	"bank-management/services"
	"bank-management/utils"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type server struct {
	db     *gorm.DB
	tokens utils.Tokens
	secure bool
}

type accountView struct {
	models.Account
	Balance json.Number `json:"balance"`
}

var errAccountLimit = errors.New("Limite de 100 contas por usuário.")

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func SetupRoutes(r *gin.Engine, db *gorm.DB, tokens utils.Tokens, secure bool) {
	s := server{db: db, tokens: tokens, secure: secure}
	r.Use(requestPolicy)
	r.GET("/healthz", s.health)
	authSlots := make(chan struct{}, 4)
	r.POST("/users", limitAuth(authSlots), s.register)
	r.POST("/login", limitAuth(authSlots), s.login)
	r.POST("/logout", s.logout)

	auth := r.Group("/auth", middleware.AuthMiddleware(tokens))
	auth.GET("/users", s.profile)
	auth.GET("/accounts", s.accounts)
	auth.GET("/overview", s.overview)
	auth.POST("/accounts", s.createAccount)
	auth.POST("/accounts/transfer", s.movement(true))
	auth.POST("/accounts/entries", s.movement(false))
}

func requestPolicy(c *gin.Context) {
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "same-origin")
	c.Header("Content-Security-Policy", "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	if strings.HasPrefix(c.Request.URL.Path, "/auth/") || c.Request.URL.Path == "/login" {
		c.Header("Cache-Control", "no-store")
	}
	if c.Request.Method == http.MethodPost {
		if raw := c.GetHeader("Origin"); raw != "" {
			origin, err := url.Parse(raw)
			if err != nil || (origin.Scheme != "https" && origin.Scheme != "http") ||
				!strings.EqualFold(origin.Host, c.Request.Host) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Origem não permitida."})
				return
			}
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	}
	c.Next()
}

func limitAuth(slots chan struct{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			c.Next()
		default:
			c.Header("Retry-After", "2")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Aguarde e tente novamente."})
		}
	}
}

func (s server) health(c *gin.Context) {
	pool, err := s.db.DB()
	if err == nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		err = pool.PingContext(ctx)
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s server) register(c *gin.Context) {
	var input struct {
		Name     string `json:"name" binding:"required,max=80"`
		Email    string `json:"email" binding:"required,email,max=254"`
		Password string `json:"password" binding:"required"`
	}
	if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Name) == "" ||
		len(input.Password) < 10 || len(input.Password) > 72 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Informe nome, e-mail válido e senha de 10 a 72 bytes."})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		internal(c)
		return
	}
	user := models.User{Name: strings.TrimSpace(input.Name), Email: strings.ToLower(input.Email), Password: string(hash)}
	if err := s.db.WithContext(c.Request.Context()).Create(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusConflict, gin.H{"error": "E-mail já cadastrado."})
			return
		}
		internal(c)
		return
	}
	c.JSON(http.StatusCreated, user)
}

func (s server) login(c *gin.Context) {
	var input struct {
		Email    string `json:"email" binding:"required,email,max=254"`
		Password string `json:"password" binding:"required"`
	}
	if c.ShouldBindJSON(&input) != nil || len(input.Password) > 72 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Informe e-mail e senha válidos."})
		return
	}
	var user models.User
	err := s.db.WithContext(c.Request.Context()).
		Order("id").Where("LOWER(email) = ?", strings.ToLower(input.Email)).First(&user).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		internal(c)
		return
	}
	// Mesmo custo de bcrypt quando o e-mail ainda não existe.
	hash := user.Password
	if err != nil {
		hash = "$2a$10$123456789012345678901utN.CQFaFjH1EJ0OIB/jU2H3AKq1lUDK"
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)) != nil || err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "E-mail ou senha inválidos."})
		return
	}
	token, err := s.tokens.Generate(user.ID)
	if err != nil {
		internal(c)
		return
	}
	s.cookie(c, token, int(utils.SessionLifetime.Seconds()))
	c.JSON(http.StatusOK, gin.H{"token": token})
}

func (s server) cookie(c *gin.Context, value string, age int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(middleware.CookieName, value, age, "/", "", s.secure, true)
}

func (s server) logout(c *gin.Context) {
	s.cookie(c, "", -1)
	c.Status(http.StatusNoContent)
}

func owner(c *gin.Context) uint { return c.MustGet("userID").(uint) }

func (s server) profile(c *gin.Context) {
	var user models.User
	err := s.db.WithContext(c.Request.Context()).First(&user, owner(c)).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Sessão inválida ou expirada."})
	case err != nil:
		internal(c)
	default:
		c.JSON(http.StatusOK, []models.User{user})
	}
}

func listAccounts(db *gorm.DB, id uint) ([]accountView, error) {
	var accounts []models.Account
	err := db.Where("user_id = ?", id).Order("id").Find(&accounts).Error
	views := make([]accountView, 0, len(accounts))
	for _, account := range accounts {
		views = append(views, viewAccount(account))
	}
	return views, err
}

func viewAccount(account models.Account) accountView {
	cents := account.BalanceCents
	return accountView{Account: account, Balance: json.Number(fmt.Sprintf("%d.%02d", cents/100, cents%100))}
}

func (s server) accounts(c *gin.Context) {
	accounts, err := listAccounts(s.db.WithContext(c.Request.Context()), owner(c))
	if err != nil {
		internal(c)
		return
	}
	c.JSON(http.StatusOK, accounts)
}

func (s server) overview(c *gin.Context) {
	var accounts []accountView
	movements := make([]models.Movement, 0)
	err := s.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		accounts, err = listAccounts(tx, owner(c))
		if err != nil {
			return err
		}
		return tx.Where("user_id = ?", owner(c)).Order("id DESC").Limit(100).Find(&movements).Error
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		internal(c)
		return
	}
	c.JSON(http.StatusOK, gin.H{"accounts": accounts, "movements": movements})
}

func (s server) createAccount(c *gin.Context) {
	var input struct {
		Name         *string         `json:"name" binding:"omitempty,max=80"`
		UserID       *uint           `json:"user_id"`
		Balance      json.RawMessage `json:"balance"`
		BalanceCents *int64          `json:"balance_cents"`
	}
	if c.ShouldBindJSON(&input) != nil ||
		(input.Name != nil && strings.TrimSpace(*input.Name) == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados da conta inválidos."})
		return
	}
	id := owner(c)
	if input.UserID != nil && *input.UserID != id {
		c.JSON(http.StatusForbidden, gin.H{"error": "Crie contas apenas no seu espaço."})
		return
	}
	value, err := services.ReadMoney(input.Balance, input.BalanceCents)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	account := models.Account{UserID: id}
	if input.Name != nil {
		account.Name = strings.TrimSpace(*input.Name)
	}
	err = s.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var user models.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&user, id).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&models.Account{}).Where("user_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count >= 100 {
			return errAccountLimit
		}
		if err := tx.Create(&account).Error; err != nil || value == 0 {
			return err
		}
		movement := models.Movement{UserID: id, ToAccountID: &account.ID, AmountCents: value, Description: "Saldo inicial"}
		if err := tx.Create(&movement).Error; err != nil {
			return err
		}
		account.BalanceCents = value
		return tx.Model(&account).Update("balance_cents", value).Error
	})
	if errors.Is(err, errAccountLimit) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		internal(c)
		return
	}
	c.JSON(http.StatusCreated, viewAccount(account))
}

func (s server) movement(transfer bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			From        *uint           `json:"from_account_id"`
			To          *uint           `json:"to_account_id"`
			Amount      json.RawMessage `json:"amount"`
			AmountCents *int64          `json:"amount_cents"`
			Description string          `json:"description"`
		}
		if c.ShouldBindJSON(&input) != nil || utf8.RuneCountInString(input.Description) > 160 ||
			(transfer && (input.From == nil || input.To == nil)) ||
			(!transfer && (input.From == nil) == (input.To == nil)) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dados do lançamento inválidos."})
			return
		}
		amount, err := services.ReadMoney(input.Amount, input.AmountCents)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		movement := models.Movement{
			UserID: owner(c), FromAccountID: input.From, ToAccountID: input.To,
			AmountCents: amount, Description: strings.TrimSpace(input.Description),
		}
		if key := c.GetHeader("Idempotency-Key"); key != "" {
			if !keyPattern.MatchString(key) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Chave de idempotência inválida."})
				return
			}
			movement.IdempotencyKey = &key
		}
		replayed, err := services.PostMovement(s.db.WithContext(c.Request.Context()), &movement)
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "Conta não encontrada."})
		case errors.Is(err, services.ErrAmount), errors.Is(err, services.ErrAccounts), errors.Is(err, services.ErrBalance), errors.Is(err, services.ErrBalanceLimit):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case errors.Is(err, services.ErrKeyConflict):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case err != nil:
			internal(c)
		default:
			c.JSON(http.StatusOK, gin.H{"message": "Lançamento registrado.", "movement": movement, "replayed": replayed})
		}
	}
}

func internal(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Não foi possível concluir a operação."})
}
