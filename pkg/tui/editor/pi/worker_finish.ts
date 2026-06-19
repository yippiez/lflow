/** lflow worker finish tool — exposed only to worker subprocesses. */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

// The deliverable is an OUTLINE, not markdown. Each node has plain text (no
// markdown markup), an optional note, and optional child nodes. children is left
// loosely typed (this typebox build has no Type.Recursive); the parent validates
// the nested shape — the description tells the model the recursive structure.
const OutlineNode = Type.Object({
	text: Type.String({ description: "Plain text for this node. No markdown, no bullets, no '#'." }),
	note: Type.Optional(Type.String({ description: "Optional secondary text shown under the node." })),
	children: Type.Optional(
		Type.Array(Type.Any(), {
			description: "Optional nested child nodes, each shaped { text, note?, children? }.",
		}),
	),
});

export default function LflowWorkerFinish(pi: ExtensionAPI) {
	pi.registerTool({
		name: "finish_worker",
		label: "Finish Worker",
		description: "Submit the final deliverable to the parent outline as outline nodes (NOT markdown). One call only.",
		promptSnippet: "finish_worker: deliver the answer as outline nodes — ONE node unless asked for a list",
		promptGuidelines: [
			"Call finish_worker exactly once when done. nodes is the answer itself, not a tool-by-tool log.",
			"Return a SINGLE node unless the user explicitly asked for a list/steps/outline.",
			"Plain text only — never markdown, bullets, or headings. Nesting is expressed with children, not text.",
			"After finish_worker, assistant text must be only WORKER_DONE.",
		],
		parameters: Type.Object({
			status: Type.Optional(Type.Union([Type.Literal("done"), Type.Literal("needs_input"), Type.Literal("failed")])),
			nodes: Type.Array(OutlineNode, {
				description: "The deliverable as outline nodes. ONE node unless the user asked for a list. No markdown.",
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
