package utils

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

const SessionLifetime = 8 * time.Hour
const issuer = "bank-management"

type Tokens struct {
	key []byte
}

type Claims struct {
	UserID uint `json:"user_id"`
	jwt.RegisteredClaims
}

func NewTokens(secret string) (Tokens, error) {
	if len(secret) < 32 {
		return Tokens{}, errors.New("JWT_SECRET precisa ter pelo menos 32 bytes")
	}
	return Tokens{key: []byte(secret)}, nil
}

func (t Tokens) Generate(userID uint) (string, error) {
	now := time.Now()
	claims := Claims{UserID: userID, RegisteredClaims: jwt.RegisteredClaims{
		Issuer: issuer, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(SessionLifetime)),
	}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.key)
}

func (t Tokens) Validate(raw string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(_ *jwt.Token) (interface{}, error) {
		return t.key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || !token.Valid || claims.UserID == 0 || !claims.VerifyIssuer(issuer, true) ||
		!claims.VerifyExpiresAt(time.Now(), true) {
		return nil, errors.New("sessão inválida ou expirada")
	}
	return claims, nil
}
