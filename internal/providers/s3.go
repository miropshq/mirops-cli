package providers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Provider reads objects from AWS S3.
// Credentials are resolved automatically from the environment:
//
//	AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_REGION
//	AWS_SESSION_TOKEN (optional, for temporary credentials)
//	AWS_PROFILE       (optional, to use a named profile)
type S3Provider struct{}

func NewS3() *S3Provider {
	return &S3Provider{}
}

// Fetch downloads an object from S3.
// source format: s3://bucket/path/to/object
func (p *S3Provider) Fetch(source string) ([]byte, error) {
	path := strings.TrimPrefix(source, "s3://")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid S3 source %q, expected s3://bucket/key", source)
	}
	bucket, key := parts[0], parts[1]

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	client := s3.NewFromConfig(cfg)
	resp, err := client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, fmt.Errorf("fetching s3://%s/%s: %w", bucket, key, err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return nil, fmt.Errorf("reading S3 response body: %w", err)
	}
	return buf.Bytes(), nil
}
