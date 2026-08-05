// Package arch pins the repo's dependency contract: each packages/* directory
// has a layer, and imports may only point downward (same layer is allowed for
// siblings that do not import each other transitively — Go's own cycle check
// covers the rest). Violations that predate the contract are grandfathered
// explicitly below so they cannot silently multiply; shrink that list, never
// grow it.
package arch

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/lflow/lflow/"

// layer assigns each packages/ subtree a level. Lower may never import higher.
//
//	0: leaf vocabularies and clients — no repo-internal imports at all
//	   (mobile is one: the daemon serves it, it knows nothing of the daemon)
//	1: database (the schema layer)
//	2: daemon (owns the DB), nlp, app (the process runtime), outline and
//	   fileeditor (scriptable DB-subtree renderers/codecs, database only)
//	3: editor, nodes (the editor's plugin registrations)
//	4: cli, cmd (the process shell)
var layers = map[string]int{
	"packages/utils":        0,
	"packages/chips":        0,
	"packages/mobile":       0, // the embedded web client: assets and an http.Handler, no repo imports
	"packages/database":     1,
	"packages/integrations": 1, // zotero half uses database.Open on a foreign sqlite file (debt: should be a leaf)
	"packages/daemon":       2,
	"packages/nlp":          2,
	"packages/app":          2,
	"packages/outline":      2,
	"packages/fileeditor":    3,
	"packages/editor":       3,
	"packages/nodes":        3,
	"packages/cli":          4,
	"cmd":                   4,
}

// grandfathered lists the upward imports that exist today. Removing an entry
// after fixing the debt is the intended direction; adding one needs a reason
// as good as the ones below.
var grandfathered = map[string]bool{}

func layerOf(pkg string) (string, int, bool) {
	rel := strings.TrimPrefix(pkg, module)
	for prefix, l := range layers {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return prefix, l, true
		}
	}
	return "", 0, false
}

func TestLayering(t *testing.T) {
	out, err := exec.Command("go", "list", "-f",
		`{{.ImportPath}} {{join .Imports " "}}`, "../../...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		from := fields[0]
		fromPrefix, fromLayer, ok := layerOf(from)
		if !ok {
			continue // tests/, rule-tests/ — outside the contract
		}
		for _, imp := range fields[1:] {
			if !strings.HasPrefix(imp, module) {
				continue
			}
			toPrefix, toLayer, ok := layerOf(imp)
			if !ok || toPrefix == fromPrefix {
				continue
			}
			if toLayer > fromLayer {
				edge := fmt.Sprintf("%s -> %s", fromPrefix, toPrefix)
				if grandfathered[edge] {
					seen[edge] = true
					continue
				}
				t.Errorf("upward import: %s (layer %d) imports %s (layer %d)\n  %s -> %s",
					fromPrefix, fromLayer, toPrefix, toLayer, from, imp)
			}
		}
	}
	for edge := range grandfathered {
		if !seen[edge] {
			t.Errorf("grandfathered edge %q no longer exists — delete it from the list", edge)
		}
	}
}
