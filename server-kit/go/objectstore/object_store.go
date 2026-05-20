package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	runtimeconfig "github.com/nmxmxh/ovasabi_foundation/config-contracts/go/runtimeconfig"
)

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
}

type Object struct {
	Key         string
	Bucket      string
	ContentType string
	Size        int64
	ETag        string
	URL         string
	Metadata    map[string]string
}

type storedObject struct {
	payload     []byte
	contentType string
	metadata    map[string]string
}

type Store struct {
	Endpoint        string
	PresignEndpoint string
	Region          string
	Bucket          string
	AccessKey       string
	SecretKey       string
	UseTLS          bool
	Strict          bool

	memory bool
	mu     sync.RWMutex
	blobs  map[string]storedObject

	session *session.Session
	client  *s3.S3

	presignSession *session.Session
	presignClient  *s3.S3
}

func New(cfg runtimeconfig.ObjectStorageConfig) *Store {
	store := &Store{
		Endpoint:        strings.TrimSpace(cfg.Endpoint),
		PresignEndpoint: strings.TrimSpace(cfg.PresignEndpoint),
		Region:          strings.TrimSpace(cfg.Region),
		Bucket:          strings.TrimSpace(cfg.Bucket),
		AccessKey:       strings.TrimSpace(cfg.AccessKey),
		SecretKey:       strings.TrimSpace(cfg.SecretKey),
		UseTLS:          cfg.UseTLS,
		Strict:          cfg.Strict,
	}
	if isMemoryEndpoint(store.Endpoint) {
		store.memory = true
		store.blobs = map[string]storedObject{}
		return store
	}
	return store
}

func (s *Store) Describe() map[string]string {
	if s == nil {
		return map[string]string{}
	}
	driver := "s3-compatible"
	if s.memory {
		driver = "memory"
	}
	return map[string]string{
		"driver":   driver,
		"endpoint": s.Endpoint,
		"region":   s.Region,
		"bucket":   s.Bucket,
	}
}

func (s *Store) PutBytes(ctx context.Context, key string, payload []byte, opts PutOptions) (Object, error) {
	if s == nil {
		return Object{}, fmt.Errorf("object store is required")
	}
	key = normalizeKey(key)
	if key == "" {
		return Object{}, fmt.Errorf("object key is required")
	}
	if s.Bucket == "" {
		return Object{}, fmt.Errorf("object storage bucket is required")
	}
	contentType := strings.TrimSpace(opts.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if s.memory {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.blobs == nil {
			s.blobs = map[string]storedObject{}
		}
		s.blobs[key] = storedObject{
			payload:     append([]byte(nil), payload...),
			contentType: contentType,
			metadata:    cloneMetadata(opts.Metadata),
		}
		return Object{
			Key:         key,
			Bucket:      s.Bucket,
			ContentType: contentType,
			Size:        int64(len(payload)),
			URL:         s.ObjectURL(key),
			Metadata:    cloneMetadata(opts.Metadata),
		}, nil
	}

	client, err := s.s3Client()
	if err != nil {
		return Object{}, err
	}
	input := &s3.PutObjectInput{
		Bucket:      new(s.Bucket),
		Key:         new(key),
		Body:        bytes.NewReader(payload),
		ContentType: new(contentType),
		Metadata:    aws.StringMap(cloneMetadata(opts.Metadata)),
	}
	output, err := client.PutObjectWithContext(ctxOrBackground(ctx), input)
	if err != nil {
		return Object{}, err
	}
	return Object{
		Key:         key,
		Bucket:      s.Bucket,
		ContentType: contentType,
		Size:        int64(len(payload)),
		ETag:        aws.StringValue(output.ETag),
		URL:         s.ObjectURL(key),
		Metadata:    cloneMetadata(opts.Metadata),
	}, nil
}

func (s *Store) PresignPut(ctx context.Context, key, contentType string, expiry time.Duration) (string, error) {
	if s == nil {
		return "", fmt.Errorf("object store is required")
	}
	key = normalizeKey(key)
	if key == "" {
		return "", fmt.Errorf("object key is required")
	}
	if s.memory {
		return s.ObjectURL(key), nil
	}

	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	client, err := s.getPresignClient()
	if err != nil {
		return "", err
	}
	req, _ := client.PutObjectRequest(&s3.PutObjectInput{
		Bucket:      new(s.Bucket),
		Key:         new(key),
		ContentType: new(contentType),
	})
	return req.Presign(expiry)
}

func (s *Store) PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if s == nil {
		return "", fmt.Errorf("object store is required")
	}
	key = normalizeKey(key)
	if key == "" {
		return "", fmt.Errorf("object key is required")
	}
	if s.memory {
		return s.ObjectURL(key), nil
	}

	client, err := s.getPresignClient()
	if err != nil {
		return "", err
	}
	req, _ := client.GetObjectRequest(&s3.GetObjectInput{
		Bucket: new(s.Bucket),
		Key:    new(key),
	})
	return req.Presign(expiry)
}

