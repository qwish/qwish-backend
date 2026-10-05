package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/qwish/backend/internal/config"
)

func configureTestCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "test-access-key")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_SESSION_TOKEN", "test-session-token")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
}

func TestPresignUploadUsesS3RegionAndTemporaryCredentials(t *testing.T) {
	configureTestCredentials(t)
	client, err := NewS3Client(context.Background(), &config.Config{
		AWSRegion: "ap-south-1", S3BucketName: "qwish-test-media",
	})
	if err != nil {
		t.Fatal(err)
	}
	uploadURL, publicURL, key, err := client.PresignUpload(context.Background(), "quiz-images", "image/jpeg", 1234, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "qwish-test-media.s3.ap-south-1.amazonaws.com" || u.Path != "/"+key {
		t.Fatalf("unexpected upload endpoint: %s", uploadURL)
	}
	if !strings.HasPrefix(key, "quiz-images/") || !strings.HasSuffix(key, ".jpg") {
		t.Fatalf("unexpected key: %s", key)
	}
	if u.Query().Get("X-Amz-Expires") != "300" || u.Query().Get("X-Amz-Security-Token") != "test-session-token" {
		t.Fatalf("missing expiry or session token: %s", uploadURL)
	}
	if !strings.Contains(u.Query().Get("X-Amz-Credential"), "/ap-south-1/s3/aws4_request") {
		t.Fatalf("wrong signing region: %s", uploadURL)
	}
	if !strings.Contains(u.Query().Get("X-Amz-SignedHeaders"), "content-length") {
		t.Fatalf("content-length not signed, size limit unenforced: %s", uploadURL)
	}
	// A bodyless presign must not constrain browser uploads to an empty checksum.
	if u.Query().Has("x-amz-checksum-crc32") {
		t.Fatal("presigned PUT unexpectedly contains an empty-body checksum")
	}
	if publicURL != "https://"+u.Host+"/"+key {
		t.Fatalf("unexpected public URL: %s", publicURL)
	}
	getURL, err := client.PresignURL(context.Background(), key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	get, _ := url.Parse(getURL)
	if get.Path != "/"+key || get.Query().Get("X-Amz-Expires") != "60" {
		t.Fatalf("unexpected presigned GET: %s", getURL)
	}
}

func TestS3ConfigurationAndPublicURLs(t *testing.T) {
	configureTestCredentials(t)
	for _, tc := range []struct {
		name, region, bucket, publicURL, want string
		wantErr                               bool
	}{
		{name: "missing region", bucket: "bucket", wantErr: true},
		{name: "R2 region", region: "auto", bucket: "bucket", wantErr: true},
		{name: "missing bucket", region: "ap-south-1", wantErr: true},
		{name: "HTTP URL", region: "ap-south-1", bucket: "bucket", publicURL: "http://example.com", wantErr: true},
		{name: "query URL", region: "ap-south-1", bucket: "bucket", publicURL: "https://example.com?secret=value", wantErr: true},
		{name: "override", region: "ap-south-1", bucket: "bucket", publicURL: "https://media.example.com/base/", want: "https://media.example.com/base"},
		{name: "dotted bucket", region: "ap-south-1", bucket: "bucket.name", want: "https://s3.ap-south-1.amazonaws.com/bucket.name"},
		{name: "China region", region: "cn-north-1", bucket: "bucket", want: "https://bucket.s3.cn-north-1.amazonaws.com.cn"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewS3Client(context.Background(), &config.Config{
				AWSRegion: tc.region, S3BucketName: tc.bucket, S3PublicURL: tc.publicURL,
			})
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected a configuration error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if client.publicURL != tc.want {
				t.Fatalf("got %s, want %s", client.publicURL, tc.want)
			}
			if got := client.objectURL("images/photo #1?.jpg"); got != tc.want+"/images/photo%20%231%3F.jpg" {
				t.Fatalf("object key was not URL escaped: %s", got)
			}
		})
	}
}

func TestS3MissingCredentialsFailDuringInitialization(t *testing.T) {
	configureTestCredentials(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_FULL_URI", "")
	t.Setenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := NewS3Client(ctx, &config.Config{AWSRegion: "ap-south-1", S3BucketName: "bucket"})
	if err == nil {
		t.Fatal("expected missing credentials to fail at startup")
	}
}

func TestUploadAndDeleteUseConfiguredBucket(t *testing.T) {
	var key string
	var deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			if !bytes.Equal(body, []byte("image-data")) || r.Header.Get("Content-Type") != "image/png" {
				t.Errorf("unexpected body or content type: %q, %s", body, r.Header.Get("Content-Type"))
			}
			if !strings.HasPrefix(r.URL.Path, "/test-bucket/quiz-images/") {
				t.Errorf("wrong bucket/path: %s", r.URL.Path)
			}
			key = strings.TrimPrefix(r.URL.Path, "/test-bucket/")
			w.Header().Set("ETag", `"test"`)
		} else if r.Method == http.MethodDelete {
			if r.URL.Path != "/test-bucket/"+key {
				t.Errorf("wrong delete key: %s", r.URL.Path)
			}
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		} else {
			t.Errorf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()
	client := &S3Client{bucket: "test-bucket", publicURL: "https://test-bucket.s3.ap-south-1.amazonaws.com",
		client: s3.NewFromConfig(aws.Config{
			Region: "ap-south-1", Credentials: credentials.NewStaticCredentialsProvider("test", "secret", ""),
		}, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(server.URL)
			o.UsePathStyle = true
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		}),
	}
	publicURL, err := client.Upload(context.Background(), "quiz-images", "image/png", bytes.NewReader([]byte("image-data")), 10)
	if err != nil {
		t.Fatal(err)
	}
	if publicURL != client.publicURL+"/"+key || !strings.HasSuffix(key, ".png") {
		t.Fatalf("wrong public URL: %s", publicURL)
	}
	if err := client.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("delete request not received")
	}
}
