package utils

// Pure caret math over a multiline rune buffer: rune-indexed caret ⇄
// line/column conversions and vertical movement. Shared by every multiline
// node view (json, code, …) and the node plugin helpers.

// CaretLineCol returns the zero-based line and column of a rune-index caret
// in s.
func CaretLineCol(s string, caret int) (line, col int) {
	r := []rune(s)
	if caret > len(r) {
		caret = len(r)
	}
	for i := 0; i < caret; i++ {
		if r[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return
}

// CaretAt returns the rune-index caret sitting at line/col in s, clamped to
// the line's end.
func CaretAt(s string, line, col int) int {
	r := []rune(s)
	i, cur := 0, 0
	for i < len(r) && cur < line {
		if r[i] == '\n' {
			cur++
		}
		i++
	}
	c := 0
	for i < len(r) && r[i] != '\n' && c < col {
		i++
		c++
	}
	return i
}

// CaretVMove moves a rune-index caret one line up (dir -1) or down (dir +1),
// keeping the column.
func CaretVMove(s string, caret, dir int) int {
	line, col := CaretLineCol(s, caret)
	line += dir
	if line < 0 {
		line = 0
	}
	return CaretAt(s, line, col)
}
