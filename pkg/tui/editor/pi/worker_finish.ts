/** lflow worker finish tool — exposed only to worker subprocesses. */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

// The deliverable is an OUTLINE, not markdown. Each node has plain text (no
// markdown markup), an optional note, and optional child nodes. children is left
// loosely typed (this typebox build has no Type.Recursive); the parent validates
// the nested shape — the description tells the model the recursive structure.
const OutlineNode = Type.Object({
	text: Type.String({ description: "Plain text for this node. No markdown, no bullets, no '#'." }),
	note: Type.Optional(
		Type.String({
			description:
				"Optional elaboration shown under the node. PREFER putting detail here over " +
				"creating child nodes — a multi-part explanation is one node with a rich note.",
		}),
	),
	type: Type.Optional(
		Type.String({
			description:
				"Optional node format: bullet (default), todo, heading, code, quote, bash, json. " +
				"Use 'bash' when the text is a shell command, 'code' for code, 'todo' for a task, etc.",
		}),
	),
	children: Type.Optional(
		Type.Array(Type.Any(), {
			description: "Optional nested child nodes, each shaped { text, note?, type?, children? }.",
		}),
	),
});

export default function LflowWorkerFinish(pi: ExtensionAPI) {
	pi.registerTool({
		name: "finish_worker",
		label: "Finish Worker",
		description: "Submit the final deliverable to the parent outline as outline nodes (NOT markdown). One call only.",
		promptSnippet: "finish_worker: deliver the answer as ONE node (detail in its note) unless the user asked for a list",
		promptGuidelines: [
			"Call finish_worker exactly once when done. nodes is the answer itself, not a tool-by-tool log.",
			"DEFAULT TO ONE node. An explanation — even a long, multi-finding one — is ONE node; put the detail " +
				"in that node's note, NOT in child nodes.",
			"Use more than one node ONLY when the user literally asked for a list/steps/outline, or the answer " +
				"is genuinely an enumeration of distinct items. When in doubt, one node.",
			"Plain text only — never markdown, bullets, or headings.",
			"Set type per node when it fits: 'bash' for a shell command, 'code' for code, 'todo' for a task.",
			"After finish_worker, assistant text must be only WORKER_DONE.",
		],
		parameters: Type.Object({
			status: Type.Optional(Type.Union([Type.Literal("done"), Type.Literal("needs_input"), Type.Literal("failed")])),
			nodes: Type.Array(OutlineNode, {
				description:
					"The deliverable as outline nodes. STRONGLY PREFER a single node (length 1) with the detail " +
					"in its note. Use multiple nodes only for a list the user explicitly asked for. No markdown.",
			}),
		}),
		async execute(_id: string, params: { status?: string; nodes?: unknown[] }) {
			const status = params.status ?? "done";
			const nodes = Array.isArray(params.nodes) ? params.nodes : [];
			if (nodes.length === 0) {
				return { content: [{ type: "text", text: "nodes empty" }], details: { status, nodes: [] }, isError: true };
			}
			return { content: [{ type: "text", text: `Captured ${nodes.length} node(s).` }], details: { status, nodes } };
		},
	});
}
