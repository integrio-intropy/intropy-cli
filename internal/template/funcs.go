package template

import (
	"text/template"

	"github.com/Masterminds/sprig/v3"
)

// templateFuncs is the function map every template expression renders with:
// sprig, plus pascal — the same message-to-type derivation system assembly
// uses (PascalCase), so a skeleton naming a route's payload type or folder
// agrees with the generated system host by construction.
func templateFuncs() template.FuncMap {
	funcs := sprig.TxtFuncMap()
	funcs["pascal"] = PascalCase
	return funcs
}
