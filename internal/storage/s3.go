package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// ObjectStore — контракт объектного хранилища.
// Позволяет подменять реализацию (например, in-memory в тестах).
type ObjectStore interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
}

// S3Config — параметры подключения к S3/MinIO.
type S3Config struct {
	Endpoint  string // "localhost:9000"
	AccessKey string
	SecretKey string
	Bucket    string // "avatars"
	UseSSL    bool
}

var s3Tracer = otel.Tracer("gophprofile/storage/s3")

// ErrObjectNotFound — объект по ключу отсутствует.
var ErrObjectNotFound = errors.New("s3 object not found")

// S3Store — тонкая обёртка над minio-клиентом, привязанная к одному bucket'у.
type S3Store struct {
	client *minio.Client
	bucket string
}

// NewS3Store подключается к MinIO/S3 и, если bucket ещё не существует, создаёт его.
func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio.New: %w", err)
	}

	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("bucket exists: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, fmt.Errorf("make bucket: %w", err)
		}
	}

	return &S3Store{client: client, bucket: cfg.Bucket}, nil
}

// Put загружает поток в объект под ключом key. contentType — MIME тип.
// size — точный размер (если неизвестен, передавай -1: потоковая загрузка).
func (s *S3Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	ctx, span := s3Tracer.Start(ctx, "s3.put",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("s3.bucket", s.bucket),
			attribute.String("s3.key", key),
			attribute.String("s3.content_type", contentType),
			attribute.Int64("s3.size_bytes", size),
		))
	defer span.End()

	_, err := s.client.PutObject(ctx, s.bucket, key, r, size,
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}

// Get отдаёт поток объекта. Вызывающий обязан вызвать Close.
// Если объект не найден — возвращает ErrObjectNotFound.
func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	ctx, span := s3Tracer.Start(ctx, "s3.get",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("s3.bucket", s.bucket),
			attribute.String("s3.key", key),
		))
	defer span.End()

	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if e := minio.ToErrorResponse(err); e.Code == "NoSuchKey" {
			return nil, ErrObjectNotFound
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("stat object %s: %w", key, err)
	}
	return obj, nil
}

// Delete удаляет объект. Отсутствие объекта не считается ошибкой.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	ctx, span := s3Tracer.Start(ctx, "s3.delete",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("s3.bucket", s.bucket),
			attribute.String("s3.key", key),
		))
	defer span.End()

	if err := s.client.RemoveObject(ctx, s.bucket, key,
		minio.RemoveObjectOptions{}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("remove object %s: %w", key, err)
	}
	return nil
}

// HealthCheck проверяет доступность S3-бакета.
func (s *S3Store) HealthCheck(ctx context.Context) error {
	ctx, span := s3Tracer.Start(ctx, "s3.health_check",
		trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	_, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return err
}
