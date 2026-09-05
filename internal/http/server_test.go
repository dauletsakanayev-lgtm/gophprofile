package http

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServer_New_ReturnsNonNil(t *testing.T) {
	ah := NewAvatarHandler(newFakeRepo(), newFakeS3(), &fakePub{})
	s := New(":0", ah)
	require.NotNil(t, s)
}

func TestServer_Run_ShutdownOnCtxCancel(t *testing.T) {
	ah := NewAvatarHandler(newFakeRepo(), newFakeS3(), &fakePub{})
	s := New("127.0.0.1:0", ah)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil && err != http.ErrServerClosed {
			require.NoError(t, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}
