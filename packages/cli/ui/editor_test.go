package ui

import (
	"fmt"
	"os"
	"testing"

	"github.com/lflow/lflow/packages/app"
	"github.com/lflow/lflow/packages/utils/assert"
	"github.com/pkg/errors"
)

func TestGetTmpContentPath(t *testing.T) {
	t.Run("no collision", func(t *testing.T) {
		ctx := app.InitTestCtx(t)

		res, err := GetTmpContentPath(ctx.Paths.Cache)
		if err != nil {
			t.Fatal(errors.Wrap(err, "executing"))
		}

		expected := fmt.Sprintf("%s/%s", ctx.Paths.Cache, "LFLOW_TMPCONTENT_0.md")
		assert.Equal(t, res, expected, "filename did not match")
	})

	t.Run("one existing session", func(t *testing.T) {
		// set up
		ctx := app.InitTestCtx(t)

		p := fmt.Sprintf("%s/%s", ctx.Paths.Cache, "LFLOW_TMPCONTENT_0.md")
		if _, err := os.Create(p); err != nil {
			t.Fatal(errors.Wrap(err, "preparing the conflicting file"))
		}

		// execute
		res, err := GetTmpContentPath(ctx.Paths.Cache)
		if err != nil {
			t.Fatal(errors.Wrap(err, "executing"))
		}

		// test
		expected := fmt.Sprintf("%s/%s", ctx.Paths.Cache, "LFLOW_TMPCONTENT_1.md")
		assert.Equal(t, res, expected, "filename did not match")
	})

	t.Run("two existing sessions", func(t *testing.T) {
		// set up
		ctx := app.InitTestCtx(t)

		p1 := fmt.Sprintf("%s/%s", ctx.Paths.Cache, "LFLOW_TMPCONTENT_0.md")
		if _, err := os.Create(p1); err != nil {
			t.Fatal(errors.Wrap(err, "preparing the conflicting file"))
		}
		p2 := fmt.Sprintf("%s/%s", ctx.Paths.Cache, "LFLOW_TMPCONTENT_1.md")
		if _, err := os.Create(p2); err != nil {
			t.Fatal(errors.Wrap(err, "preparing the conflicting file"))
		}

		// execute
		res, err := GetTmpContentPath(ctx.Paths.Cache)
		if err != nil {
			t.Fatal(errors.Wrap(err, "executing"))
		}

		// test
		expected := fmt.Sprintf("%s/%s", ctx.Paths.Cache, "LFLOW_TMPCONTENT_2.md")
		assert.Equal(t, res, expected, "filename did not match")
	})
}
