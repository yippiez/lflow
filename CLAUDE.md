# CLAUDE.md — Claude Code entry point for lflow

@AGENTS.md

## Verification

Verification is delegated, not inlined: after any code change, call the
**verifier** subagent (defined in `.claude/agents/verifier.md`) and let it run
the build / unit-test / e2e / ast-grep pass. Do not reproduce those steps
yourself — the subagent owns the verification logic and keeps its own project
memory in `.claude/agent-memory/verifier/` (known flakes, environment quirks),
so calling it is both the complete and the informed check. Install the dev
binary and commit only after the verifier reports PASS.
