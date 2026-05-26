package importsort

//ant:import-sort the import group below is out of canonical order
import (
	"strings"
	"fmt"
)

// Shout uppercases s, using both imports so the group stays compilable after the
// organizer sorts it (the sort reorders the spec lines; both packages remain
// referenced).
func Shout(s string) string {
	return fmt.Sprint(strings.ToUpper(s))
}
