package oauth

import (
	"testing"
	"time"

	"OurAgent/internal/config"

	"github.com/golang-jwt/jwt/v5"
)

func TestNotionStateRejectsInvalidSignature(t *testing.T) {
	service := NewNotionService(config.NotionOAuthConfig{}, "correct-secret", nil)
	other := NewNotionService(config.NotionOAuthConfig{}, "wrong-secret", nil)
	state, err := other.signState(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.verifyState(state); err == nil {
		t.Fatal("invalid signature was accepted")
	}
}

func TestNotionStateRejectsExpiredToken(t *testing.T) {
	service := NewNotionService(config.NotionOAuthConfig{}, "secret", nil)
	now := time.Now().Add(-time.Hour)
	claims := notionStateClaims{
		SourceID: 1,
		UserID:   2,
		Nonce:    "expired",
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		},
	}
	state, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.verifyState(state); err == nil {
		t.Fatal("expired state was accepted")
	}
}

func TestNotionStateKeepsSourceAndUserBinding(t *testing.T) {
	service := NewNotionService(config.NotionOAuthConfig{}, "secret", nil)
	state, err := service.signState(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := service.verifyState(state)
	if err != nil {
		t.Fatal(err)
	}
	if claims.SourceID != 10 || claims.UserID != 20 {
		t.Fatalf("unexpected claims: source=%d user=%d", claims.SourceID, claims.UserID)
	}
}
