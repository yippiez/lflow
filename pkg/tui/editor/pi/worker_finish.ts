/** lflow worker finish tool — exposed only to worker subprocesses. */
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { Type } from "typebox";

export default function LflowWorkerFinish(pi: ExtensionAPI) {
	pi.registerTool({
		name: "finish_worker",
		label: "Finish Worker",
		description: "Submit the final deliverable for the parent outline. One call only — no process recap.",
		promptSnippet: "finish_worker: deliver the answer in markdown, nothing else",
		promptGuidelines: [
			"Call finish_worker exactly once when done. markdown is the answer itself, not a tool-by-tool log.",
			"After finish_worker, assistant text must be only WORKER_DONE.",
		],
		parameters: Type.Object({
			status: Type.Optional(Type.Union([Type.Literal("done"), Type.Literal("needs_input"), Type.Literal("failed")])),
			markdown: Type.String({ description: "The deliverable inserted into the outline. Not a recap of steps." }),
		}),
		async execute(_id: string, params: { status?: string; markdown?: string }) {
			const status = params.status ?? "done";
			const markdown = (params.markdown ?? "").trim();
			if (!markdown) {
				return { content: [{ type: "text", text: "markdown empty" }], details: { status, markdown: "" }, isError: true };
			}
			return { content: [{ type: "text", text: `Captured (${markdown.length} chars).` }], details: { status, markdown } };
		},
	});
}
