// Log — a timestamped entry: → glyph tinted by /color, a muted
// "(YYYY-MM-DD HH:MM)" creation-time prefix, and a muted " · description"
// tail. Set a node to this type via /type. The reference NodeMod: this file
// is what an lflow mod is expected to look like.
lflow.registerType({
    key: "log",
    label: "Log",
    inlineEditable: true,
    glyph: function (node) { return [ "→", node.color || "dim" ]; },
    baseColor: function (node) { return node.color || "dim"; },
    prefix: function (node) {
        return lflow.style("(" + lflow.time(node.addedOn) + ") ", "dim");
    },
    muteFrom: function (name) { return name.indexOf(" · "); },
});
