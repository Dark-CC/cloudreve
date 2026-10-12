package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newPingTestContext(t *testing.T) *gin.Context {
	gin.SetMode(gin.TestMode)
	logger := logging.NewConsoleLogger(logging.LevelError)
	cfg, err := conf.NewIniConfigProvider(filepath.Join(t.TempDir(), "conf.ini"), logger)
	require.NoError(t, err)

	dep := dependency.NewDependency(
		dependency.WithConfigProvider(cfg),
		dependency.WithLogger(logger),
	)

	engine := gin.New()
	engine.ContextWithFallback = true
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), engine)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Request = c.Request.WithContext(
		context.WithValue(c.Request.Context(), dependency.DepCtx{}, dep))
	return c
}

func fakeMaster(t *testing.T, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.Close)
	return s
}

// TestSlavePingVersionMismatchWarns: a master reporting a different version
// must produce a warning, not a failure — mixed-version deployments (e.g.
// upstream master + this fork's slave) share the same wire protocol.
func TestSlavePingVersionMismatchWarns(t *testing.T) {
	master := fakeMaster(t, `{"code":0,"data":"4.14.0"}`)
	service := &SlavePingService{Callback: master.URL}

	warning, err := service.Test(newPingTestContext(t))
	require.NoError(t, err)
	require.Contains(t, warning, "Version mismatch")
	require.Contains(t, warning, "4.14.0")
	require.Contains(t, warning, constants.BackendVersion)
}

// TestSlavePingVersionMatchSilent: identical versions produce no warning.
func TestSlavePingVersionMatchSilent(t *testing.T) {
	master := fakeMaster(t, fmt.Sprintf(`{"code":0,"data":"%s"}`, constants.BackendVersion))
	service := &SlavePingService{Callback: master.URL}

	warning, err := service.Test(newPingTestContext(t))
	require.NoError(t, err)
	require.Empty(t, warning)
}

// TestSlavePingProSuffixIgnored: "x.y.z-pro" normalizes to "x.y.z".
func TestSlavePingProSuffixIgnored(t *testing.T) {
	master := fakeMaster(t, fmt.Sprintf(`{"code":0,"data":"%s-pro"}`, constants.BackendVersion))
	service := &SlavePingService{Callback: master.URL}

	warning, err := service.Test(newPingTestContext(t))
	require.NoError(t, err)
	require.Empty(t, warning)
}

// TestSlavePingUnreachableMasterFails: connectivity failures still fail hard.
func TestSlavePingUnreachableMasterFails(t *testing.T) {
	service := &SlavePingService{Callback: "http://127.0.0.1:0/"}

	_, err := service.Test(newPingTestContext(t))
	require.Error(t, err)
}

// TestSlavePingMalformedVersionFails: a non-string version payload returns a
// normal error instead of panicking on the type assertion.
func TestSlavePingMalformedVersionFails(t *testing.T) {
	master := fakeMaster(t, `{"code":0,"data":42}`)
	service := &SlavePingService{Callback: master.URL}

	_, err := service.Test(newPingTestContext(t))
	require.Error(t, err)
}
