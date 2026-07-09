// Priority — the glyph is driven by the node's content: a leading p1/p2/p3
// token colors it red/yellow/green. Pattern: a content-aware glyph.
lflow.registerType({
    key: "priority", label: "Priority", inlineEditable: true,
    glyph: function (node) {
        var m = node.name.match(/^p([123])\b/);
        var color = m ? {1: "red", 2: "yellow", 3: "green"}[m[1]] : "dim";
        return ["!", color];
    },
});
