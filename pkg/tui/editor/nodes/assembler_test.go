package nodes

import (
	"context"
	"testing"
)

// TestAssembleStage: an assembler pipes the payload through its command; an
// empty assembler is a bare belt segment; a nonzero exit fails the line.
func TestAssembleStage(t *testing.T) {
	o := assembleStage(context.Background(), "tr a-z A-Z", "gear")
	if o.payload != "GEAR" || o.err != "" {
		t.Fatalf("assemble = %+v", o)
	}
	if o := assembleStage(context.Background(), "", "gear"); o.payload != "gear" {
		t.Fatalf("bare belt = %+v", o)
	}
	if o := assembleStage(context.Background(), "exit 1", "gear"); o.err == "" {
		t.Fatal("nonzero exit must fail the line")
	}
}
