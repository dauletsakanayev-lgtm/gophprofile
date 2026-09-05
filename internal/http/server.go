package http

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// Server оборачивает http.Server с graceful shutdown при отмене ctx.
type Server struct {
	srv  *http.Server
	addr string
}

// New собирает роутер и возвращает готовый сервер.
func New(addr string, ah *AvatarHandler) *Server {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Head("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(AuthMiddleware)
		r.Route("/avatars", func(r chi.Router) {
			r.Post("/", ah.Create)
			r.Get("/", ah.List)
			r.Get("/{id}", ah.Get)
			r.Get("/{id}/original", ah.DownloadOriginal)
			r.Get("/{id}/processed", ah.DownloadProcessed)
			r.Delete("/{id}", ah.Delete)
		})
	})

	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           r,
			ReadHeaderTimeout: 5 * time.Second,
		},
		addr: addr,
	}
}

// Run слушает на addr и корректно завершает сервер при отмене ctx.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		log.Println("HTTP listening on", s.addr)
		errCh <- s.srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