func (s *Store) PutFile(ctx context.Context, key, localPath string, opts PutOptions) (Object, error) {
	if s == nil {
		return Object{}, fmt.Errorf("object store is required")
	}
	key = normalizeKey(key)
	if key == "" {
		return Object{}, fmt.Errorf("object key is required")
	}
	if s.Bucket == "" {
		return Object{}, fmt.Errorf("object storage bucket is required")
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return Object{}, err
	}
	contentType := strings.TrimSpace(opts.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if s.memory {
		// #nosec G304 -- PutFile uploads an explicit caller-selected local file path; object keys remain separately normalized.
		payload, err := os.ReadFile(localPath)
		if err != nil {
			return Object{}, err
		}
		return s.PutBytes(ctx, key, payload, PutOptions{
			ContentType: contentType,
			Metadata:    opts.Metadata,
		})
	}

	// #nosec G304 -- PutFile uploads an explicit caller-selected local file path; object keys remain separately normalized.
	file, err := os.Open(localPath)
	if err != nil {
		return Object{}, err
	}
	defer func() { _ = file.Close() }()

	client, err := s.s3Client()
	if err != nil {
		return Object{}, err
	}
	output, err := client.PutObjectWithContext(ctxOrBackground(ctx), &s3.PutObjectInput{
		Bucket:      new(s.Bucket),
		Key:         new(key),
		Body:        file,
		ContentType: new(contentType),
		Metadata:    aws.StringMap(cloneMetadata(opts.Metadata)),
	})
	if err != nil {
		return Object{}, err
	}
	return Object{
		Key:         key,
		Bucket:      s.Bucket,
		ContentType: contentType,
		Size:        info.Size(),
		ETag:        aws.StringValue(output.ETag),
		URL:         s.ObjectURL(key),
		Metadata:    cloneMetadata(opts.Metadata),
	}, nil
}

func (s *Store) ReadBytes(ctx context.Context, key string) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("object store is required")
	}
	key = normalizeKey(key)
	if key == "" {
		return nil, fmt.Errorf("object key is required")
	}
	if s.memory {
		s.mu.RLock()
		defer s.mu.RUnlock()
		blob, ok := s.blobs[key]
		if !ok {
			return nil, fmt.Errorf("object %s not found", key)
		}
		return append([]byte(nil), blob.payload...), nil
	}

	client, err := s.s3Client()
	if err != nil {
		return nil, err
	}
	output, err := client.GetObjectWithContext(ctxOrBackground(ctx), &s3.GetObjectInput{
		Bucket: new(s.Bucket),
		Key:    new(key),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = output.Body.Close() }()
	return io.ReadAll(output.Body)
}

