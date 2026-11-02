package subserver

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/subscription"
)

const testPublishToken = "psw_pub_01234567890123456789012345678901"

func publishTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func testBundle(t *testing.T, key ed25519.PrivateKey, source string, sequence int64, token string) []byte {
	t.Helper()
	data, err := subscription.Seal(&subscription.Snapshot{
		Source: source, Sequence: sequence, GeneratedAt: time.Unix(sequence, 0).UTC(),
		Settings: subscription.Settings{DisabledResponse: subscription.DisabledNotFound},
		Users:    []subscription.User{{Name: "alice", Token: token, Enabled: true}},
		Nodes:    nil,
	}, key)
	require.NoError(t, err)
	return data
}

func testOnlineServer(t *testing.T) (*Server, Config, ed25519.PrivateKey, ed25519.PrivateKey) {
	t.Helper()
	_, primaryKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, extraKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	layout := Layout{Dir: t.TempDir()}
	require.NoError(t, os.MkdirAll(layout.DataDir(), 0o750))
	require.NoError(t, os.MkdirAll(layout.InboxDir(), 0o750))
	config := DefaultConfig()
	config.Sources = []SourceConfig{
		{Name: "main", PublicKey: subscription.FormatPublicKey(primaryKey.Public().(ed25519.PublicKey)), PublishTokenHash: publishTokenHash(testPublishToken), Primary: true},
		{Name: "extra", PublicKey: subscription.FormatPublicKey(extraKey.Public().(ed25519.PublicKey))},
	}
	require.NoError(t, SaveConfig(layout, config))
	server, err := New(layout)
	require.NoError(t, err)
	return server, config, primaryKey, extraKey
}

func importRequest(method, target, token, contentType string, body []byte) *http.Request {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request
}

func TestConfigPublishTokenValidation(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	publicKey := subscription.FormatPublicKey(key.Public().(ed25519.PublicKey))
	tests := []struct {
		name    string
		sources []SourceConfig
		wantErr string
	}{
		{name: "empty is compatible", sources: []SourceConfig{{Name: "main", PublicKey: publicKey, Primary: true}}},
		{name: "valid", sources: []SourceConfig{{Name: "main", PublicKey: publicKey, PublishTokenHash: publishTokenHash(testPublishToken), Primary: true}}},
		{name: "invalid hash", sources: []SourceConfig{{Name: "main", PublicKey: publicKey, PublishTokenHash: "bad", Primary: true}}, wantErr: "publish_token_hash"},
		{name: "uppercase hash", sources: []SourceConfig{{Name: "main", PublicKey: publicKey, PublishTokenHash: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", Primary: true}}, wantErr: "lowercase"},
		{name: "duplicate hash", sources: []SourceConfig{
			{Name: "main", PublicKey: publicKey, PublishTokenHash: publishTokenHash(testPublishToken), Primary: true},
			{Name: "extra", PublicKey: publicKey, PublishTokenHash: publishTokenHash(testPublishToken)},
		}, wantErr: "unique"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			config.Sources = test.sources
			err := config.Validate()
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestOnlineBundleImport(t *testing.T) {
	server, _, primaryKey, extraKey := testOnlineServer(t)
	handler := server.Handler()
	path := "/api/v1/subscription-bundles/import"

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, importRequest(http.MethodHead, path, "wrong", "", nil))
	require.Equal(t, http.StatusUnauthorized, response.Code)

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, importRequest(http.MethodHead, path, testPublishToken, "", nil))
	require.Equal(t, http.StatusNoContent, response.Code)
	require.Equal(t, "main", response.Header().Get("X-Proxystack-Source"))

	bundle := testBundle(t, primaryKey, "main", 2, "alice-token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, importRequest(http.MethodPost, path, testPublishToken, "application/octet-stream", bundle))
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var imported importResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &imported))
	require.Equal(t, "imported", imported.Status)
	require.EqualValues(t, 2, imported.Sequence)
	require.EqualValues(t, 2, server.Health().Source[0].Sequence)

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, importRequest(http.MethodPost, path, testPublishToken, "application/octet-stream", bundle))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "already_imported")

	response = httptest.NewRecorder()
	conflict := testBundle(t, primaryKey, "main", 2, "replacement-token")
	handler.ServeHTTP(response, importRequest(http.MethodPost, path, testPublishToken, "application/octet-stream", conflict))
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())

	response = httptest.NewRecorder()
	old := testBundle(t, primaryKey, "main", 1, "old-token")
	handler.ServeHTTP(response, importRequest(http.MethodPost, path, testPublishToken, "application/octet-stream", old))
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())

	response = httptest.NewRecorder()
	other := testBundle(t, extraKey, "extra", 3, "extra-token")
	handler.ServeHTTP(response, importRequest(http.MethodPost, path, testPublishToken, "application/octet-stream", other))
	require.Equal(t, http.StatusUnprocessableEntity, response.Code, response.Body.String())

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, importRequest(http.MethodPost, path, testPublishToken, "application/json", bundle))
	require.Equal(t, http.StatusUnsupportedMediaType, response.Code)

	response = httptest.NewRecorder()
	tooLarge := importRequest(http.MethodPost, path, testPublishToken, "application/octet-stream", []byte("x"))
	tooLarge.ContentLength = subscription.MaxBundleSize + 1
	handler.ServeHTTP(response, tooLarge)
	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)

	response = httptest.NewRecorder()
	tampered := append([]byte{}, bundle...)
	tampered[len(tampered)-2] ^= 1
	handler.ServeHTTP(response, importRequest(http.MethodPost, path, testPublishToken, "application/octet-stream", tampered))
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
}

func TestImportConcurrentDoesNotRollBack(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	layout := Layout{Dir: t.TempDir()}
	config := DefaultConfig()
	config.Sources = []SourceConfig{{Name: "main", PublicKey: subscription.FormatPublicKey(key.Public().(ed25519.PublicKey)), Primary: true}}
	require.NoError(t, os.MkdirAll(layout.DataDir(), 0o750))
	one := testBundle(t, key, "main", 1, "one")
	two := testBundle(t, key, "main", 2, "two")

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for _, data := range [][]byte{one, two} {
		data := data
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := Import(layout, config, data, false)
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			require.ErrorIs(t, err, ErrStale)
		}
	}
	states := LoadSources(layout, config)
	require.Len(t, states, 1)
	require.NotNil(t, states[0].Snapshot)
	require.EqualValues(t, 2, states[0].Snapshot.Sequence)

	result, err := Import(layout, config, two, false)
	require.NoError(t, err)
	require.True(t, result.Already)
	_, err = Import(layout, config, testBundle(t, key, "main", 2, "different"), false)
	require.ErrorIs(t, err, ErrSequenceConflict)
}

func TestUnauthorizedImportDoesNotReadBody(t *testing.T) {
	server, _, _, _ := testOnlineServer(t)
	read := make(chan struct{}, 1)
	body := &trackingBody{read: read}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/subscription-bundles/import", body)
	request.Header.Set("Authorization", "Bearer wrong")
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	select {
	case <-read:
		t.Fatal("unauthorized request body was read")
	default:
	}
}

type trackingBody struct {
	read chan struct{}
}

func (b *trackingBody) Read(_ []byte) (int, error) {
	select {
	case b.read <- struct{}{}:
	default:
	}
	return 0, errors.New("unexpected read")
}

func (b *trackingBody) Close() error { return nil }
