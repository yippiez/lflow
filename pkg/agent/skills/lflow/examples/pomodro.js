// Pomodoro — a 25-minute focus timer that owns its subtree. Pattern: a node
// reaching DOWN into its own children. alt+r runs a 25-min countdown that
// streams beneath the node; when the timer ends it reads its subtree with
// `lflow node list <uuid> --format json` and locks every node in it (itself
// AND its children) with `lflow node edit --readonly`, so the finished
// session can't be edited. The glyph/render read node.children to advertise
// how much the timer guards before it ever fires.
lflow.registerType({
    key: "pomodro",
    label: "Pomodoro",
    sign: "◔ ",
    inlineEditable: true,
    glyph: function (node) { return ["◔", node.color || "red"]; },
    render: function (node, name) {
        var kids = node.children;
        var tail = kids > 0
            ? "  · 25:00 focus, locks " + kids + (kids === 1 ? " child" : " children") + " when done"
            : "  · 25:00 focus, alt+r to start";
        return name + lflow.style(tail, "dim");
    },
    // alt+r: count down 25 minutes (one streamed line a minute), then walk the
    // subtree via the CLI and lock self + children. A mod accesses its children
    // through the lflow CLI — `list --format json` emits one "uuid" per node in
    // the subtree (the root included), so locking every one locks itself too.
    run: function (node) {
        var id = node.uuid;
        return [
            'id="' + id + '"',
            'mins=25',
            'echo "pomodoro started — ${mins}:00 focus on this subtree"',
            'for ((i=mins; i>0; i--)); do',
            '  printf "focus · %d min left\\n" "$i"',
            '  sleep 60',
            'done',
            'echo "time — locking this node and its children"',
            'lflow node list "$id" --format json \\',
            '  | grep -oE \'"uuid": *"[^"]+"\' \\',
            '  | cut -d\'"\' -f4 \\',
            '  | while read -r n; do lflow node edit "$n" --readonly && echo "locked $n"; done',
            'echo "pomodoro complete — subtree is read-only"',
        ].join("\n");
    },
});
