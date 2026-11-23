package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
)

const (
	ModeDev  = "dev"
	ModeOIDC = "oidc"
)

// Config selects the verifier. Production must use OIDC.
type Config struct {
	Mode          string
	Environment   string
	DevSecret     string
	DevIssuer     string
	Audience      string
	OIDCIssuerURL string
	OIDCClientID  string
}

// Verifier validates a bearer token and returns its subject.
type Verifier interface {
	Verify(ctx context.Context, token string) (Principal, error)
	IsDevelopment() bool
}

type devVerifier struct {
	secret   []byte
	issuer   string
	audience string
}

func (v *devVerifier) IsDevelopment() bool { return true }

func (v *devVerifier) Verify(_ context.Context, raw string) (Principal, error) {
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unsupported signing method %s", token.Method.Alg())
		}
		return v.secret, nil
	}, jwt.WithIssuer(v.issuer), jwt.WithAudience(v.audience), jwt.WithExpirationRequired(), jwt.WithLeeway(5*time.Second))
	if err != nil || !token.Valid {
		return Principal{}, ErrUnauthenticated
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{ID: claims.Subject}, nil
}

type oidcVerifier struct {
	verifier *oidc.IDTokenVerifier
}

func (v *oidcVerifier) IsDevelopment() bool { return false }

func (v *oidcVerifier) Verify(ctx context.Context, raw string) (Principal, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	if strings.TrimSpace(token.Subject) == "" {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{ID: token.Subject}, nil
}

// NewVerifier builds the configured verifier. In development, dev mode is the default only when a dev secret is
// configured; otherwise OIDC is required. Production without OIDC configuration is a startup error.
func NewVerifier(ctx context.Context, cfg Config) (Verifier, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		if cfg.Environment == "development" && cfg.DevSecret != "" {
			mode = ModeDev
		} else {
			mode = ModeOIDC
		}
	}

	switch mode {
	case ModeDev:
		if cfg.Environment != "development" {
			return nil, errors.New("auth: dev mode is only allowed when ENVIRONMENT=development")
		}
		if len(cfg.DevSecret) < 16 {
			return nil, errors.New("auth: AUTH_DEV_SECRET must be at least 16 characters in dev mode")
		}
		issuer := cfg.DevIssuer
		if issuer == "" {
			issuer = "agent-platform-dev"
		}
		audience := cfg.Audience
		if audience == "" {
			audience = "agent-platform-api"
		}
		return &devVerifier{secret: []byte(cfg.DevSecret), issuer: issuer, audience: audience}, nil
	case ModeOIDC:
		if cfg.OIDCIssuerURL == "" || cfg.OIDCClientID == "" {
			return nil, errors.New("auth: OIDC_ISSUER_URL and OIDC_CLIENT_ID are required in oidc mode")
		}
		provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuerURL)
		if err != nil {
			return nil, fmt.Errorf("auth: discover OIDC provider: %w", err)
		}
		return &oidcVerifier{verifier: provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})}, nil
	default:
		return nil, fmt.Errorf("auth: unknown AUTH_MODE %q", mode)
	}
}
