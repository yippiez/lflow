// Dice — a roll-on-demand node: the text is the die size, alt+r rolls it.
// Pattern: the run hook — return a shell command, its output streams beneath
// the node (ephemeral, never persisted).
lflow.registerType({
    key: "dice", label: "Dice", sign: "⚂ ", inlineEditable: true,
    glyph: function (node) { return ["⚂", node.color || "yellow"]; },
    run: function (node) {
        var n = parseInt(node.name, 10) || 6;
        return "echo rolled $(( (RANDOM % " + n + ") + 1 )) of " + n;
    },
});
