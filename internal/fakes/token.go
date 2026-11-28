package fakes

import (
	"encoding/json"
	"fmt"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

// Token returns a deterministic unsigned string and records every signing call.
type Token struct {
	Signed []SignedToken
}

type SignedToken struct {
	Claims domain.JSON
	Secret string
}

func (t *Token) Sign(claims domain.JSON, secret string) (string, error) {
	encoded, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode claims: %w", err)
	}
	t.Signed = append(t.Signed, SignedToken{Claims: claims, Secret: secret})
	return fmt.Sprintf("fake.%s.%s", encoded, secret), nil
}
