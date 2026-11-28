package jwt_test

import (
	"reflect"
	"testing"

	golangjwt "github.com/golang-jwt/jwt/v5"

	"github.com/4thel00z/qdrant-operator/internal/adapters/jwt"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

var _ ports.Token = jwt.Adapter{}

const secret = "0123456789abcdef0123456789abcdef"

// pyjwtToken is PyJWT 2.x output for pyjwtClaims signed with secret.
const pyjwtToken = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJhY2Nlc3MiOlt7ImNvbGxlY3Rpb24iOiJkb2NzIiwiYWNjZXNzIjoiciJ9XSwic3ViIjoiYWxpY2UiLCJleHAiOjE4OTM0NTYwMDB9.R_j_OSt-UazByYiecNPuscdOcNKN9nP_EkIbGac66sc"

func pyjwtClaims() map[string]any {
	return map[string]any{
		"access": []any{map[string]any{"collection": "docs", "access": "r"}},
		"sub":    "alice",
		"exp":    float64(1893456000),
	}
}

func decode(t *testing.T, token string, key string) map[string]any {
	t.Helper()
	claims := golangjwt.MapClaims{}
	parsed, err := golangjwt.ParseWithClaims(token, claims, func(*golangjwt.Token) (any, error) { return []byte(key), nil },
		golangjwt.WithValidMethods([]string{"HS256"}), golangjwt.WithoutClaimsValidation())
	if err != nil || !parsed.Valid {
		t.Fatalf("token does not verify: %v", err)
	}
	return claims
}

func TestSignedTokenVerifiesAndCarriesClaims(t *testing.T) {
	claims := domain.JSON{
		"access": []domain.JSON{{"collection": "docs", "access": "r"}},
		"sub":    "alice",
		"exp":    int64(1893456000),
	}
	token, err := jwt.Adapter{}.Sign(claims, secret)
	if err != nil {
		t.Fatal(err)
	}
	if got := decode(t, token, secret); !reflect.DeepEqual(got, pyjwtClaims()) {
		t.Fatalf("claims %v, want %v", got, pyjwtClaims())
	}
}

func TestWrongSecretDoesNotVerify(t *testing.T) {
	token, err := jwt.Adapter{}.Sign(domain.JSON{"sub": "alice"}, secret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = golangjwt.Parse(token, func(*golangjwt.Token) (any, error) { return []byte("other"), nil })
	if err == nil {
		t.Fatal("expected a signature error")
	}
}

func TestPyJWTTokenDecodesToTheSameClaims(t *testing.T) {
	if got := decode(t, pyjwtToken, secret); !reflect.DeepEqual(got, pyjwtClaims()) {
		t.Fatalf("claims %v, want %v", got, pyjwtClaims())
	}
}
