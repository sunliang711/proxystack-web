package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTaskCenterAPI(t *testing.T) {
	guest, _ := newHarness(t)
	guest.mustDo(http.MethodGet, "/api/task-center", nil, http.StatusUnauthorized)
	guest.mustDo(http.MethodGet, "/api/task-center/task/1", nil, http.StatusUnauthorized)
	h := newAdminHarness(t)
	body := h.mustDo(http.MethodGet, "/api/task-center", nil, http.StatusOK)
	require.NotNil(t, body["items"])
	require.EqualValues(t, 0, body["running"])
	for _, query := range []string{"source=invalid", "status=invalid", "kind=invalid", "limit=0", "limit=201", "offset=-1"} {
		h.mustDo(http.MethodGet, "/api/task-center?"+query, nil, http.StatusUnprocessableEntity)
	}
	h.mustDo(http.MethodGet, "/api/task-center?kind=egress_check", nil, http.StatusOK)
	h.mustDo(http.MethodGet, "/api/task-center/job/9999", nil, http.StatusNotFound)
	h.mustDo(http.MethodGet, "/api/task-center/task/9999", nil, http.StatusNotFound)
	h.mustDo(http.MethodGet, "/api/task-center/job/0", nil, http.StatusUnprocessableEntity)
	h.mustDo(http.MethodGet, "/api/task-center/invalid/1", nil, http.StatusUnprocessableEntity)
}
