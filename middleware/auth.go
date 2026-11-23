package middleware

import (
	"bank-management/utils"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const CookieName = "bank_session"

func AuthMiddleware(tokens utils.Tokens) gin.HandlerFunc {
	return func(c *gin.Context) {
		var raw string
		if header := c.GetHeader("Authorization"); header != "" {
			raw, _ = strings.CutPrefix(header, "Bearer ")
		} else {
			raw, _ = c.Cookie(CookieName)
		}
		claims, err := tokens.Validate(raw)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Sessão inválida ou expirada."})
			return
		}
		c.Set("userID", claims.UserID)
		c.Next()
	}
}
