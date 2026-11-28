//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

const (
	namespace         = "qdrant-e2e"
	operatorNamespace = "qdrant-system"
	operatorSelector  = "app.kubernetes.io/name=qdrant-operator"
	clusterName       = "store"
	qdrantVersion     = "v1.16.3"
	collectionName    = "docs"
	restoredName      = "docs-restored"
	accessKeyName     = "reader"
	backupName        = "first"
	restoreName       = "docs-copy"
	apiKeySecret      = "store-api-key"
	apiKey            = "e2e-api-key-0123456789abcdef0123456789"
	s3Service         = "s3"
	s3Image           = "rustfs/rustfs:1.0.4"
	s3Port            = 9000
	s3Secret          = "s3-credentials"
	s3AccessKey       = "e2e-access-key"
	s3SecretKey       = "e2e-secret-key-0123456789"
	bucketName        = "qdrant-backups"
	bucketPrefix      = "e2e"
	manifestKey       = "manifest.json"
	tokenSecretKey    = "token"
	qdrantLocalPort   = 16343
	s3LocalPort       = 19000
	pointCount        = 64
	failedPhase       = "Failed"
	timeout           = 300 * time.Second
	pollInterval      = 3 * time.Second
	forwardTimeout    = 30 * time.Second
)

var s3Endpoint = fmt.Sprintf("http://%s.%s.svc:%d", s3Service, namespace, s3Port)

var deleteOrder = []resourceKind{restores, accessKeys, collections, backups, clusters}

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	if err := setup(); err != nil {
		return 0, err
	}
	code := m.Run()
	if code != 0 {
		return code, nil
	}
	teardown()
	return code, nil
}

func setup() error {
	if err := initClient(); err != nil {
		return err
	}
	if _, err := kubectlRun("create", "namespace", namespace); err != nil {
		return err
	}
	objects := []object{
		secretObject(apiKeySecret, map[string]string{"api-key": apiKey}),
		secretObject(s3Secret, map[string]string{
			"AWS_ACCESS_KEY_ID":     s3AccessKey,
			"AWS_SECRET_ACCESS_KEY": s3SecretKey,
		}),
	}
	objects = append(objects, s3Server()...)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := createAll(ctx, objects); err != nil {
		return err
	}
	_, err := kubectlRun("rollout", "status", "-n", namespace, "deployment/"+s3Service, "--timeout=300s")
	return err
}

func teardown() {
	for _, kind := range deleteOrder {
		exec.Command("kubectl", "delete", kind.plural, "--all", "-n", namespace, "--timeout=120s").Run()
	}
	exec.Command("kubectl", "delete", "namespace", namespace, "--timeout=180s").Run()
}
