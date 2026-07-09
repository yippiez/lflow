// Countdown — the node text starts with a YYYY-MM-DD date; the body renders
// the days remaining. Pattern: the render hook — full control of the body.
lflow.registerType({
    key: "countdown", label: "Countdown", sign: "⧗ ", inlineEditable: true,
    glyph: function (node) { return ["⧗", node.color || "purple"]; },
    render: function (node, name) {
        var m = name.match(/(\d{4})-(\d{2})-(\d{2})/);
        if (!m) return name;
        var due = new Date(+m[1], +m[2] - 1, +m[3]);
        var days = Math.ceil((due - new Date()) / 86400000);
        var tail = days < 0 ? Math.abs(days) + "d overdue"
                 : days === 0 ? "today"
                 : days + "d left";
        return name + lflow.style("  · " + tail, days <= 1 ? "red" : "dim");
    },
});