func (s *Store) StageToTempFile(ctx context.Context, key, prefix, suffix string) (string, func() error, error) {
	if s == nil {
		return "", nil, fmt.Errorf("object store is required")
	}
	payload, err := s.ReadBytes(ctx, key)
	if err != nil {
		return "", nil, err
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "ovasabi-object-*"
	}
	if suffix != "" && !strings.HasPrefix(suffix, ".") {
		suffix = "." + suffix
	}
	pattern := prefix
	if suffix != "" {
		pattern += suffix
	}
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	path := filepath.Clean(file.Name())
	cleanup := func() error {
		return os.Remove(path)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		_ = cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func (s *Store) ObjectURL(key string) string {
	if s == nil {
		return ""
	}
	key = normalizeKey(key)
	if key == "" {
		return ""
	}
	if s.memory {
		return "memory://" + path.Join(strings.TrimSpace(s.Bucket), key)
	}

	base := strings.TrimRight(strings.TrimSpace(s.Endpoint), "/")
	if base == "" {
		return ""
	}
	if parsed, err := url.Parse(base); err == nil && parsed.Scheme != "" {
		parsed.Path = path.Join(parsed.Path, s.Bucket, key)
		return parsed.String()
	}
	return base + "/" + path.Join(s.Bucket, key)
}

func (s *Store) s3Client() (*s3.S3, error) {
	if s == nil {
		return nil, fmt.Errorf("object store is required")
	}
	if s.client != nil {
		return s.client, nil
	}
	if strings.TrimSpace(s.Endpoint) == "" {
		return nil, fmt.Errorf("object storage endpoint is required")
	}
	if strings.TrimSpace(s.Region) == "" {
		return nil, fmt.Errorf("object storage region is required")
	}
	if strings.TrimSpace(s.AccessKey) == "" || strings.TrimSpace(s.SecretKey) == "" {
		return nil, fmt.Errorf("object storage credentials are required")
	}

	sess, err := session.NewSession(&aws.Config{
		Credentials:      credentials.NewStaticCredentials(s.AccessKey, s.SecretKey, ""),
		DisableSSL:       new(!s.UseTLS),
		Endpoint:         new(s.Endpoint),
		Region:           new(s.Region),
		S3ForcePathStyle: new(true),
	})
	if err != nil {
		return nil, err
	}
	s.session = sess
	s.client = s3.New(sess)
	return s.client, nil
}

func (s *Store) getPresignClient() (*s3.S3, error) {
	if s == nil {
		return nil, fmt.Errorf("object store is required")
	}
	if s.PresignEndpoint == "" || s.PresignEndpoint == s.Endpoint {
		return s.s3Client()
	}
	if s.presignClient != nil {
		return s.presignClient, nil
	}
	if strings.TrimSpace(s.Region) == "" {
		return nil, fmt.Errorf("object storage region is required")
	}
	if strings.TrimSpace(s.AccessKey) == "" || strings.TrimSpace(s.SecretKey) == "" {
		return nil, fmt.Errorf("object storage credentials are required")
	}

	sess, err := session.NewSession(&aws.Config{
		Credentials:      credentials.NewStaticCredentials(s.AccessKey, s.SecretKey, ""),
		DisableSSL:       new(!s.UseTLS),
		Endpoint:         new(s.PresignEndpoint),
		Region:           new(s.Region),
		S3ForcePathStyle: new(true),
	})
	if err != nil {
		return nil, err
	}
	s.presignSession = sess
	s.presignClient = s3.New(sess)
	return s.presignClient, nil
}

func isMemoryEndpoint(endpoint string) bool {
	endpoint = strings.ToLower(strings.TrimSpace(endpoint))
	return strings.HasPrefix(endpoint, "memory://") || strings.HasPrefix(endpoint, "mem://")
}

func normalizeKey(key string) string {
	key = path.Clean(strings.TrimSpace(key))
	key = strings.TrimPrefix(key, "./")
	key = strings.TrimPrefix(key, "/")
	if key == "." {
		return ""
	}
	return key
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func ctxOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
