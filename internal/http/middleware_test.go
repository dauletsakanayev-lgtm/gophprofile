package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthMiddleware(t *testing.T) {
	cases := []struct {
		name       string
		header     string
		wantStatus int
		wantUserID string
	}{
		{"no header", "", http.StatusUnauthorized, ""},
		{"too long", strings.Repeat("a", 256), http.StatusUnauthorized, ""},
		{"valid string", "alice@example.com", http.StatusOK, "alice@example.com"},
		{"single char", "x", http.StatusOK, "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen string
			h := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = userIDFromCtx(r.Context())
				w.WriteHeader(http.StatusOK)
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("X-User-ID", tc.header)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			require.Equal(t, tc.wantStatus, rr.Code)
			require.Equal(t, tc.wantUserID, seen)
		})
	}
}
