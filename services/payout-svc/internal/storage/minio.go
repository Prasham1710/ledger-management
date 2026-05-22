package storage

import (
	"bytes"
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinIO struct {
	client *minio.Client
	bucket string
}

func NewMinIO(endpoint, accessKey, secretKey, bucket string) (*MinIO, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		return nil, fmt.Errorf("minio.New: %w", err)
	}
	return &MinIO{client: client, bucket: bucket}, nil
}

// Upload stores data at objectKey and returns the full object path.
func (m *MinIO) Upload(ctx context.Context, objectKey string, data []byte) (string, error) {
	_, err := m.client.PutObject(ctx, m.bucket, objectKey, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		return "", fmt.Errorf("minio upload %s: %w", objectKey, err)
	}
	return objectKey, nil
}
