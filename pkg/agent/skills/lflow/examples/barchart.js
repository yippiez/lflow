// Barchart — the node text is space-separated numbers ("4 8 15 16 23 42"); the
// node collapses to a sparkline, and alt+e opens a full interactive bar chart.
// Pattern: a `view` — the full inline expanded UI (an Elm loop). This is what
// makes a mod as rich as a built-in type: it draws its own bands, captures keys,
// holds state, and runs effects, all inline in the outline (never an alt-screen).
lflow.registerType({
    key: "barchart", label: "Barchart", sign: "▁ ", inlineEditable: true,
    glyph: function (node) { return ["▁", node.color || "cyan"]; },

    // collapsed one-liner: a sparkline of the numbers, right after the text.
    render: function (node, name) {
        var xs = nums(name);
        if (!xs.length) return name;
        return name + lflow.style("  " + spark(xs), "cyan");
    },

    // the expanded view (alt+e).
    view: {
        // per-node state; `hi` is the highlighted bar, restored from last time.
        init: function (node) {
            var saved = lflow.getData(node.uuid) || {};
            return { hi: saved.hi || 0 };
        },

        // draw a horizontal bar chart into a truecolor canvas — one row per value,
        // the highlighted row brightened. `ctx.width` is the usable inner width.
        render: function (node, s, ctx) {
            var xs = nums(node.name);
            if (!xs.length) return [lflow.style("  add numbers to the node text, e.g. 4 8 15 16", "dim")];
            var max = Math.max.apply(null, xs);
            var labelW = 6, barW = Math.max(4, ctx.width - labelW - 2);
            var cv = lflow.canvas(ctx.width, xs.length);
            for (var i = 0; i < xs.length; i++) {
                var label = lflow.text.pad(String(xs[i]), labelW, "right");
                for (var c = 0; c < labelW; c++) cv.set(c, i, label[c] || " ", "dim", "");
                var fill = Math.round(barW * xs[i] / max);
                var color = i === s.hi ? "#7fd4ff" : "#2b7fa6";
                for (var x = 0; x < fill; x++) cv.set(labelW + 1 + x, i, "█", color, "");
            }
            var bands = cv.bands();
            bands.push(lflow.style("  ↑↓ move · r reload · esc close", "dim"));
            return bands;
        },

        // keys mutate state (↑↓) or fire an effect (r → an async shell reload).
        key: function (node, s, ctx, k) {
            var n = nums(node.name).length;
            if (k === "up" || k === "k") return { state: { hi: (s.hi + n - 1) % n } };
            if (k === "down" || k === "j") return { state: { hi: (s.hi + 1) % n } };
            if (k === "r") return { state: s, effect: { kind: "exec", cmd: "echo reloaded" } };
            return false; // not ours → esc/ctrl+c still close the view
        },

        // fold an effect's result back into state (here just a demo passthrough).
        update: function (node, s, msg) { return { state: s }; },

        // persist the highlighted bar so re-opening resumes where you left.
        leave: function (node, s) { lflow.setData(node.uuid, { hi: s.hi }); },
    },
});

function nums(name) {
    return (name.match(/-?\d+(\.\d+)?/g) || []).map(Number);
}

function spark(xs) {
    var g = "▁▂▃▄▅▆▇█", max = Math.max.apply(null, xs) || 1, out = "";
    for (var i = 0; i < xs.length; i++) out += g[Math.min(7, Math.round(7 * xs[i] / max))];
    return out;
}
