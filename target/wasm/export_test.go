package wasm

import (
	"errors"
	"testing"
)

func TestManifestValidatesVersionProfileCapabilitiesAndLimits(t *testing.T) {
	validLimits := Limits{
		MaxArtifactBytes:  8 << 20,
		MaxInputBytes:     4 << 20,
		MaxOutputBytes:    8 << 20,
		MaxRows:           4096,
		MaxProgramColumns: 256,
		MaxFuel:           1 << 30,
	}
	tests := []struct {
		name     string
		manifest Manifest
		wantErr  error
	}{
		{
			name: "wasi base",
			manifest: Manifest{
				ABI: CurrentABIVersion, Schema: CurrentSchemaVersion,
				Profile: ProfileWASI, Limits: validLimits,
			},
		},
		{
			name: "browser base",
			manifest: Manifest{
				ABI: CurrentABIVersion, Schema: CurrentSchemaVersion,
				Profile: ProfileBrowser, Limits: validLimits,
			},
		},
		{
			name: "explicit envoy capabilities",
			manifest: Manifest{
				ABI: CurrentABIVersion, Schema: CurrentSchemaVersion,
				Profile:              ProfileEnvoy,
				RequiredCapabilities: CapabilityRequestMetadata | CapabilityClock,
				Limits:               validLimits,
			},
		},
		{
			name:     "zero abi",
			manifest: Manifest{Schema: CurrentSchemaVersion, Profile: ProfileWASI, Limits: validLimits},
			wantErr:  ErrIncompatibleVersion,
		},
		{
			name:     "future schema",
			manifest: Manifest{ABI: CurrentABIVersion, Schema: CurrentSchemaVersion + 1, Profile: ProfileWASI, Limits: validLimits},
			wantErr:  ErrIncompatibleVersion,
		},
		{
			name:     "unknown profile",
			manifest: Manifest{ABI: CurrentABIVersion, Schema: CurrentSchemaVersion, Profile: Profile(255), Limits: validLimits},
			wantErr:  ErrInvalidManifest,
		},
		{
			name: "unknown capability",
			manifest: Manifest{
				ABI: CurrentABIVersion, Schema: CurrentSchemaVersion, Profile: ProfileEnvoy,
				RequiredCapabilities: Capability(1 << 31), Limits: validLimits,
			},
			wantErr: ErrInvalidManifest,
		},
		{
			name: "base profile host capability",
			manifest: Manifest{
				ABI: CurrentABIVersion, Schema: CurrentSchemaVersion, Profile: ProfileWASI,
				RequiredCapabilities: CapabilityNetwork, Limits: validLimits,
			},
			wantErr: ErrInvalidManifest,
		},
		{
			name: "zero artifact limit",
			manifest: Manifest{
				ABI: CurrentABIVersion, Schema: CurrentSchemaVersion, Profile: ProfileWASI,
				Limits: Limits{MaxInputBytes: 1, MaxOutputBytes: 1, MaxRows: 1, MaxProgramColumns: 1, MaxFuel: 1},
			},
			wantErr: ErrInvalidLimits,
		},
		{
			name: "output below input",
			manifest: Manifest{
				ABI: CurrentABIVersion, Schema: CurrentSchemaVersion, Profile: ProfileWASI,
				Limits: Limits{MaxArtifactBytes: 2, MaxInputBytes: 2, MaxOutputBytes: 1, MaxRows: 1, MaxProgramColumns: 1, MaxFuel: 1},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.manifest.Validate()
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Validate() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestABIEnumsAreFixedAndBounded(t *testing.T) {
	if ArtifactMagic != 0x4e525750 || FrameMagic != 0x4e525746 {
		t.Fatalf("magic = %#x/%#x", ArtifactMagic, FrameMagic)
	}
	if !OperationMetadata.Valid() || !OperationLastError.Valid() || Operation(0).Valid() || Operation(255).Valid() {
		t.Fatal("operation validity contract changed")
	}
	if !ErrorNone.Valid() || !ErrorInternal.Valid() || ErrorCode(255).Valid() {
		t.Fatal("error-code validity contract changed")
	}
	if CapabilityAll != CapabilityRequestMetadata|CapabilityClock|CapabilityStorage|CapabilityNetwork|CapabilityLogging {
		t.Fatalf("CapabilityAll = %#x", CapabilityAll)
	}
}

func TestExportRejectsNilProgram(t *testing.T) {
	manifest := Manifest{
		ABI: CurrentABIVersion, Schema: CurrentSchemaVersion, Profile: ProfileWASI,
		Limits: Limits{
			MaxArtifactBytes: 1 << 20, MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20,
			MaxFuel: 1 << 20, MaxRows: 1024, MaxProgramColumns: 256,
		},
	}
	if _, err := Export(nil, nil, manifest); err == nil {
		t.Fatal("Export(nil Program) succeeded")
	}
}
