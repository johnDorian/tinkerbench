package project

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// NotesFile holds the written wiring notes. It is an ordinary versioned project file, but it is
// not part of what Fritzing gets (render bundle) or what is downloaded as .fzz.
const NotesFile = "notes.md"

var stamp = regexp.MustCompile(`(?m)^<!-- written against sketch v([0-9]+) -->[ \t]*\r?\n?`)

// ParseNotes splits stored notes into the body and the sketch version they were written against
// (0 if there is no stamp).
func ParseNotes(stored string) (body string, version int) {
	m := stamp.FindStringSubmatch(stored)
	if m == nil {
		return strings.TrimSpace(stored), 0
	}
	version, _ = strconv.Atoi(m[1])
	return strings.TrimSpace(stamp.ReplaceAllString(stored, "")), version
}

// StampNotes prepares notes for storing: the body plus the version stamp. Empty notes give "".
func StampNotes(body string, version int) string {
	body, _ = ParseNotes(body)
	if body == "" {
		return ""
	}
	return fmt.Sprintf("<!-- written against sketch v%d -->\n%s\n", version, body)
}
