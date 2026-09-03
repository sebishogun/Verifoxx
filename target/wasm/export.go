package wasm

import (
	"github.com/sebishogun/nornrune/internal/program"
	internalwasm "github.com/sebishogun/nornrune/internal/target/wasm"
)

type Metadata = internalwasm.Metadata

const MetadataBytes = internalwasm.MetadataBytes

// Export appends a deterministic, checksummed artifact for a validated Program.
func Export(dst []byte, compiled *program.Program, manifest Manifest) ([]byte, error) {
	return internalwasm.EncodeProgram(dst, compiled, manifest)
}

func DecodeMetadata(src []byte) (Metadata, error) { return internalwasm.DecodeMetadata(src) }
