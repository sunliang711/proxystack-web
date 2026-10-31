package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"proxystack-web/internal/version"
)

func TestSystemInfoUsesResolvedVersion(t *testing.T) {
	h := newAdminHarness(t)
	body := h.mustDo(http.MethodGet, "/api/system/info", nil, http.StatusOK)
	build := version.Current()
	require.Equal(t, build.Version, body["version"])
	require.Equal(t, build.Commit, body["commit"])
	require.Equal(t, build.Date, body["build_date"])
}
