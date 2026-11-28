package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	golangjwt "github.com/golang-jwt/jwt/v5"

	"github.com/4thel00z/qdrant-operator/internal/adapters/jwt"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

func accessKeyDecode(t *testing.T, token string, key string) map[string]any {
	t.Helper()
	claims := golangjwt.MapClaims{}
	parsed, err := golangjwt.ParseWithClaims(token, claims, func(*golangjwt.Token) (any, error) { return []byte(key), nil },
		golangjwt.WithValidMethods([]string{"HS256"}), golangjwt.WithoutClaimsValidation())
	if err != nil || !parsed.Valid {
		t.Fatalf("token does not verify against %q: %v", key, err)
	}
	return claims
}

func accessKeyUseCase(f *collectionFixture) usecases.IssueAccessKey {
	return usecases.IssueAccessKey{Kubernetes: f.kubernetes, Token: jwt.Adapter{}}
}

func accessKeyRBACCluster(f *collectionFixture) {
	keyed := collectionKeyed()
	keyed["apiKey"].(domain.JSON)["jwtRbac"] = true
	f.kubernetes.PutResource(collectionClusterBody("db", 2, keyed))
}

func accessKeyPending() domain.AccessKeyStatus {
	return domain.AccessKeyStatus{Phase: domain.AccessKeyPending}
}

