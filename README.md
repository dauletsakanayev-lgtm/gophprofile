# GophProfile

Микросервис управления аватарками пользователей — учебный проект курса «Go-разработчик» от Yandex Practicum.

## Стек

- Go 1.26, chi
- PostgreSQL (метаданные)
- MinIO (S3-совместимое хранилище)
- RabbitMQ (очередь на асинхронную обработку)
- Docker Compose

## Быстрый старт (в WSL)

```bash
cd /mnt/c/Users/daulet.sakanayev/Desktop/dev/gophprofile
docker compose up -d
make build
./bin/server
./bin/worker