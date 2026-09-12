package template

import "strings"

// KeyPayloadType names the derived value a template stores alongside the
// message: the PascalCase projection of the message identity. The message
// is the declaration; the payload type is generated from it, not declared
// alongside it. "contract" remains the override key (older records and
// hand-tuned names); the parser's priority is contract, payloadType, then
// the derivation below.
const KeyPayloadType = "payloadType"

// PascalCase derives the payload type name an internal message's schema
// projects to: strips the message to its letter-and-digit runs, drops
// separators (dots, dashes, underscores), and capitalizes each run.
// "orders" -> "Orders", "order-events" -> "OrderEvents". No singularization:
// the name is generated, and an explicit contract override wins wherever a
// derived name would read poorly. Returns "" for an input with no letters
// or digits; callers gate on that and keep the strict regime.
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
