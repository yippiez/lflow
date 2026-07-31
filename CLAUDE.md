# CLAUDE.md

@AGENTS.md

After any code change, call the **verifier** subagent instead of running
build / test / e2e / ast-grep steps yourself — it owns that logic and its
memory of known flakes. Install the dev binary and commit only after it
reports PASS.
