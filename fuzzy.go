package main

import "strings"

// fuzzyScore reports whether query is a case-insensitive subsequence of s,
// with a score that favors consecutive runs and word-boundary matches.
func fuzzyScore(s, query string) (int, bool) {
	if query == "" {
		return 0, true
	}
	sr := []rune(strings.ToLower(s))
	qr := []rune(strings.ToLower(query))
	score, qi, streak := 0, 0, 0
	for si := 0; si < len(sr) && qi < len(qr); si++ {
		if sr[si] != qr[qi] {
			streak = 0
			continue
		}
		score++
		if streak > 0 {
			score += 2
		}
		if si == 0 || isWordSep(sr[si-1]) {
			score += 3
		}
		streak++
		qi++
	}
	if qi < len(qr) {
		return 0, false
	}
	return score, true
}

func isWordSep(r rune) bool {
	return r == '-' || r == '_' || r == '.' || r == '/' || r == ' '
}
