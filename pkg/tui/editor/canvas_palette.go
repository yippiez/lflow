package editor

// The canvas palette catalog: every entry is a NAMED glyph (or color) the
// painter can search — "branch curved", "geometry square", "background blue",
// "half block". Categories group the unicode drawing ranges; names stay
// lowercase so search is case-free.

var canvasPalette []canvasPaletteEntry

// palAdd registers ch/name pairs under one category.
func palAdd(cat string, pairs ...string) {
	for i := 0; i+1 < len(pairs); i += 2 {
		canvasPalette = append(canvasPalette, canvasPaletteEntry{cat: cat, ch: pairs[i], name: pairs[i+1]})
	}
}

func init() {
	palAdd("line",
		"─", "light horizontal", "━", "heavy horizontal", "│", "light vertical", "┃", "heavy vertical",
		"┄", "dashed horizontal", "┅", "heavy dashed horizontal", "┆", "dashed vertical", "┇", "heavy dashed vertical",
		"┈", "dotted horizontal", "┉", "heavy dotted horizontal", "┊", "dotted vertical", "┋", "heavy dotted vertical",
		"╌", "double dash horizontal", "╍", "heavy double dash horizontal", "╎", "double dash vertical", "╏", "heavy double dash vertical",
		"═", "double horizontal", "║", "double vertical",
		"╱", "diagonal rising", "╲", "diagonal falling", "╳", "diagonal cross",
		"╴", "half line left", "╵", "half line up", "╶", "half line right", "╷", "half line down",
	)
	palAdd("corner",
		"┌", "down and right", "┐", "down and left", "└", "up and right", "┘", "up and left",
		"┏", "heavy down and right", "┓", "heavy down and left", "┗", "heavy up and right", "┛", "heavy up and left",
		"╔", "double down and right", "╗", "double down and left", "╚", "double up and right", "╝", "double up and left",
		"╒", "double-h down and right", "╕", "double-h down and left", "╘", "double-h up and right", "╛", "double-h up and left",
		"╓", "double-v down and right", "╖", "double-v down and left", "╙", "double-v up and right", "╜", "double-v up and left",
	)
	palAdd("curved",
		"╭", "arc down and right", "╮", "arc down and left", "╰", "arc up and right", "╯", "arc up and left",
		"◜", "arc upper left", "◝", "arc upper right", "◞", "arc lower right", "◟", "arc lower left",
		"◠", "arc upper half", "◡", "arc lower half",
	)
	palAdd("branch",
		"├", "tee right", "┤", "tee left", "┬", "tee down", "┴", "tee up", "┼", "cross",
		"┣", "heavy tee right", "┫", "heavy tee left", "┳", "heavy tee down", "┻", "heavy tee up", "╋", "heavy cross",
		"╠", "double tee right", "╣", "double tee left", "╦", "double tee down", "╩", "double tee up", "╬", "double cross",
		"┝", "tee right heavy arm", "┥", "tee left heavy arm", "┯", "tee down heavy bar", "┷", "tee up heavy bar",
		"┿", "cross heavy bar", "╂", "cross heavy pole",
		"╞", "double tee right single", "╡", "double tee left single", "╤", "double tee down single", "╧", "double tee up single", "╪", "cross double bar", "╫", "cross double pole",
	)
	palAdd("block",
		"█", "full block", "▉", "seven eighths", "▊", "three quarters", "▋", "five eighths",
		"▌", "half block left", "▍", "three eighths", "▎", "quarter", "▏", "eighth",
		"▐", "half block right", "▀", "half block upper", "▄", "half block lower",
		"▁", "lower eighth", "▂", "lower quarter", "▃", "lower three eighths", "▅", "lower five eighths",
		"▆", "lower three quarters", "▇", "lower seven eighths", "▔", "upper eighth", "▕", "right eighth",
	)
	palAdd("quadrant",
		"▖", "lower left", "▗", "lower right", "▘", "upper left", "▝", "upper right",
		"▙", "left and lower right", "▛", "left and upper right", "▜", "right and upper left", "▟", "right and lower left",
		"▚", "upper left lower right", "▞", "upper right lower left",
	)
	palAdd("shade",
		"░", "light shade", "▒", "medium shade", "▓", "dark shade",
	)
	palAdd("geometry",
		"■", "square filled", "□", "square", "▢", "square rounded", "▣", "square contains square",
		"▤", "square horizontal fill", "▥", "square vertical fill", "▦", "square grid fill", "▧", "square diagonal fill",
		"▨", "square reverse diagonal", "▩", "square crosshatch", "▪", "square small filled", "▫", "square small",
		"▬", "rectangle filled", "▭", "rectangle", "▮", "vertical rectangle filled", "▯", "vertical rectangle",
		"▰", "parallelogram filled", "▱", "parallelogram",
		"▲", "triangle up filled", "△", "triangle up", "▴", "triangle up small", "▵", "triangle up small hollow",
		"▶", "triangle right filled", "▷", "triangle right", "▸", "triangle right small", "▹", "triangle right small hollow",
		"▼", "triangle down filled", "▽", "triangle down", "▾", "triangle down small", "▿", "triangle down small hollow",
		"◀", "triangle left filled", "◁", "triangle left", "◂", "triangle left small", "◃", "triangle left small hollow",
		"◆", "diamond filled", "◇", "diamond", "◈", "diamond contains diamond", "◊", "lozenge",
		"●", "circle filled", "○", "circle", "◉", "circle fisheye", "◎", "circle bullseye",
		"◌", "circle dotted", "◍", "circle vertical fill", "◦", "circle small", "◯", "circle large",
		"◐", "circle half left", "◑", "circle half right", "◒", "circle half lower", "◓", "circle half upper",
		"◔", "circle quarter", "◕", "circle three quarters",
		"◖", "half circle left", "◗", "half circle right",
		"◢", "triangle corner lower right", "◣", "triangle corner lower left", "◤", "triangle corner upper left", "◥", "triangle corner upper right",
		"◧", "square half left", "◨", "square half right", "◩", "square diagonal upper left", "◪", "square diagonal lower right", "◫", "square divided vertical",
		"◬", "triangle dotted", "◭", "triangle left half black", "◮", "triangle right half black",
	)
	palAdd("arrow",
		"←", "left", "↑", "up", "→", "right", "↓", "down",
		"↔", "left right", "↕", "up down", "↖", "up left", "↗", "up right", "↘", "down right", "↙", "down left",
		"↚", "left stroked", "↛", "right stroked", "↜", "left wave", "↝", "right wave",
		"↞", "left double head", "↟", "up double head", "↠", "right double head", "↡", "down double head",
		"↤", "left from bar", "↥", "up from bar", "↦", "right from bar", "↧", "down from bar",
		"↩", "left hook", "↪", "right hook", "↫", "left loop", "↬", "right loop", "↭", "left right wave",
		"↰", "up then left", "↱", "up then right", "↲", "down then left", "↳", "down then right", "↴", "right then down", "↵", "return",
		"⇐", "double left", "⇑", "double up", "⇒", "double right", "⇓", "double down", "⇔", "double left right", "⇕", "double up down",
		"⇠", "dashed left", "⇡", "dashed up", "⇢", "dashed right", "⇣", "dashed down",
		"⇦", "white left", "⇧", "white up", "⇨", "white right", "⇩", "white down",
		"➔", "heavy right", "➜", "heavy round right", "➤", "pointer right", "➨", "heavy concave right",
	)
	palAdd("star",
		"★", "star filled", "☆", "star", "✦", "sparkle filled", "✧", "sparkle",
		"✱", "heavy asterisk", "✳", "eight spoked asterisk", "✴", "eight pointed star", "✵", "pinwheel star",
		"✶", "six pointed star", "✷", "eight pointed pinwheel", "✸", "heavy eight pointed star", "✹", "twelve pointed star",
		"✺", "sixteen pointed star", "❋", "heavy florette",
		"•", "bullet", "∙", "bullet operator", "·", "middle dot", "⋅", "dot operator", "°", "degree",
	)
	palAdd("math",
		"±", "plus minus", "×", "multiply", "÷", "divide", "≈", "approximately", "≠", "not equal",
		"≤", "less or equal", "≥", "greater or equal", "∞", "infinity", "∑", "sum", "∏", "product",
		"√", "square root", "∫", "integral", "Δ", "delta", "∇", "nabla", "∂", "partial",
		"∈", "element of", "∉", "not element of", "∩", "intersection", "∪", "union",
		"⊂", "subset", "⊃", "superset", "⊕", "circled plus", "⊗", "circled times", "⊥", "perpendicular",
		"∅", "empty set", "∥", "parallel", "∠", "angle", "∴", "therefore", "μ", "mu", "λ", "lambda", "π", "pi", "Ω", "omega",
	)
	palAdd("misc",
		" ", "space blank eraser", "…", "ellipsis", "§", "section", "¶", "pilcrow", "†", "dagger", "‡", "double dagger",
		"✓", "check mark", "✗", "ballot x", "☐", "ballot box", "☑", "ballot box checked", "☒", "ballot box x",
		"♠", "spade", "♣", "club", "♥", "heart", "♦", "diamond suit",
		"♩", "quarter note", "♪", "eighth note", "♫", "beamed notes", "♻", "recycle",
		"☰", "trigram heaven", "☱", "trigram lake", "☲", "trigram fire", "☳", "trigram thunder",
		"☴", "trigram wind", "☵", "trigram water", "☶", "trigram mountain", "☷", "trigram earth",
		"⚀", "die one", "⚁", "die two", "⚂", "die three", "⚃", "die four", "⚄", "die five", "⚅", "die six",
	)
	// colors: background stamps a colored space (or tints the brush glyph);
	// foreground colors the brush glyph. "none" resets.
	for _, c := range []string{"red", "orange", "yellow", "green", "cyan", "blue", "purple", "gray"} {
		canvasPalette = append(canvasPalette,
			canvasPaletteEntry{cat: "background", name: c, color: c},
			canvasPaletteEntry{cat: "foreground", name: c, color: c},
		)
	}
	canvasPalette = append(canvasPalette,
		canvasPaletteEntry{cat: "background", name: "none · clear", color: "none"},
		canvasPaletteEntry{cat: "foreground", name: "default · clear", color: ""},
	)
}
