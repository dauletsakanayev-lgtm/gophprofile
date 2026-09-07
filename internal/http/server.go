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
func New(addr string, ah *AvatarHandler, hh *HealthHandler) *Server {
	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))

	// /health и /healthz — оба возвращают глубокий статус.
	r.Method(http.MethodGet, "/health", hh)
	r.Method(http.MethodGet, "/healthz", hh)

	// Статика фронтенда (одностраничка от Yandex Practicum).
	r.Handle("/", http.RedirectHandler("/web/", http.StatusFound))

	// Статика + SPA-роуты по ТЗ.
	r.Route("/web", func(r chi.Router) {
		serveIndex := func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, "web/static/index.html")
		}
		r.Get("/", serveIndex)
		r.Get("/upload", serveIndex)
		r.Get("/gallery/{user_id}", serveIndex)
		r.Post("/upload", ah.WebUpload)
		// Fallback — статика (JS/CSS/картинки).
		r.Handle("/*", http.StripPrefix("/web/",
			http.FileServer(http.Dir("web/static"))))
	})

	r.Route("/api/v1", func(r chi.Router) {
		// Публичные (без X-User-ID): чтение.
		r.Get("/avatars/{id}", ah.Get)
		r.Get("/avatars/{id}/metadata", ah.GetMetadata)
		r.Get("/users/{user_id}/avatar", ah.GetUserAvatar)
		r.Get("/users/{user_id}/avatars", ah.ListUserAvatars)

		// Защищённые (с X-User-ID): запись.
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware)
			r.Post("/avatars", ah.Create)
			r.Delete("/avatars/{id}", ah.Delete)
			r.Delete("/users/{user_id}/avatar", ah.DeleteUserAvatar)
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
