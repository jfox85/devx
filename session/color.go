package session

import "hash/fnv"

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
