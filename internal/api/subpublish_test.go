package api_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/subpublish"
)

type apiDeployFake struct {
	status *subpublish.DeployStatus
	err    error
}

func (f apiDeployFake) InspectDeploy(context.Context, subpublish.Identity, subpublish.Target) (*subpublish.DeployStatus, error) {
	return f.status, f.err
}

func TestDeployInspectReturnsRemoteFailureDetails(t *testing.T) {
	h, password := newHarness(t)
	h.login("admin", password)
	response, _ := h.do(http.MethodPost, "/api/auth/password", map[string]string{
		"old_password": password, "new_password": "a-new-password",
	})
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	response, identity := h.do(http.MethodPost, "/api/sub-publish/identity", nil)
	require.Equal(t, http.StatusOK, response.StatusCode, identity)
	response, created := h.do(http.MethodPost, "/api/sub-publish/targets", map[string]any{
		"name": "offline", "ssh_host": "sub.example.com", "ssh_port": 22, "ssh_user": "proxystack-tunnel",
		"host_key_algorithm": "ssh-ed25519", "host_key_fingerprint": "SHA256:" + strings.Repeat("A", 43),
		"remote_port": 9444, "source": "main", "enabled": true,
	})
	require.Equal(t, http.StatusOK, response.StatusCode, created)
	targetID := int64(created["target"].(map[string]any)["id"].(float64))
	h.svc.SubDeployer = apiDeployFake{err: errors.New("SSH TCP connection failed")}
	response, body := h.do(http.MethodPost, "/api/sub-publish/targets/"+strconv.FormatInt(targetID, 10)+"/deploy/inspect", nil)
	require.Equal(t, http.StatusBadGateway, response.StatusCode, body)
	require.Equal(t, "SSH TCP connection failed", body["detail"])
}

func (apiDeployFake) Deploy(context.Context, subpublish.Identity, subpublish.Target, subpublish.DeployRequest) (*subpublish.DeployResult, error) {
	panic("unexpected deploy call")
}

func (apiDeployFake) RollbackDeploy(context.Context, subpublish.Identity, subpublish.Target) (*subpublish.DeployResult, error) {
	panic("unexpected rollback call")
}

func TestSSHPublishSecretsStayMaskedAndSetupCanBeViewed(t *testing.T) {
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
	h.svc.SubDeployer = apiDeployFake{status: &subpublish.DeployStatus{
		OS: "linux", Arch: "amd64", Sudo: true, State: "installed", Version: "v0.2.0", PreviousVersion: "v0.1.0",
		ServiceActive: true, ServiceEnabled: true,
	}}
	response, deployStatus := h.do(http.MethodPost, "/api/sub-publish/targets/"+strconv.FormatInt(targetID, 10)+"/deploy/inspect", nil)
	require.Equal(t, http.StatusOK, response.StatusCode, deployStatus)
	require.Equal(t, "installed", deployStatus["state"])
	require.Equal(t, "v0.2.0", deployStatus["version"])

	request, err := http.NewRequest(http.MethodGet, h.base+"/api/sub-publish/targets", nil)
	require.NoError(t, err)
	listResponse, err := h.client.Do(request)
	require.NoError(t, err)
	listBody, err := io.ReadAll(listResponse.Body)
	require.NoError(t, err)
	require.NoError(t, listResponse.Body.Close())
	require.Equal(t, http.StatusOK, listResponse.StatusCode, string(listBody))
	require.NotContains(t, string(listBody), secret)

	setupPath := "/api/sub-publish/targets/" + strconv.FormatInt(targetID, 10) + "/setup"
	response, viewed := h.do(http.MethodGet, setupPath, nil)
	require.Equal(t, http.StatusOK, response.StatusCode, viewed)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	require.Equal(t, secret, viewed["secret"])
	require.NotEmpty(t, viewed["authorized_keys_entry"])
	require.NotEmpty(t, viewed["sshd_config"])
	response, viewedAgain := h.do(http.MethodGet, setupPath, nil)
	require.Equal(t, http.StatusOK, response.StatusCode, viewedAgain)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	require.Equal(t, secret, viewedAgain["secret"])

	response, rotated := h.do(http.MethodPost, "/api/sub-publish/targets/"+strconv.FormatInt(targetID, 10)+"/rotate-secret", nil)
	require.Equal(t, http.StatusOK, response.StatusCode, rotated)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	require.NotEqual(t, secret, rotated["secret"])
	response, viewed = h.do(http.MethodGet, setupPath, nil)
	require.Equal(t, http.StatusOK, response.StatusCode, viewed)
	require.Equal(t, rotated["secret"], viewed["secret"])
}
