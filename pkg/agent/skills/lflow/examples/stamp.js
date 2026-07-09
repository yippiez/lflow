// Stamp — a bullet that wears its creation time on its sleeve.
// Pattern: a prefix-only decoration; the node stays a plain editable line.
lflow.registerType({
    key: "stamp", label: "Stamp", inlineEditable: true,
    glyph: function (node) { return ["◷", node.color || "cyan"]; },
    prefix: function (node) {
        return lflow.style("(" + lflow.time(node.addedOn) + ") ", "dim");
    },
});
