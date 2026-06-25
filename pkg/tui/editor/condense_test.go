package editor

import "testing"

func TestOverStructuredDeliverable(t *testing.T) {
	cases := []struct {
		name  string
		nodes []deliverNode
		want  bool
	}{
		{"single flat node", []deliverNode{{Text: "one answer"}}, false},
		{"empty", nil, false},
		{"two nodes", []deliverNode{{Text: "a"}, {Text: "b"}}, true},
		{"one node with children", []deliverNode{{Text: "a", Children: []deliverNode{{Text: "c"}}}}, true},
	}
	for _, c := range cases {
		if got := overStructuredDeliverable(c.nodes); got != c.want {
			t.Errorf("%s: overStructuredDeliverable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSingleDeliverableJSONRoundTrips(t *testing.T) {
	js := singleDeliverableJSON("the condensed answer")
	nodes := parseDeliverNodes(js)
	if len(nodes) != 1 || nodes[0].Text != "the condensed answer" || len(nodes[0].Children) != 0 {
		t.Fatalf("round-trip failed: %q -> %+v", js, nodes)
	}
}
