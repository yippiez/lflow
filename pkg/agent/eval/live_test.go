package eval

import (
	"context"
	"os"
	"testing"
	"time"
)

// the exact over-structured worker deliverable the user complained about, as the
// indented outline text the eval receives.
const verboseDeliverable = `Medical File Selection — where we pick which file to process
  Selection happens at multiple layers in the processing pipeline. The primary decision chain is:
  1. Study ZIP download from RIS
    Packages/pipelines/src/pipelines/pipelines.py — task_process()
      Downloads the task's DICOM study ZIP from the RIS. The task already points to one study — no cross-study selection.
  2. Readable-MR series filter
    processing_dicom_is_readable_mr_series() keeps only MR series with uniform slice sizes. First hard filter.
  3. Sagittal series selection (the main "best file" pick)
    processing_dicom_select_sagittal_series() filters to scalar sagittal images, prefers T2, falls back to first sagittal.
  4. Axial series selection
    processing_dicom_select_axial_series() keeps 3D scalar axial T1/T2 series; returns all matching.
  5. Golden sagittal slice selection
    processing_dicom_get_golden_sagittal_slice() takes the middle slice of the chosen sagittal series.`

// TestRunOnceEchoLive is a free, deterministic plumbing smoke test against pi's
// echo backend: runOnce must return text without hanging. Gated behind
// LFLOW_EVAL_LIVE so the normal suite stays offline.
func TestRunOnceEchoLive(t *testing.T) {
	if os.Getenv("LFLOW_EVAL_LIVE") == "" {
		t.Skip("set LFLOW_EVAL_LIVE=1 to run live agent tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := runOnce(ctx, "echo/echo", "you are a test", "ping")
	t.Logf("echo runOnce → out=%q err=%v", out, err)
	if err != nil {
		t.Fatalf("echo plumbing failed: %v", err)
	}
}

// TestSingleNodeCondenseLive runs the REAL condense eval on the user's verbose
// deliverable with a cheap model ($LFLOW_EVAL_MODEL). It must collapse it to one
// node. Gated behind LFLOW_EVAL_LIVE (costs a cheap call).
func TestSingleNodeCondenseLive(t *testing.T) {
	if os.Getenv("LFLOW_EVAL_LIVE") == "" || os.Getenv("LFLOW_EVAL_MODEL") == "" {
		t.Skip("set LFLOW_EVAL_LIVE=1 and LFLOW_EVAL_MODEL=<cheap model> to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// task that should NOT warrant an outline — it's an explanatory question
	cond, col := singleNodeLive(ctx,
		"Where does picking the best medical file from available ones happen?",
		verboseDeliverable)
	t.Logf("collapse=%v\ncondensed=%q", col, cond)
	if !col {
		t.Error("expected the verbose multi-node deliverable to COLLAPSE to one node")
	}
	if col && len(cond) == 0 {
		t.Error("collapse with empty condensed text")
	}
}

// TestSingleNodeKeepsListLive guards against over-collapsing: when the task asks
// for a list and the answer IS a list of distinct items, the eval must KEEP it.
func TestSingleNodeKeepsListLive(t *testing.T) {
	if os.Getenv("LFLOW_EVAL_LIVE") == "" || os.Getenv("LFLOW_EVAL_MODEL") == "" {
		t.Skip("set LFLOW_EVAL_LIVE=1 and LFLOW_EVAL_MODEL=<cheap model> to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	list := "Buy milk\nCall the dentist\nFinish the report\nWater the plants"
	cond, col := singleNodeLive(ctx, "Give me a todo list of 4 separate tasks", list)
	t.Logf("collapse=%v condensed=%q", col, cond)
	if col {
		t.Error("an explicit list of distinct items should be KEPT, not collapsed")
	}
}

// singleNodeLive mirrors SingleNode but takes the model from $LFLOW_EVAL_MODEL
// directly (so the test pins the model regardless of a worker model).
func singleNodeLive(ctx context.Context, task, deliverable string) (string, bool) {
	model := os.Getenv("LFLOW_EVAL_MODEL")
	out, err := runner(ctx, model, singleNodeSystem, singleNodePrompt(task, deliverable))
	if err != nil {
		return "", false
	}
	return parseSingleNode(out)
}
