package controllers

import (
	"net/http"
	"testing"

	"github.com/lflow/lflow/pkg/server/app"
	"github.com/lflow/lflow/pkg/server/testutils"
	"github.com/lflow/lflow/pkg/shared/assert"
	"github.com/lflow/lflow/pkg/shared/clock"
)

func TestNotSupportedVersions(t *testing.T) {
	testCases := []struct {
		path string
	}{
		// v1
		{
			path: "/api/v1",
		},
		{
			path: "/api/v1/foo",
		},
		{
			path: "/api/v1/bar/baz",
		},
		// v2
		{
			path: "/api/v2",
		},
		{
			path: "/api/v2/foo",
		},
		{
			path: "/api/v2/bar/baz",
		},
	}

	// setup
	db := testutils.InitMemoryDB(t)
	a := app.NewTest()
	a.Clock = clock.NewMock()
	a.DB = db
	server := MustNewServer(t, &a)
	defer server.Close()

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			// execute
			req := testutils.MakeReq(server.URL, "GET", tc.path, "")
			res := testutils.HTTPDo(t, req)

			// test
			assert.Equal(t, res.StatusCode, http.StatusGone, "status code mismatch")
		})
	}
}
