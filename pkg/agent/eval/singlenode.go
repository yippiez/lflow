package eval

import (
	"context"
	"strings"
)

const singleNodeSystem = "You review a worker's outline answer for a notes app. A SINGLE outline node is " +
	"strongly preferred. Multiple or nested nodes are justified ONLY when the task literally asked for a list, " +
	"steps, or an outline, or the answer is genuinely an enumeration of distinct items (e.g. several files, " +
	"several options). Explanatory prose — even detailed, multi-part prose with several findings — must be ONE " +
	"node, with the detail in its text. Be strict and terse."

func singleNodePrompt(task, deliverable string) string {
	return "TASK:\n" + task + "\n\nWORKER ANSWER (outline, indented by depth):\n" + deliverable +
		"\n\nReply on the FIRST line with exactly KEEP or COLLAPSE.\n" +
		"KEEP = the multiple/nested nodes are genuinely justified.\n" +
		"COLLAPSE = it should be one node; then, on the following lines, give the condensed single-node answer " +
		"as plain text (no markdown, no bullets, no headings). Fold any detail into that text."
}

// SingleNode judges whether a multi-node worker deliverable should collapse to a
// single node, given the task. It returns (condensed, true) to collapse, or
// ("", false) to keep the original — including on any error or when no eval model
// is configured for workerModel. Never blocks the worker beyond its own call.
func SingleNode(ctx context.Context, workerModel, task, deliverable string) (condensed string, collapse bool) {
	model := ModelFor(workerModel)
	if model == "" {
		return "", false
	}
	out, err := runner(ctx, model, singleNodeSystem, singleNodePrompt(task, deliverable))
	if err != nil {
		return "", false
	}
	return parseSingleNode(out)
}

// parseSingleNode reads the eval response: a leading COLLAPSE keeps the remaining
// lines as the condensed node; anything else (KEEP / empty / unparseable) keeps
// the original deliverable.
func parseSingleNode(out string) (condensed string, collapse bool) {
	out = strings.TrimSpace(out)
	if out == "" {
		return "", false
	}
	first, rest := out, ""
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		first, rest = out[:i], out[i+1:]
	}
	if !strings.EqualFold(strings.TrimSpace(first), "COLLAPSE") {
		return "", false
	}
	if rest = strings.TrimSpace(rest); rest == "" {
		return "", false // collapse with no body → nothing to collapse to, keep
	}
	return rest, true
}
