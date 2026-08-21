//go:build tools

package buildinfo

// Pins the SIMD runtime as a direct module dependency. Tool-tagged so normal
// builds do not link or initialize SIMD; go mod tidy still considers this
// import and retains the requirement.
import _ "github.com/sebishogun/simd"
