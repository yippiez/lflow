package controllers

import (
	"net/http"
	"testing"

	"github.com/lflow/lflow/pkg/assert"
	"github.com/lflow/lflow/pkg/server/app"
	"github.com/lflow/lflow/pkg/server/testutils"
)

func TestHealth(t *testing.T) {
	db := testutils.InitMemoryDB(t)

	a := app.NewTest()
	a.DB = db
	server := MustNewServer(t, &a)
	defer server.Close()

	// Execute
	req := testutils.MakeReq(server.URL, "GET", "/health", "")
	res := testutils.HTTPDo(t, req)

	// Test
	assert.StatusCodeEquals(t, res, http.StatusOK, "Status code mismtach")
}
