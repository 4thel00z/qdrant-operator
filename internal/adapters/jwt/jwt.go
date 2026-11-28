// Package jwt signs Qdrant RBAC tokens (HS256, the algorithm Qdrant's parser accepts).
package jwt

import (
	"fmt"

	golangjwt "github.com/golang-jwt/jwt/v5"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

type Adapter struct{}

func (Adapter) Sign(claims domain.JSON, secret string) (string, error) {
	token, err := golangjwt.NewWithClaims(golangjwt.SigningMethodHS256, golangjwt.MapClaims(claims)).SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("sign HS256 token: %w", err)
	}
	return token, nil
}
