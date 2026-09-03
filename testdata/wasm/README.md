# WebAssembly conformance fixtures

`harness.mjs` contains the runtime-independent ABI calls used by Node and the
browser harness. `browser-wasi.mjs` is the fail-closed browser WASI host used by
both `browser-harness.html` and the automated Node browser-host conformance
gate. Tests generate the module, Program artifact, input frame, and expected
result into temporary directories; generated binaries are not tracked. The
helper compares the artifact's hash-bound limits with pre-load module metadata,
uses the module-advertised fuel maximum, and the Node gates also require the
module to reject a valid artifact carrying mismatched limits.
