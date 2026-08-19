package editor

import "strings"

// Word-level diff for a proposed text change, so a row can show what the
// proposal actually touches instead of two whole copies of itself. Words, not
// characters: a character diff on prose shreds words into fragments that read
// as noise, and the unit somebody edits in is the word.

type diffOp int

const (
	diffSame diffOp = iota
	diffDel
	diffAdd
)

// diffPart is one run of text with a single verdict.
type diffPart struct {
	text string
	op   diffOp
}

// wordDiff turns before/after into the runs a row renders: kept text, removed
// text, added text — removals ahead of the additions that replace them, so the
// row reads as "this became that".
func wordDiff(before, after string) []diffPart {
	a, b := splitWords(before), splitWords(after)
	// words are matched on the word alone, spacing aside: the last word of a row
	// carries no trailing space, so comparing the raw slices called every
	// appended word a rewrite of the one before it
	ka, kb := trimAll(a), trimAll(b)

	// dp[i][j] is the longest common run of words from a[i:] and b[j:]
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if ka[i] == kb[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var out []diffPart
	push := func(text string, op diffOp) {
		if text == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].op == op {
			out[n-1].text += text
			return
		}
		out = append(out, diffPart{text: text, op: op})
	}

	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case ka[i] == kb[j]:
			push(a[i], diffSame)
			i, j = i+1, j+1
		case dp[i+1][j] >= dp[i][j+1]:
			push(a[i], diffDel)
			i++
		default:
			push(b[j], diffAdd)
			j++
		}
	}
	for ; i < len(a); i++ {
		push(a[i], diffDel)
	}
	for ; j < len(b); j++ {
		push(b[j], diffAdd)
	}
	return out
}

// splitWords cuts text into words that carry their own trailing spaces, so
// reassembling the parts reproduces the text exactly — spacing included.
func splitWords(s string) []string {
	var out []string
	runes := []rune(s)
	for i := 0; i < len(runes); {
		start := i
		for i < len(runes) && runes[i] != ' ' {
			i++
		}
		for i < len(runes) && runes[i] == ' ' {
			i++
		}
		out = append(out, string(runes[start:i]))
	}
	return out
}

// trimAll is the words without their trailing spaces — the keys the diff
// matches on.
func trimAll(words []string) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = strings.TrimRight(w, " ")
	}
	return out
}

// diffText reassembles one side of a diff — the before text (kept + removed) or
// the after text (kept + added).
func diffText(parts []diffPart, side diffOp) string {
	var b strings.Builder
	for _, p := range parts {
		if p.op == diffSame || p.op == side {
			b.WriteString(p.text)
		}
	}
	return b.String()
}
