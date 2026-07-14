package nodes

import (
	"context"
	"testing"
)

// TestMineStage: a miner ignores the incoming payload and emits fresh stdout;
// an empty miner emits nothing; a nonzero exit fails the line.
func TestMineStage(t *testing.T) {
	o := mineStage(context.Background(), "printf fresh", "stale belt content")
	if o.payload != "fresh" || o.err != "" {
		t.Fatalf("mine = %+v", o)
	}
	if o := mineStage(context.Background(), "", "stale"); o.payload != "" || o.err != "" {
		t.Fatalf("empty miner = %+v", o)
	}
	if o := mineStage(context.Background(), "exit 2", ""); o.err == "" {
		t.Fatal("nonzero exit must fail the line")
	}
}
