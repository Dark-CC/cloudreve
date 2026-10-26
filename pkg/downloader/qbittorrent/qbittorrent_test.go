package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/stretchr/testify/require"
)

type stubSettingProvider struct {
	setting.Provider
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *qbittorrentClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	d, err := NewClient(
		logging.NewConsoleLogger(logging.LevelError),
		request.NewClient(nil),
		&stubSettingProvider{},
		&types.QBittorrentSetting{Server: srv.URL, TempPath: "/tmp"},
	)
	require.NoError(t, err)
	return d.(*qbittorrentClient)
}

func TestCreateTaskLegacyOk(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/torrents/add", r.URL.Path)
		_, _ = w.Write([]byte("Ok."))
	})

	handle, err := c.CreateTask(context.Background(), "magnet:?xt=urn:btih:abc", nil)
	require.NoError(t, err)
	require.NotEmpty(t, handle.ID)
}

// WebAPI >= 2.14.0 (qBittorrent 5.2+) returns a JSON summary with status 200.
func TestCreateTaskNewAPIJson(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"added_torrent_ids":["66efe1be0eb8f73fe1222c60694308cfcfb28a74"],"failure_count":0,"pending_count":0,"success_count":1}`))
	})

	handle, err := c.CreateTask(context.Background(), "magnet:?xt=urn:btih:abc", nil)
	require.NoError(t, err)
	require.NotEmpty(t, handle.ID)
}

// WebAPI >= 2.14.0 returns 202 when entries are still pending.
func TestCreateTaskNewAPIPending(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"added_torrent_ids":[],"failure_count":0,"pending_count":1,"success_count":0}`))
	})

	_, err := c.CreateTask(context.Background(), "https://example.com/f.torrent", nil)
	require.NoError(t, err)
}

// WebAPI >= 2.14.0 returns 409 when nothing was added.
func TestCreateTaskNewAPIConflict(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte("Conflict"))
	})

	_, err := c.CreateTask(context.Background(), "magnet:?xt=urn:btih:abc", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "409")
}

// New API success: HTTP 200 with an empty body instead of "Ok.".
func TestLoginNewAPIEmptyBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/auth/login", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
	require.NoError(t, c.login(context.Background()))
}

// New API failure: HTTP 401 on bad credentials.
func TestLoginNewAPIUnauthorized(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	require.Error(t, c.login(context.Background()))
}

// Old API failure: HTTP 200 with "Fails." body.
func TestLoginLegacyFails(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Fails."))
	})
	require.Error(t, c.login(context.Background()))
}
