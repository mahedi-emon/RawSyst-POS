// The build this binary came from.
//
// One variable, set once by one linker flag, read by every command. It was six
// variables and six flags — `-X main.version` repeated per binary — which is
// fine while each command is its own `main` package and impossible once they
// share one. Nothing else about it changed: it is still stamped at build time
// and still says "dev" when nobody stamped it.
package build

// Version is set at build time via -ldflags:
//
//	-X github.com/mahedi-emon/rawsyst-pos/backend/internal/build.Version=1.2.3
var Version = "dev"
