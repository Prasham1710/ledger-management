// Package storage writes settlement instructions to object storage.
//
// Settlement files contain beneficiary bank details — account numbers, IBANs,
// names. They are written with server-side encryption when a key is configured,
// and the transport can be TLS-secured: the previous implementation hardcoded
// Secure: false with no way to override it, so bank details crossed the network
// in plaintext even where TLS was available.
package storage

import (
	"bytes"
	"context"
	"fmt"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/encrypt"
)

// Config holds object storage settings.
type Config struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	// UseSSL enables TLS to the object store.
	UseSSL bool
	// EncryptionKey, when set, enables SSE-C so settlement files are encrypted
	// at rest with a key the platform controls. Must be exactly 32 bytes.
	EncryptionKey string
}

type MinIO struct {
	client *minio.Client
	bucket string
	sse    encrypt.ServerSide
}

// NewMinIO builds a client. An EncryptionKey of the wrong length is an error
// rather than a silent downgrade to unencrypted storage.
func NewMinIO(cfg Config) (*MinIO, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio.New: %w", err)
	}

	m := &MinIO{client: client, bucket: cfg.Bucket}

	if cfg.EncryptionKey != "" {
		const requiredLen = 32
		if len(cfg.EncryptionKey) != requiredLen {
			return nil, fmt.Errorf(
				"settlement file encryption key must be exactly %d bytes, got %d",
				requiredLen, len(cfg.EncryptionKey))
		}
		// SSE-C requires TLS: the key travels in request headers, so sending it
		// over plaintext HTTP would expose it.
		if !cfg.UseSSL {
			return nil, fmt.Errorf(
				"settlement file encryption requires MINIO_USE_SSL=true (SSE-C sends the key in request headers)")
		}
		sse, err := encrypt.NewSSEC([]byte(cfg.EncryptionKey))
		if err != nil {
			return nil, fmt.Errorf("build SSE-C encryption: %w", err)
		}
		m.sse = sse
	}

	return m, nil
}

// EnsureBucket creates the target bucket if it does not exist, so a fresh
// environment does not fail its first payout.
func (m *MinIO) EnsureBucket(ctx context.Context) error {
	exists, err := m.client.BucketExists(ctx, m.bucket)
	if err != nil {
		return fmt.Errorf("check bucket %s: %w", m.bucket, err)
	}
	if exists {
		return nil
	}
	if err := m.client.MakeBucket(ctx, m.bucket, minio.MakeBucketOptions{}); err != nil {
		// A concurrent replica may have created it between the two calls.
		if exists, checkErr := m.client.BucketExists(ctx, m.bucket); checkErr == nil && exists {
			return nil
		}
		return fmt.Errorf("create bucket %s: %w", m.bucket, err)
	}
	return nil
}

// Upload stores data at objectKey and returns the object path.
func (m *MinIO) Upload(ctx context.Context, objectKey string, data []byte) (string, error) {
	opts := minio.PutObjectOptions{ContentType: "application/json"}
	if m.sse != nil {
		opts.ServerSideEncryption = m.sse
	}

	if _, err := m.client.PutObject(ctx, m.bucket, objectKey,
		bytes.NewReader(data), int64(len(data)), opts); err != nil {
		return "", fmt.Errorf("minio upload %s: %w", objectKey, err)
	}
	return objectKey, nil
}

// Ping verifies the object store is reachable, for readiness checks.
func (m *MinIO) Ping(ctx context.Context) error {
	if _, err := m.client.BucketExists(ctx, m.bucket); err != nil {
		return fmt.Errorf("minio unreachable: %w", err)
	}
	return nil
}
