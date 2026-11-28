// Package fakes holds in-memory implementations of the ports for use-case tests.
package fakes

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

type ObjectID struct {
	Bucket string
	Key    string
}

// Storage keeps objects and pending multipart uploads in memory.
type Storage struct {
	mu      sync.Mutex
	Objects map[ObjectID][]byte
	Uploads map[ObjectID]bool
}

func NewStorage() *Storage {
	return &Storage{Objects: map[ObjectID][]byte{}, Uploads: map[ObjectID]bool{}}
}

func (s *Storage) UploadStream(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, key string, body io.Reader) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("read upload body: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Objects[ObjectID{storage.Bucket, key}] = data
	return int64(len(data)), nil
}

func (s *Storage) PutObject(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Objects[ObjectID{storage.Bucket, key}] = slices.Clone(data)
	return nil
}

func (s *Storage) GetObject(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.Objects[ObjectID{storage.Bucket, key}]
	if !ok {
		return nil, fmt.Errorf("no such key %s", storage.URI(key))
	}
	return slices.Clone(data), nil
}

func (s *Storage) DeletePrefix(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, prefix string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for id := range s.Objects {
		if id.Bucket != storage.Bucket || !strings.HasPrefix(id.Key, prefix+"/") {
			continue
		}
		delete(s.Objects, id)
		deleted++
	}
	return deleted, nil
}

func (s *Storage) AbortUploads(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, prefix string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	aborted := 0
	for id := range s.Uploads {
		if id.Bucket != storage.Bucket || !strings.HasPrefix(id.Key, prefix+"/") {
			continue
		}
		delete(s.Uploads, id)
		aborted++
	}
	return aborted, nil
}

func (s *Storage) PresignedGetURL(ctx context.Context, storage domain.S3StorageSpec, credentials domain.S3Credentials, key string, expires time.Duration) (string, error) {
	return fmt.Sprintf("https://presigned.test/%s/%s?expires=%d", storage.Bucket, key, int64(expires.Seconds())), nil
}

func (s *Storage) Keys(bucket string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := []string{}
	for id := range s.Objects {
		if id.Bucket == bucket {
			keys = append(keys, id.Key)
		}
	}
	slices.Sort(keys)
	return keys
}
