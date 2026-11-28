// Package s3 implements ports.Storage on aws-sdk-go-v2.
package s3

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

const (
	DeleteBatchSize   = 1000
	UploadPartBytes   = 8 * 1024 * 1024
	UploadConcurrency = 2
)

// Adapter builds a client per call, so no connection or credential outlives its operation.
type Adapter struct{}

type countingReader struct {
	reader io.Reader
	total  atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.total.Add(int64(n))
	return n, err
}

func client(storage domain.S3StorageSpec, creds domain.S3Credentials) *awss3.Client {
	region := storage.Region
	if region == "" {
		region = domain.DefaultS3Region
	}
	return awss3.New(awss3.Options{
		Region:                     region,
		Credentials:                credentials.NewStaticCredentialsProvider(creds.AccessKeyID, creds.SecretAccessKey, ""),
		UsePathStyle:               storage.ForcePathStyle,
		BaseEndpoint:               endpoint(storage),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
}

func endpoint(storage domain.S3StorageSpec) *string {
	if storage.Endpoint == "" {
		return nil
	}
	return aws.String(storage.Endpoint)
}

func (Adapter) UploadStream(ctx context.Context, storage domain.S3StorageSpec, creds domain.S3Credentials, key string, body io.Reader) (int64, error) {
	reader := &countingReader{reader: body}
	manager := transfermanager.New(client(storage, creds), func(o *transfermanager.Options) {
		o.PartSizeBytes = UploadPartBytes
		o.MultipartUploadThreshold = UploadPartBytes
		o.Concurrency = UploadConcurrency
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	_, err := manager.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(key),
		Body:   reader,
	})
	if err != nil {
		return 0, fmt.Errorf("upload %s: %w", storage.URI(key), err)
	}
	return reader.total.Load(), nil
}

func (Adapter) PutObject(ctx context.Context, storage domain.S3StorageSpec, creds domain.S3Credentials, key string, data []byte) error {
	_, err := client(storage, creds).PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", storage.URI(key), err)
	}
	return nil
}

func (Adapter) GetObject(ctx context.Context, storage domain.S3StorageSpec, creds domain.S3Credentials, key string) ([]byte, error) {
	out, err := client(storage, creds).GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", storage.URI(key), err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", storage.URI(key), err)
	}
	return data, nil
}

func (Adapter) DeletePrefix(ctx context.Context, storage domain.S3StorageSpec, creds domain.S3Credentials, prefix string) (int, error) {
	s3 := client(storage, creds)
	deleted := 0
	pages := awss3.NewListObjectsV2Paginator(s3, &awss3.ListObjectsV2Input{
		Bucket: aws.String(storage.Bucket),
		Prefix: aws.String(prefix + "/"),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return deleted, fmt.Errorf("list %s: %w", storage.URI(prefix), err)
		}
		identifiers := make([]types.ObjectIdentifier, 0, len(page.Contents))
		for _, item := range page.Contents {
			identifiers = append(identifiers, types.ObjectIdentifier{Key: item.Key})
		}
		for start := 0; start < len(identifiers); start += DeleteBatchSize {
			batch := identifiers[start:min(start+DeleteBatchSize, len(identifiers))]
			out, err := s3.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
				Bucket: aws.String(storage.Bucket),
				Delete: &types.Delete{Objects: batch, Quiet: aws.Bool(true)},
			})
			if err != nil {
				return deleted, fmt.Errorf("delete under %s: %w", storage.URI(prefix), err)
			}
			if len(out.Errors) > 0 {
				first := out.Errors[0]
				return deleted, fmt.Errorf("delete under %s: %d objects failed, first %s: %s", storage.URI(prefix), len(out.Errors), aws.ToString(first.Key), aws.ToString(first.Message))
			}
			deleted += len(batch)
		}
	}
	return deleted, nil
}

func (Adapter) AbortUploads(ctx context.Context, storage domain.S3StorageSpec, creds domain.S3Credentials, prefix string) (int, error) {
	s3 := client(storage, creds)
	aborted := 0
	input := &awss3.ListMultipartUploadsInput{
		Bucket: aws.String(storage.Bucket),
		Prefix: aws.String(prefix + "/"),
	}
	for {
		page, err := s3.ListMultipartUploads(ctx, input)
		if err != nil {
			return aborted, fmt.Errorf("list uploads %s: %w", storage.URI(prefix), err)
		}
		for _, upload := range page.Uploads {
			_, err := s3.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
				Bucket:   aws.String(storage.Bucket),
				Key:      upload.Key,
				UploadId: upload.UploadId,
			})
			if err != nil {
				return aborted, fmt.Errorf("abort upload %s: %w", storage.URI(aws.ToString(upload.Key)), err)
			}
			aborted++
		}
		if !aws.ToBool(page.IsTruncated) {
			return aborted, nil
		}
		input.KeyMarker = page.NextKeyMarker
		input.UploadIdMarker = page.NextUploadIdMarker
	}
}

func (Adapter) PresignedGetURL(ctx context.Context, storage domain.S3StorageSpec, creds domain.S3Credentials, key string, expires time.Duration) (string, error) {
	request, err := awss3.NewPresignClient(client(storage, creds)).PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(key),
	}, awss3.WithPresignExpires(expires))
	if err != nil {
		return "", fmt.Errorf("presign %s: %w", storage.URI(key), err)
	}
	return request.URL, nil
}
