// Pill chip — a custom chip KIND (not a node type): a ◷-marked stamp chip.
// Pattern: registerChip — display is the compact inline form, expand is what
// bash commands and search see when the chip is dereferenced.
lflow.registerChip({
    key: "stamp", marker: "◷", color: "cyan",
    display: function (value) { return "◷ " + value; },
    expand: function (value) { return value; },
});
