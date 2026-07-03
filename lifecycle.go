package basecoat

import _ "embed"

//go:embed lifecycle.js
var lifecycleJS []byte

// lifecycleShim returns the JS appended immediately after the
// upstream basecoat runtime in parent mode. It wraps
// window.basecoat.register to accept an optional destroy(el)
// callback and adds basecoat.destroy(el) / destroyAll(root) /
// unregister(name). Idempotent at runtime.
func lifecycleShim() []byte { return lifecycleJS }
