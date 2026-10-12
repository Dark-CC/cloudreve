package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth"
	"github.com/cloudreve/Cloudreve/v4/pkg/cluster/routes"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Interop tests against a real, live Cloudreve slave binary (any version,
// including an upstream build). They verify that this fork's remote storage
// client speaks the same slave wire protocol upstream produces and consumes.
//
// Run a slave node first, e.g.:
//
//	./cloudreve -c conf.ini        # [System] Mode = slave, Listen = :5213
//
// then:
//
//	SLAVE_E2E=http://127.0.0.1:5213 SLAVE_E2E_SECRET=<secret> \
//	  SLAVE_E2E_BLOBDIR=/abs/blob/dir go test ./pkg/filemanager/driver/remote/
//
// SLAVE_E2E_BLOBDIR defaults to <exe-dir>/data/uploads resolution on the
// slave; pass the absolute path the slave can see.
func slaveE2E(t *testing.T) (*remoteClient, *ent.StoragePolicy) {
	t.Helper()
	base := os.Getenv("SLAVE_E2E")
	if base == "" {
		t.Skip("SLAVE_E2E not set; skipping live slave interop test")
	}
	secret := os.Getenv("SLAVE_E2E_SECRET")
	require.NotEmpty(t, secret, "SLAVE_E2E_SECRET must match the slave's [Slave] Secret")

	a := auth.HMACAuth{SecretKey: []byte(secret)}
	node := &ent.Node{ID: 1, Type: "slave", Server: base, SlaveKey: secret}
	policy := &ent.StoragePolicy{
		ID:       1,
		Type:     types.PolicyTypeRemote,
		NodeID:   node.ID,
		Settings: &types.PolicySetting{ChunkSize: 16},
	}
	policy.Edges.Node = node

	return &remoteClient{
		policy:       policy,
		authInstance: a,
		l:            logging.NewConsoleLogger(logging.LevelError),
		httpClient: request.NewClient(
			nil,
			request.WithEndpoint(base+constants.APIPrefixSlave+"/"),
			request.WithCredential(a, 600),
			request.WithSlaveMeta(node.ID),
			request.WithCorrelationID(),
		),
	}, policy
}

func e2eBlobDir() string {
	if d := os.Getenv("SLAVE_E2E_BLOBDIR"); d != "" {
		return d
	}
	return "data/uploads"
}

// TestInteropSlaveList lists a directory on the slave via the signed RPC —
// the same call the import-files task and blob audit make.
func TestInteropSlaveList(t *testing.T) {
	c, _ := slaveE2E(t)

	marker := fmt.Sprintf("interop-%d.txt", time.Now().UnixNano())
	objects, err := c.List(context.Background(), e2eBlobDir(), true)
	require.NoError(t, err)
	for _, o := range objects {
		t.Logf("listed: %s (dir=%v, size=%d)", o.Source, o.IsDir, o.Size)
	}
	_ = marker
}

// TestInteropSlaveContentURL signs a content URL the way the master does and
// fetches bytes directly — the path browsers take when downloading from a
// remote-policy entity.
func TestInteropSlaveContentURL(t *testing.T) {
	c, policy := slaveE2E(t)
	ctx := context.Background()

	src := os.Getenv("SLAVE_E2E_SRC")
	if src == "" {
		// Discover the first non-dir object via List.
		objects, err := c.List(ctx, e2eBlobDir(), true)
		require.NoError(t, err)
		for _, o := range objects {
			if !o.IsDir && o.Size > 0 {
				src = o.Source
				break
			}
		}
		require.NotEmpty(t, src, "no files found under %s; set SLAVE_E2E_SRC", e2eBlobDir())
	}

	serverURL, err := url.Parse(policy.Edges.Node.Server)
	require.NoError(t, err)
	contentURL := routes.SlaveFileContentUrl(serverURL, src, "interop.txt", true, 0, policy.Edges.Node.ID)
	expire := time.Now().Add(10 * time.Minute)
	signed, err := auth.SignURI(ctx, c.authInstance, contentURL.String(), &expire)
	require.NoError(t, err)

	resp, err := http.Get(signed.String())
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)
	require.NotEmpty(t, body)
	t.Logf("served %d bytes from upstream slave", len(body))

	// A tampered signature must be rejected. API errors arrive as HTTP 200
	// with a non-zero code in the JSON body.
	tampered := strings.Replace(signed.String(), "sign=", "sign=0000", 1)
	resp2, err := http.Get(tampered)
	require.NoError(t, err)
	defer resp2.Body.Close()
	var tamperRes struct {
		Code int `json:"code"`
	}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&tamperRes))
	require.NotEqual(t, 0, tamperRes.Code, "tampered signature was accepted")
}

