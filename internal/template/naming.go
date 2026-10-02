package template

import "strings"

// PascalCase derives the payload type name an internal message's schema
// projects to: strips the message to its letter-and-digit runs, drops
// separators (dots, dashes, underscores), and capitalizes each run.
// "orders" -> "Orders", "order-events" -> "OrderEvents". No singularization:
// the name is generated, never declared — the message is the only
// declaration. Returns "" for an input with no letters or digits; callers
// gate on that. The template library applies the same derivation at render
// time (payloadType), so the generated record and assembly can never
// disagree.
func PascalCase(message string) string {
	var b strings.Builder
	capitalize := true
	for _, r := range message {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			if capitalize {
				b.WriteRune(upper(r))
				capitalize = false
				continue
			}
			b.WriteRune(r)
			continue
		}
		capitalize = true
	}
	return b.String()
}

func upper(r rune) rune {
	if r >= 'a' && r <= 'z' {
		return r - ('a' - 'A')
	}
	return r
}
