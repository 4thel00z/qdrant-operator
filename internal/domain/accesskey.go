package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

type AccessKeyPhase string

const (
	AccessKeyPending AccessKeyPhase = "Pending"
	AccessKeyReady   AccessKeyPhase = "Ready"
	AccessKeyFailed  AccessKeyPhase = "Failed"
)

const (
	AccessKeyTokenSecretKey = "token"
	AccessKeyURLSecretKey   = "url"
)

var (
	durationPattern = regexp.MustCompile(`^([0-9]+[hms])+$`)
	durationPart    = regexp.MustCompile(`([0-9]+)([hms])`)
	durationUnits   = map[string]time.Duration{"h": time.Hour, "m": time.Minute, "s": time.Second}
)

// ParseDuration reads Go-style durations built from h, m and s such as 720h or 1h30m.
func ParseDuration(value string) (time.Duration, error) {
	if !durationPattern.MatchString(value) {
		return 0, fmt.Errorf("invalid duration %q; use digits with h, m or s", value)
	}
	var total time.Duration
	for _, part := range durationPart.FindAllStringSubmatch(value, -1) {
		count, err := strconv.ParseInt(part[1], 10, 64)
		unit := durationUnits[part[2]]
		if err != nil || count > math.MaxInt64/int64(unit) {
			return 0, fmt.Errorf("duration %q is out of range", value)
		}
		step := time.Duration(count) * unit
		if total > time.Duration(math.MaxInt64)-step {
			return 0, fmt.Errorf("duration %q is out of range", value)
		}
		total += step
	}
	return total, nil
}

func KeyFingerprint(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])[:16]
}

type CollectionAccess struct {
	Name   string
	Access string
}

func (c CollectionAccess) Claim() JSON {
	return JSON{"collection": c.Name, "access": c.Access}
}

// AccessKeySpec treats a zero TTL as a token that never expires and a zero RenewBefore as a third of the TTL.
type AccessKeySpec struct {
	Name         string
	Namespace    string
	ClusterRef   ClusterRef
	SecretName   string
	GlobalAccess string
	Collections  []CollectionAccess
	Subject      string
	TTL          time.Duration
	RenewBefore  time.Duration
	ValueExists  JSON
}

func DefaultAccessKeySpec(name string, namespace string, clusterRef ClusterRef) AccessKeySpec {
	return AccessKeySpec{Name: name, Namespace: namespace, ClusterRef: clusterRef, SecretName: name}
}

func (s AccessKeySpec) Validate() error {
	if (s.GlobalAccess != "") == (len(s.Collections) > 0) {
		return fmt.Errorf("QdrantAccessKey needs exactly one of spec.access or spec.collections")
	}
	return nil
}

func (s AccessKeySpec) AccessClaim() any {
	if s.GlobalAccess != "" {
		return s.GlobalAccess
	}
	claims := make([]JSON, 0, len(s.Collections))
	for _, collection := range s.Collections {
		claims = append(claims, collection.Claim())
	}
	return claims
}

// ExpiresAt is the zero time when the token never expires.
func (s AccessKeySpec) ExpiresAt(issuedAt time.Time) time.Time {
	if s.TTL == 0 {
		return time.Time{}
	}
	return issuedAt.Add(s.TTL)
}

// RenewAt is the zero time when the token never expires.
func (s AccessKeySpec) RenewAt(issuedAt time.Time) time.Time {
	if s.TTL == 0 {
		return time.Time{}
	}
	lead := s.RenewBefore
	if lead == 0 {
		lead = thirdOfDuration(s.TTL)
	}
	return issuedAt.Add(s.TTL - lead)
}

// thirdOfDuration rounds to whole microseconds like Python's timedelta division.
func thirdOfDuration(value time.Duration) time.Duration {
	micros := value.Microseconds()
	third := micros / 3
	if micros%3 == 2 {
		third++
	}
	return time.Duration(third) * time.Microsecond
}

// Claims is the JWT payload in the layout Qdrant's auth parser deserializes.
func (s AccessKeySpec) Claims(issuedAt time.Time) JSON {
	claims := JSON{"access": s.AccessClaim()}
	if s.Subject != "" {
		claims["sub"] = s.Subject
	}
	if expiresAt := s.ExpiresAt(issuedAt); !expiresAt.IsZero() {
		claims["exp"] = expiresAt.Unix()
	}
	if s.ValueExists != nil {
		claims["value_exists"] = s.ValueExists
	}
	return claims
}

func (s AccessKeySpec) TokenSecretRef() SecretRef {
	return SecretRef{Name: s.SecretName, Key: AccessKeyTokenSecretKey, Namespace: s.Namespace}
}

type AccessKeyStatus struct {
	Phase              AccessKeyPhase
	SecretRef          *SecretRef
	IssuedAt           time.Time
	ExpiresAt          time.Time
	RenewAt            time.Time
	KeyFingerprint     string
	Error              string
	ObservedGeneration int64
	Conditions         []Condition
}

// TokenCurrent is true while the issued token still matches the spec, the signing key and its lifetime.
func (s AccessKeyStatus) TokenCurrent(generation int64, fingerprint string, secretPresent bool, now time.Time) bool {
	if s.Phase != AccessKeyReady || !secretPresent {
		return false
	}
	if s.ObservedGeneration != generation || s.KeyFingerprint != fingerprint {
		return false
	}
	return s.RenewAt.IsZero() || now.Before(s.RenewAt)
}
