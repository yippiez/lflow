// Weather — the node text is a location; alt+r fetches a one-line report.
// Pattern: a run hook that uses the node's text as its input.
lflow.registerType({
    key: "weather", label: "Weather", sign: "☂ ", inlineEditable: true,
    glyph: function (node) { return ["☂", node.color || "cyan"]; },
    run: function (node) {
        return "curl -s 'wttr.in/" + encodeURIComponent(node.name) + "?format=3'";
    },
});