// TestInteropSlaveUploadSession exercises the write path: create a session on
// the slave (signed PUT), push one chunk to the signed upload URL, then delete
// the session.
func TestInteropSlaveUploadSession(t *testing.T) {
	c, policy := slaveE2E(t)
	ctx := context.Background()

	blobPath := e2eBlobDir() + "/interop-session-blob.bin"
	uri, err := fs.NewUriFromString("cloudreve://my/interop-session-blob.bin")
	require.NoError(t, err)
	session := &fs.UploadSession{
		Props: &fs.UploadProps{
			UploadSessionID: uuid.Must(uuid.NewV4()).String(),
			Size:            16,
			SavePath:        blobPath,
			Uri:             uri,
			ExpireAt:        time.Now().Add(10 * time.Minute),
		},
		Policy: policy,
	}

	require.NoError(t, c.CreateUploadSession(ctx, session, true))
	t.Cleanup(func() {
		_ = c.DeleteUploadSession(context.Background(), session.Props.UploadSessionID)
	})

	// Upload a chunk through the signed URL + Authorization header path.
	uploadURL, sign, err := c.GetUploadURL(ctx, session.Props.ExpireAt, session.Props.UploadSessionID)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", uploadURL+"?chunk=0", strings.NewReader("0123456789abcdef"))
	require.NoError(t, err)
	req.Header.Set("Authorization", sign)
	req.ContentLength = 16
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "chunk upload failed: %s", body)

	require.NoError(t, c.DeleteUploadSession(ctx, session.Props.UploadSessionID))
}

// TestInteropSlaveDelete exercises the signed DELETE file RPC.
func TestInteropSlaveDelete(t *testing.T) {
	c, _ := slaveE2E(t)
	ctx := context.Background()

	// Seed a file on the slave disk via direct write is not possible from
	// here; reuse list to pick a disposable path created by the upload test,
	// or create one through the session flow. We simply verify the RPC is
	// accepted and reports per-file failures correctly for a missing file.
	failed, err := c.DeleteFiles(ctx, e2eBlobDir()+"/definitely-missing-interop.bin")
	// Upstream returns partial success (CodeNotFullySuccess) for missing
	// files; success is also acceptable if the slave silently ignores.
	if err != nil {
		require.NotEmpty(t, failed)
		t.Logf("delete missing file -> expected per-file failure: %v", err)
	} else {
		require.Empty(t, failed)
	}
}

// TestInteropSlavePingVersionGate documents the only version-sensitive
// surface: the admin "test node communication" flow. The slave pings the
// master's /api/v4/site/ping and reports a warning when the reported version
// string does not equal its own BackendVersion. Storage RPCs carry no such
// check, and a version mismatch no longer fails the ping.
func TestInteropSlavePingVersionGate(t *testing.T) {
	c, _ := slaveE2E(t)
	ctx := context.Background()

	var reportedVersion string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":0,"data":%q}`, reportedVersion)
	}))
	defer stub.Close()

	ping := func(version string) *serializer.Response {
		reportedVersion = version
		body := fmt.Sprintf(`{"callback":%q}`, stub.URL)
		resp, err := c.httpClient.Request(
			"POST", "ping", strings.NewReader(body),
			request.WithContext(ctx),
		).CheckHTTPResponse(200).DecodeResponse()
		require.NoError(t, err)
		return resp
	}

	// A foreign version string must succeed (code 0) and carry a warning.
	resp := ping("4.99.0-pro")
	require.Equal(t, 0, resp.Code,
		"version mismatch must not fail the ping, got code=%d msg=%q", resp.Code, resp.Msg)
	require.Contains(t, resp.Msg, "Version mismatch")
	t.Logf("version gate warning: %s", resp.Msg)
}
