package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testConfig() Config {
	return Config{
		Mode:        ModeDev,
		Environment: "development",
		DevSecret:   strings.Repeat("s", 32),
		DevIssuer:   "test-issuer",
		Audience:    "test-audience",
	}
}

func TestDevTokenRoundTrip(t *testing.T) {
	cfg := testConfig()
	issuer, err := NewDevIssuer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewVerifier(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	token, _, err := issuer.Issue("user_01", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := verifier.Verify(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.ID != "user_01" {
		t.Fatalf("principal = %q", principal.ID)
	}
	if !verifier.IsDevelopment() {
		t.Fatal("dev verifier must report development")
	}
}

func TestDevVerifierRejectsWrongAudienceAndExpiry(t *testing.T) {
	cfg := testConfig()
	verifier, err := NewVerifier(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	wrongAudience := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "user_01",
		Issuer:    cfg.DevIssuer,
		Audience:  jwt.ClaimStrings{"other"},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	})
	signed, err := wrongAudience.SignedString([]byte(cfg.DevSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), signed); err == nil {
		t.Fatal("wrong audience must be rejected")
	}

	expired := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject:   "user_01",
		Issuer:    cfg.DevIssuer,
		Audience:  jwt.ClaimStrings{cfg.Audience},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
	})
	signed, err = expired.SignedString([]byte(cfg.DevSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(context.Background(), signed); err == nil {
		t.Fatal("expired token must be rejected")
	}
}

func TestDevModeIsDevelopmentOnly(t *testing.T) {
	cfg := testConfig()
	cfg.Environment = "production"
	if _, err := NewVerifier(context.Background(), cfg); err == nil {
		t.Fatal("dev auth in production must be rejected")
	}
}

func TestRequireRole(t *testing.T) {
	ctx := WithMembership(context.Background(), Membership{OrgID: "org_1", Role: RoleViewer})
	if err := RequireRole(ctx, RoleOperator); err == nil {
		t.Fatal("viewer must not satisfy operator")
	}
	if err := RequireRole(ctx, RoleViewer); err != nil {
		t.Fatalf("viewer should satisfy viewer: %v", err)
	}
	ctx = WithMembership(ctx, Membership{OrgID: "org_1", Role: RoleOwner})
	if err := RequireRole(ctx, RoleOwner); err != nil {
		t.Fatalf("owner should satisfy owner: %v", err)
	}
}
