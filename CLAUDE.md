# CLAUDE.md

@AGENTS.md

After any code change, call the **verifier** subagent instead of running
build / test / e2e / ast-grep or demo-recording steps yourself — it owns that
logic and its memory of known flakes, and on PASS it records the DEMO.md video
of a visible change and reports the mp4 path — send that file to the user
yourself (subagents cannot). Install the dev binary and commit only after it
reports PASS.
