package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config — параметры подключения к S3/MinIO.
type S3Config struct {
	Endpoint  string // "localhost:9000"
	AccessKey string
	SecretKey string
	Bucket    string // "avatars"
	UseSSL    bool
}

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
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size,
		minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object %s: %w", key, err)
	}
	return nil
}

// Get отдаёт поток объекта. Вызывающий обязан вызвать Close.
// Если объект не найден — возвращает ErrObjectNotFound.
func (s *S3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	// GetObject возвращает объект лениво — проверяем существование через Stat.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if e := minio.ToErrorResponse(err); e.Code == "NoSuchKey" {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("stat object %s: %w", key, err)
	}
	return obj, nil
}

// Delete удаляет объект. Отсутствие объекта не считается ошибкой.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.bucket, key,
		minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("remove object %s: %w", key, err)
	}
	return nil
}