func TestIssueAccessKeyWritesSignedTokenAndRenewsOnTime(t *testing.T) {
	f := newCollectionFixture()
	accessKeyRBACCluster(f)
	spec := accessKeySpec(t, domain.JSON{
		"collections": []any{domain.JSON{"name": "docs", "access": "rw"}},
		"ttl":         "3h",
	})
	use := accessKeyUseCase(f)
	ctx := context.Background()
	now := collectionFixtureNow
	renewedAt := now.Add(2*time.Hour + time.Minute)

	issued, err := use.Execute(ctx, spec, 1, accessKeyPending(), accessKeyOwner(), now)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := use.Execute(ctx, spec, 1, issued, accessKeyOwner(), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := use.Execute(ctx, spec, 1, issued, accessKeyOwner(), renewedAt)
	if err != nil {
		t.Fatal(err)
	}

	secret := f.kubernetes.Secrets[collectionFixtureNS+"/app-token"]
	claims := accessKeyDecode(t, secret["token"], "topsecret")
	wantAccess := []any{map[string]any{"collection": "docs", "access": "rw"}}
	if !reflect.DeepEqual(claims["access"], wantAccess) || claims["exp"] != float64(renewedAt.Add(3*time.Hour).Unix()) || len(claims) != 2 {
		t.Errorf("claims: %v", claims)
	}
	if secret["url"] != collectionServiceURL("db") {
		t.Errorf("url: %s", secret["url"])
	}
	if f.kubernetes.SecretOwners[collectionFixtureNS+"/app-token"].UID != "uid-app-token" {
		t.Errorf("owner: %+v", f.kubernetes.SecretOwners)
	}
	if issued.Phase != domain.AccessKeyReady || !issued.ExpiresAt.Equal(now.Add(3*time.Hour)) || !issued.RenewAt.Equal(now.Add(2*time.Hour)) {
		t.Errorf("issued: %+v", issued)
	}
	if !reflect.DeepEqual(unchanged, issued) {
		t.Errorf("unchanged: %+v", unchanged)
	}
	if !renewed.IssuedAt.Equal(renewedAt) {
		t.Errorf("renewed at %v", renewed.IssuedAt)
	}
	if !renewed.Conditions[0].LastTransitionTime.Equal(issued.Conditions[0].LastTransitionTime) {
		t.Errorf("transition time moved: %v", renewed.Conditions[0].LastTransitionTime)
	}
}

func TestIssueAccessKeyResignsWhenClusterKeyRotatesOrSecretVanishes(t *testing.T) {
	f := newCollectionFixture()
	accessKeyRBACCluster(f)
	spec := accessKeySpec(t, domain.JSON{"secretName": "shared-token"})
	use := accessKeyUseCase(f)
	ctx := context.Background()
	now := collectionFixtureNow

	first, err := use.Execute(ctx, spec, 1, accessKeyPending(), accessKeyOwner(), now)
	if err != nil {
		t.Fatal(err)
	}
	f.kubernetes.Secrets[collectionFixtureNS+"/api-keys"] = map[string]string{"key": "rotated"}
	second, err := use.Execute(ctx, spec, 1, first, accessKeyOwner(), now)
	if err != nil {
		t.Fatal(err)
	}
	delete(f.kubernetes.Secrets, collectionFixtureNS+"/shared-token")
	third, err := use.Execute(ctx, spec, 1, second, accessKeyOwner(), now)
	if err != nil {
		t.Fatal(err)
	}

	if first.KeyFingerprint == second.KeyFingerprint {
		t.Errorf("fingerprint did not change: %s", first.KeyFingerprint)
	}
	claims := accessKeyDecode(t, f.kubernetes.Secrets[collectionFixtureNS+"/shared-token"]["token"], "rotated")
	if !reflect.DeepEqual(map[string]any(claims), map[string]any{"access": "r"}) {
		t.Errorf("claims: %v", claims)
	}
	if third.SecretRef == nil || third.SecretRef.Name != "shared-token" {
		t.Errorf("secret ref: %+v", third.SecretRef)
	}
	if !first.ExpiresAt.IsZero() {
		t.Errorf("expires at: %v", first.ExpiresAt)
	}
}

func TestIssueAccessKeyWaitsUntilTheClusterEnablesJWTRBAC(t *testing.T) {
	f := newCollectionFixture()
	spec := accessKeySpec(t, nil)
	use := accessKeyUseCase(f)
	ctx := context.Background()
	now := collectionFixtureNow

	blocked, err := use.Execute(ctx, spec, 1, accessKeyPending(), accessKeyOwner(), now)
	if err != nil {
		t.Fatal(err)
	}
	f.kubernetes.PutResource(collectionClusterBody("db", 2, domain.JSON{"apiKey": domain.JSON{"jwtRbac": true, "autoGenerate": true}}))
	_, missingKey := use.Execute(ctx, spec, 1, blocked, accessKeyOwner(), now)
	ghost := spec
	ghost.ClusterRef = domain.ClusterRef{Name: "ghost", Namespace: collectionFixtureNS}
	_, missingCluster := use.Execute(ctx, ghost, 1, blocked, accessKeyOwner(), now)

	if blocked.Phase != domain.AccessKeyPending || blocked.Conditions[0].Reason != "JwtRbacDisabled" {
		t.Errorf("blocked: %+v", blocked)
	}
	if !errors.Is(missingKey, ports.ErrSecretKeyNotFound) || !usecases.Retryable(missingKey) {
		t.Errorf("want retryable missing key, got %v", missingKey)
	}
	if !errors.Is(missingCluster, usecases.ErrClusterNotFound) {
		t.Errorf("want ErrClusterNotFound, got %v", missingCluster)
	}
	if _, written := f.kubernetes.Secrets[collectionFixtureNS+"/app-token"]; written {
		t.Error("secret written while blocked")
	}
}

func TestIssueAccessKeyRefusesToOverwriteAForeignSecret(t *testing.T) {
	f := newCollectionFixture()
	accessKeyRBACCluster(f)
	f.kubernetes.Secrets[collectionFixtureNS+"/app-token"] = map[string]string{"password": "keep-me"}

	status, err := accessKeyUseCase(f).Execute(context.Background(), accessKeySpec(t, nil), 1, accessKeyPending(), accessKeyOwner(), collectionFixtureNow)
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.AccessKeyPending || status.Conditions[0].Reason != "SecretNotOwned" {
		t.Errorf("status: %+v", status)
	}
	if !reflect.DeepEqual(f.kubernetes.Secrets[collectionFixtureNS+"/app-token"], map[string]string{"password": "keep-me"}) {
		t.Errorf("secret overwritten: %v", f.kubernetes.Secrets[collectionFixtureNS+"/app-token"])
	}
}
