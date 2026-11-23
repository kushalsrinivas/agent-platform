package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/agent-platform/agent-platform/services/api/internal/respond"
)

// DevIssuer mints development bearer tokens. It is mounted only in development mode.
type DevIssuer struct {
	secret   []byte
	issuer   string
	audience string
}

func NewDevIssuer(cfg Config) (*DevIssuer, error) {
	if cfg.Environment != "development" {
		return nil, errors.New("auth: dev issuer requires ENVIRONMENT=development")
	}
	if len(cfg.DevSecret) < 16 {
		return nil, errors.New("auth: AUTH_DEV_SECRET must be at least 16 characters")
	}
	issuer := cfg.DevIssuer
	if issuer == "" {
		issuer = "agent-platform-dev"
	}
	audience := cfg.Audience
	if audience == "" {
		audience = "agent-platform-api"
	}
	return &DevIssuer{secret: []byte(cfg.DevSecret), issuer: issuer, audience: audience}, nil
}

func (i *DevIssuer) Issue(principalID string, ttl time.Duration) (string, time.Time, error) {
	if strings.TrimSpace(principalID) == "" || len(principalID) > 255 {
		return "", time.Time{}, errors.New("principal_id must be 1..255 characters")
	}
	if ttl < time.Minute || ttl > time.Hour {
		return "", time.Time{}, errors.New("ttl_seconds must be between 60 and 3600")
	}
	now := time.Now().UTC()
	expires := now.Add(ttl)
	claims := jwt.RegisteredClaims{
		Subject:   principalID,
		Issuer:    i.issuer,
		Audience:  jwt.ClaimStrings{i.audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return signed, expires, nil
}

type devTokenRequest struct {
	PrincipalID string `json:"principal_id"`
	TTLSeconds  int    `json:"ttl_seconds,omitempty"`
}

type devTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// Handler is POST /v1/dev/token. It exists only when the API runs in development mode.
func (i *DevIssuer) Handler(w http.ResponseWriter, r *http.Request) {
	var request devTokenRequest
	if err := respond.Decode(w, r, 16<<10, &request); err != nil {
		respond.Error(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ttl := time.Hour
	if request.TTLSeconds != 0 {
		ttl = time.Duration(request.TTLSeconds) * time.Second
	}
	token, expires, err := i.Issue(request.PrincipalID, ttl)
	if err != nil {
		respond.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}
	respond.JSON(w, http.StatusOK, devTokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int(time.Until(expires).Seconds()),
	})
}
