package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthMiddleware(t *testing.T) {
	cases := []struct {
		name       string
		header     string
		wantStatus int
		wantUserID int64
	}{
		{"no header", "", http.StatusUnauthorized, 0},
		{"non-numeric", "abc", http.StatusUnauthorized, 0},
		{"zero", "0", http.StatusUnauthorized, 0},
		{"negative", "-5", http.StatusUnauthorized, 0},
		{"valid", "42", http.StatusOK, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var seen int64
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
