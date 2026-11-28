//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type bucket struct {
	client *minio.Client
}

func openBucket(t *testing.T) bucket {
	t.Helper()
	address := portForward(t, s3Service, s3LocalPort, s3Port)
	s3, err := minio.New(address, &minio.Options{
		Creds:  credentials.NewStaticV4(s3AccessKey, s3SecretKey, ""),
		Secure: false,
	})
	if err != nil {
		t.Fatalf("create s3 client: %v", err)
	}
	return bucket{client: s3}
}

func (b bucket) create(t *testing.T, name string) {
	t.Helper()
	if err := b.client.MakeBucket(context.Background(), name, minio.MakeBucketOptions{}); err != nil {
		t.Fatalf("create bucket %s: %v", name, err)
	}
}

func (b bucket) manifest(t *testing.T, name, key string) (names []string, nodeCount int) {
	t.Helper()
	object, err := b.client.GetObject(context.Background(), name, key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer object.Close()
	payload, err := io.ReadAll(object)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	var manifest struct {
		Collections []struct {
			Name string `json:"name"`
		} `json:"collections"`
		NodeCount int `json:"nodeCount"`
	}
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatalf("decode %s: %v", key, err)
	}
	for _, collection := range manifest.Collections {
		names = append(names, collection.Name)
	}
	return names, manifest.NodeCount
}

func (b bucket) countObjects(t *testing.T, name, prefix string) int {
	t.Helper()
	count := 0
	for entry := range b.client.ListObjects(context.Background(), name, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if entry.Err != nil {
			t.Fatalf("list %s: %v", prefix, entry.Err)
		}
		count++
	}
	return count
}
