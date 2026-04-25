package session

import "hash/fnv"

// AnsiColors maps palette color names to ANSI escape sequences.
var AnsiColors = map[string]string{
	"red": "\033[31m", "blue": "\033[34m", "green": "\033[32m", "yellow": "\033[33m",
	"purple": "\033[35m", "orange": "\033[38;5;208m", "pink": "\033[38;5;213m", "cyan": "\033[36m",
}

// AnsiReset is the ANSI escape sequence to reset terminal colors.
const AnsiReset = "\033[0m"

// Palette is the set of valid session colors, matching Claude Code's palette.
var Palette = []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}

// IsValidColor returns true if c is a recognized session color.
func IsValidColor(c string) bool {
	for _, p := range Palette {
		if c == p {
			return true
		}
	}
	return false
}

// AutoColor deterministically assigns a color to a session name by hashing.
func AutoColor(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return Palette[h.Sum32()%uint32(len(Palette))]
}
