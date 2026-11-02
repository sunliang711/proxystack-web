package api_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSHPublishSecretsAreOneTimeAndNotCached(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	response, _ := h.do(http.MethodPost, "/api/auth/password", map[string]string{
		"old_password": password, "new_password": "a-new-password",
	})
	require.Equal(t, http.StatusNoContent, response.StatusCode)

	response, identity := h.do(http.MethodPost, "/api/sub-publish/identity", nil)
	require.Equal(t, http.StatusOK, response.StatusCode, identity)
	require.Equal(t, true, identity["configured"])
	require.NotContains(t, identity, "seed")
	require.NotContains(t, identity, "private_key")

	response, created := h.do(http.MethodPost, "/api/sub-publish/targets", map[string]any{
		"name": "primary", "ssh_host": "sub.example.com", "ssh_port": 22, "ssh_user": "proxystack-tunnel",
		"host_key_algorithm": "ssh-ed25519", "host_key_fingerprint": "SHA256:" + strings.Repeat("A", 43),
		"remote_port": 9444, "source": "main", "enabled": true,
	})
	require.Equal(t, http.StatusOK, response.StatusCode, created)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	secret := created["secret"].(string)
	require.NotEmpty(t, secret)
	targetID := int64(created["target"].(map[string]any)["id"].(float64))

	request, err := http.NewRequest(http.MethodGet, h.base+"/api/sub-publish/targets", nil)
	require.NoError(t, err)
	listResponse, err := h.client.Do(request)
	require.NoError(t, err)
	listBody, err := io.ReadAll(listResponse.Body)
	require.NoError(t, err)
	require.NoError(t, listResponse.Body.Close())
	require.Equal(t, http.StatusOK, listResponse.StatusCode, string(listBody))
	require.NotContains(t, string(listBody), secret)

	response, rotated := h.do(http.MethodPost, "/api/sub-publish/targets/"+strconv.FormatInt(targetID, 10)+"/rotate-secret", nil)
	require.Equal(t, http.StatusOK, response.StatusCode, rotated)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	require.NotEqual(t, secret, rotated["secret"])
}
