package storage

import (
	"context"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/qwish/backend/internal/config"
)

type R2Client struct {
	client    *s3.Client
	bucket    string
	publicURL string
}

func NewR2Client(cfg *config.Config) *R2Client {
	r2Endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.R2AccountID)

	client := s3.NewFromConfig(aws.Config{
		Region: "auto",
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.R2AccessKeyID,
			cfg.R2SecretAccessKey,
			"",
		),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(r2Endpoint)
	})

	return &R2Client{
		client:    client,
		bucket:    cfg.R2BucketName,
		publicURL: cfg.R2PublicURL,
	}
}

// Upload streams a file to R2 and returns its public URL.
// prefix is a path prefix like "quiz-images" or "promo-banners".
func (c *R2Client) Upload(ctx context.Context, prefix, contentType string, body io.Reader, size int64) (string, error) {
	ext := extensionFromContentType(contentType)
	key := path.Join(prefix, uuid.New().String()+ext)

	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		return "", fmt.Errorf("R2 upload failed: %w", err)
	}

	return c.publicURL + "/" + key, nil
}

// Delete removes an object from R2 by its key.
func (c *R2Client) Delete(ctx context.Context, key string) error {
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	return err
}

// PresignURL generates a temporary pre-signed GET URL (useful for private buckets).
func (c *R2Client) PresignURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presigner := s3.NewPresignClient(c.client)
	req, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func extensionFromContentType(ct string) string {
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".bin"
	}
}
