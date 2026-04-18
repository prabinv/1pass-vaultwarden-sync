package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type contextKey string

const userIDKey contextKey = "user_id"

// IssueJWT creates a signed JWT containing userID, valid for 24 hours.
func IssueJWT(secret string, userID uuid.UUID) (string, error) {
	return IssueJWTWithExpiry(secret, userID, int64((24 * time.Hour).Seconds()))
}

// IssueJWTWithExpiry creates a JWT with a custom expiry offset in seconds.
// Negative values produce an already-expired token (useful in tests).
func IssueJWTWithExpiry(secret string, userID uuid.UUID, expiryOffsetSecs int64) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID.String(),
		"exp":     time.Now().Unix() + expiryOffsetSecs,
	})
	return token.SignedString([]byte(secret))
}

// SetUserIDInContext injects userID into ctx.
func SetUserIDInContext(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext retrieves the authenticated user ID from ctx. Returns uuid.Nil if not set.
func UserIDFromContext(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(userIDKey).(uuid.UUID)
	return id
}

// Auth validates the JWT cookie and injects user_id into context.
// Unauthenticated or invalid requests redirect to /auth/login.
func Auth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("token")
			if err != nil {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			token, err := jwt.Parse(cookie.Value, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return []byte(jwtSecret), nil
			})
			if err != nil || !token.Valid {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			idStr, _ := claims["user_id"].(string)
			userID, err := uuid.Parse(idStr)
			if err != nil {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			ctx := SetUserIDInContext(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
