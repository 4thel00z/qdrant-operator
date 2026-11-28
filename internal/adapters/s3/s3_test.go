package s3_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/4thel00z/qdrant-operator/internal/adapters/s3"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

var _ ports.Storage = s3.Adapter{}

const bucket = "snapshots"

var creds = domain.S3Credentials{AccessKeyID: "key", SecretAccessKey: "secret"}

func server(t *testing.T) (domain.S3StorageSpec, *s3mem.Backend) {
	t.Helper()
	backend := s3mem.New()
	if err := backend.CreateBucket(bucket); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gofakes3.New(backend).Server())
	t.Cleanup(srv.Close)
	return domain.S3StorageSpec{Bucket: bucket, Endpoint: srv.URL, ForcePathStyle: true, Region: domain.DefaultS3Region}, backend
}

func objectBody(t *testing.T, backend *s3mem.Backend, key string) []byte {
	t.Helper()
	object, err := backend.GetObject(bucket, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Contents.Close()
	data, err := io.ReadAll(object.Contents)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type pattern struct {
	remaining int64
	offset    int64
	maxRead   int64
}

func (p *pattern) Read(buf []byte) (int, error) {
	if p.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(buf)), p.remaining, p.maxRead)
	for i := range n {
		buf[i] = byte((p.offset + i) % 251)
	}
	p.offset += n
	p.remaining -= n
	return int(n), nil
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("source broke") }

func TestUploadStreamLargerThanOnePart(t *testing.T) {
	storage, backend := server(t)
	size := int64(3*s3.UploadPartBytes + 12345)
	written, err := s3.Adapter{}.UploadStream(context.Background(), storage, creds, "demo/snap.tar", &pattern{remaining: size, maxRead: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if written != size {
		t.Fatalf("written %d, want %d", written, size)
	}
	want, err := io.ReadAll(&pattern{remaining: size, maxRead: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(objectBody(t, backend, "demo/snap.tar"), want) {
		t.Fatal("stored object differs from the streamed bytes")
	}
}

func TestUploadStreamSmall(t *testing.T) {
	storage, backend := server(t)
	written, err := s3.Adapter{}.UploadStream(context.Background(), storage, creds, "demo/small", strings.NewReader("hello"))
	if err != nil || written != 5 {
		t.Fatalf("written %d err %v", written, err)
	}
	if got := objectBody(t, backend, "demo/small"); string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestUploadStreamReadFailureIsReported(t *testing.T) {
	storage, _ := server(t)
	reader := io.MultiReader(bytes.NewReader(make([]byte, 100)), failing{})
	if _, err := (s3.Adapter{}).UploadStream(context.Background(), storage, creds, "demo/broken", reader); err == nil {
		t.Fatal("expected an error")
	}
}

func TestPutAndGet(t *testing.T) {
	storage, _ := server(t)
	adapter := s3.Adapter{}
	if err := adapter.PutObject(context.Background(), storage, creds, "demo/manifest.json", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	got, err := adapter.GetObject(context.Background(), storage, creds, "demo/manifest.json")
	if err != nil || string(got) != `{"a":1}` {
		t.Fatalf("got %q err %v", got, err)
	}
	if _, err := adapter.GetObject(context.Background(), storage, creds, "demo/missing"); err == nil {
		t.Fatal("expected an error for a missing key")
	}
}

func TestDeletePrefixPaginates(t *testing.T) {
	storage, backend := server(t)
	for i := range 2500 {
		if _, err := backend.PutObject(bucket, fmt.Sprintf("demo/obj-%05d", i), nil, strings.NewReader("x"), 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"demo-other/keep", "other/keep"} {
		if _, err := backend.PutObject(bucket, key, nil, strings.NewReader("x"), 1, nil); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := s3.Adapter{}.DeletePrefix(context.Background(), storage, creds, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2500 {
		t.Fatalf("deleted %d, want 2500", deleted)
	}
	remaining, err := backend.ListBucket(bucket, &gofakes3.Prefix{}, gofakes3.ListBucketPage{})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Contents) != 2 {
		t.Fatalf("%d objects remain, want the 2 outside the prefix", len(remaining.Contents))
	}
}

func TestAbortUploads(t *testing.T) {
	storage, _ := server(t)
	sdk := awss3.New(awss3.Options{
		Region:       storage.Region,
		BaseEndpoint: aws.String(storage.Endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("key", "secret", ""),
	})
	for _, key := range []string{"demo/a", "demo/b", "other/c"} {
		if _, err := sdk.CreateMultipartUpload(context.Background(), &awss3.CreateMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
			t.Fatal(err)
		}
	}
	adapter := s3.Adapter{}
	aborted, err := adapter.AbortUploads(context.Background(), storage, creds, "demo")
	if err != nil || aborted != 2 {
		t.Fatalf("aborted %d err %v, want 2", aborted, err)
	}
	again, err := adapter.AbortUploads(context.Background(), storage, creds, "demo")
	if err != nil || again != 0 {
		t.Fatalf("second pass aborted %d err %v", again, err)
	}
	left, err := adapter.AbortUploads(context.Background(), storage, creds, "other")
	if err != nil || left != 1 {
		t.Fatalf("other prefix aborted %d err %v", left, err)
	}
}

func TestPresignedGetURLFetchesWithPlainHTTP(t *testing.T) {
	storage, _ := server(t)
	adapter := s3.Adapter{}
	if err := adapter.PutObject(context.Background(), storage, creds, "demo/file", []byte("payload")); err != nil {
		t.Fatal(err)
	}
	url, err := adapter.PresignedGetURL(context.Background(), storage, creds, "demo/file", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "payload" {
		t.Fatalf("status %d body %q err %v", response.StatusCode, body, err)
	}
}

func TestPresignedURLCarriesExpiry(t *testing.T) {
	storage, _ := server(t)
	url, err := s3.Adapter{}.PresignedGetURL(context.Background(), storage, creds, "demo/file", 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "X-Amz-Expires=90") {
		t.Fatalf("url %q lacks the expiry", url)
	}
}
