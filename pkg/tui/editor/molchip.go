package editor

import (
	"strings"
)

// A molecule chip is a molecule inline in ordinary text: the chip value is the
// full notation (SMILES or SELFIES) and it renders compactly as the benzene ring
// ⌬ followed by a truncated formula, so a sentence can carry a structure without
// the notation swallowing the line.
//
// Two ways in, both through /insert (see insertChip):
//   - CONVERT: the token before the caret is already a molecule — the picker
//     detects it and offers to fold exactly that token into a chip.
//   - INSERT: nothing detected, so an empty chip lands to be filled in.
//
// Detection is deliberately strict (molLooksLikeNotation): the SMILES grammar is
// so permissive that ordinary prose would otherwise "parse" — the viewer's parser
// skips characters it does not know, which is right for rendering and wrong for
// deciding whether a word is chemistry.

// molChipMaxDisplay is how many notation runes the chip shows before eliding;
// long structures stay one glanceable token instead of taking over the row.
const molChipMaxDisplay = 12

// molChipDisplay is the chip's compact form: the benzene ring plus the notation,
// truncated with an ellipsis. An empty chip shows the ring alone, so a chip you
// have not filled in yet is still visible and selectable.
func molChipDisplay(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "⌬"
	}
	r := []rune(v)
	if len(r) > molChipMaxDisplay {
		return "⌬" + string(r[:molChipMaxDisplay]) + "…"
	}
	return "⌬" + string(r)
}

// molNotationChar reports whether r is part of the notation grammar. Anything
// outside this set means the token is prose, not a structure.
func molNotationChar(r rune) bool {
	switch {
	case r >= '0' && r <= '9':
		return true
	case strings.ContainsRune("()[]=#-+@/\\%.*", r):
		return true
	}
	return molOrganicUpper(r) || molAromaticLower(r)
}

func molOrganicUpper(r rune) bool  { return strings.ContainsRune("BCNOPSFIHKVYWU", r) }
func molAromaticLower(r rune) bool { return strings.ContainsRune("bcnops", r) }

// molLooksLikeNotation is the strict detector behind the convert offer. A token
// qualifies only when every rune belongs to the grammar, it parses to at least
// two atoms, and its ring labels balance — plus it must show a STRUCTURAL marker
// (a bond, branch, ring digit or bracket) unless it is a run of uppercase element
// symbols. Without that last rule ordinary words ("SIN", "IS") would read as
// chemistry, since their letters happen to be element symbols.
func molLooksLikeNotation(tok string) bool {
	tok = strings.TrimSpace(tok)
	if len([]rune(tok)) < 3 {
		return false
	}
	// SELFIES is unambiguous — bracket tokens only — so it needs no heuristics.
	if looksLikeSELFIES(tok) {
		g, err := parseSELFIES(tok)
		return err == nil && len(g.atoms) >= 2
	}
	structural := false
	depth, rings := 0, map[rune]int{}
	for _, r := range tok {
		if !molNotationChar(r) {
			return false
		}
		switch {
		case r == '(':
			depth++
			structural = true
		case r == ')':
			depth--
			if depth < 0 {
				return false
			}
			structural = true
		case r >= '0' && r <= '9':
			rings[r]++
			structural = true
		case strings.ContainsRune("=#[]", r):
			structural = true
		}
	}
	if depth != 0 {
		return false
	}
	for _, n := range rings { // a ring label must open and close
		if n%2 != 0 {
			return false
		}
	}
	// A token with no structural marker is just letters, and plenty of English
	// words ("SIN", "CON", "PIN") are spelled entirely from element symbols. Such
	// a token only counts as chemistry if it is uppercase AND carries a real
	// carbon skeleton — two or more carbons, as in "CCO" (ethanol).
	if !structural {
		if strings.ToUpper(tok) != tok || strings.Count(tok, "C") < 2 {
			return false
		}
	}
	g, err := parseSMILES(tok)
	return err == nil && len(g.atoms) >= 2
}

// molTokenBeforeCaret finds the whitespace-delimited token ending at the caret
// and reports it with its rune range when it reads as a molecule. This is what
// turns "paste a SMILES, then /insert" into a one-key conversion.
func molTokenBeforeCaret(cur *item, caret int) (tok string, start, end int, ok bool) {
	if cur == nil {
		return "", 0, 0, false
	}
	runes := []rune(cur.name)
	if caret > len(runes) {
		caret = len(runes)
	}
	end = caret
	// allow the caret to sit just after a trailing space
	for end > 0 && runes[end-1] == ' ' {
		end--
	}
	start = end
	for start > 0 && runes[start-1] != ' ' && runes[start-1] != chipSentinel {
		start--
	}
	if start >= end {
		return "", 0, 0, false
	}
	tok = string(runes[start:end])
	if !molLooksLikeNotation(tok) {
		return "", 0, 0, false
	}
	return tok, start, end, true
}

// molConvertBeforeCaret folds a detected token into a molecule chip in place,
// replacing exactly those runes with the chip anchor. Returns false when there
// is nothing to convert, so the caller can fall back to inserting a blank chip.
func (m *Model) molConvertBeforeCaret(cur *item) bool {
	tok, start, end, ok := molTokenBeforeCaret(cur, m.caret)
	if !ok {
		return false
	}
	anchor := m.createChip(chipKindMol, tok)
	if anchor == "" {
		return false
	}
	runes := []rune(cur.name)
	cur.name = string(runes[:start]) + anchor + string(runes[end:])
	m.caret = start + len([]rune(anchor))
	m.unsaved = true
	return true
}
