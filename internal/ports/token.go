package ports

import "github.com/4thel00z/qdrant-operator/internal/domain"

// Token encodes claims as an HS256 JWT signed with the cluster's API key.
type Token interface {
	Sign(claims domain.JSON, secret string) (string, error)
}
