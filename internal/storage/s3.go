package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/qwish/backend/internal/config"
)

type S3Client struct {
	client    *s3.Client
	bucket    string
	publicURL string
}

func NewS3Client(ctx context.Context, cfg *config.Config) (*S3Client, error) {
	if strings.TrimSpace(cfg.AWSRegion) == "" || cfg.AWSRegion == "auto" {
		return nil, fmt.Errorf("AWS_REGION must be an AWS region")
	}
	if strings.TrimSpace(cfg.S3BucketName) == "" {
		return nil, fmt.Errorf("S3_BUCKET_NAME is required")
	}
	publicURL := strings.TrimRight(cfg.S3PublicURL, "/")
	if publicURL == "" {
		suffix := "amazonaws.com"
		if strings.HasPrefix(cfg.AWSRegion, "cn-") {
			suffix += ".cn"
		}
		publicURL = fmt.Sprintf("https://%s.s3.%s.%s", cfg.S3BucketName, cfg.AWSRegion, suffix)
		// Dotted bucket names cannot match S3's wildcard TLS certificate.
		if strings.Contains(cfg.S3BucketName, ".") {
			publicURL = fmt.Sprintf("https://s3.%s.%s/%s", cfg.AWSRegion, suffix, cfg.S3BucketName)
		}
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("S3_PUBLIC_URL must be an HTTPS URL without credentials, query, or fragment")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	// Resolve now so missing or incomplete credentials fail at startup rather
	// than on the first upload. The SDK caches and refreshes temporary credentials.
	if _, err := awsCfg.Credentials.Retrieve(ctx); err != nil {
		return nil, fmt.Errorf("load AWS credentials: %w", err)
	}
	return &S3Client{
		client:    s3.NewFromConfig(awsCfg),
		bucket:    cfg.S3BucketName,
		publicURL: publicURL,
	}, nil
}

// Upload streams a file to S3 and returns its public URL.
// prefix is a path prefix like "quiz-images" or "promo-banners".
func (c *S3Client) Upload(ctx context.Context, prefix, contentType string, body io.Reader, size int64) (string, error) {
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
		return "", fmt.Errorf("S3 upload failed: %w", err)
	}

	return c.objectURL(key), nil
}

// Delete removes an object from S3 by its key.
func (c *S3Client) Delete(ctx context.Context, key string) error {
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	return err
}

// PresignURL generates a temporary pre-signed GET URL (useful for private buckets).
func (c *S3Client) PresignURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
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

// PresignUpload generates a temporary pre-signed PUT URL for direct client upload, along with the expected public URL.
func (c *S3Client) PresignUpload(ctx context.Context, prefix, contentType string, ttl time.Duration) (string, string, string, error) {
	ext := extensionFromContentType(contentType)
	key := path.Join(prefix, uuid.New().String()+ext)

	presigner := s3.NewPresignClient(c.client)
	req, err := presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", "", "", err
	}

	publicURL := c.objectURL(key)
	return req.URL, publicURL, key, nil
}

func (c *S3Client) objectURL(key string) string {
	return c.publicURL + (&url.URL{Path: "/" + key}).EscapedPath()
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
