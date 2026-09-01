package wasm

import "errors"

var (
	ErrIncompatibleVersion = errors.New("wasm: incompatible version")
	ErrInvalidManifest     = errors.New("wasm: invalid manifest")
	ErrInvalidLimits       = errors.New("wasm: invalid limits")
)

type Capability uint32

const (
	CapabilityRequestMetadata Capability = 1 << iota
	CapabilityClock
	CapabilityStorage
	CapabilityNetwork
	CapabilityLogging

	CapabilityAll = CapabilityRequestMetadata | CapabilityClock | CapabilityStorage | CapabilityNetwork | CapabilityLogging
)

type Profile uint8

const (
	ProfileInvalid Profile = iota
	ProfileWASI
	ProfileBrowser
	ProfileEnvoy
	ProfileIstio
	ProfileCloudflare
)

func (profile Profile) Valid() bool {
	return profile >= ProfileWASI && profile <= ProfileCloudflare
}

type Limits struct {
	MaxArtifactBytes  uint64
	MaxInputBytes     uint64
	MaxOutputBytes    uint64
	MaxFuel           uint64
	MaxRows           uint32
	MaxProgramColumns uint32
}

func (limits Limits) Validate() error {
	if limits.MaxArtifactBytes == 0 || limits.MaxInputBytes == 0 || limits.MaxOutputBytes == 0 || limits.MaxFuel == 0 ||
		limits.MaxRows == 0 || limits.MaxProgramColumns == 0 {
		return ErrInvalidLimits
	}
	return nil
}

type Manifest struct {
	Limits               Limits
	RequiredCapabilities Capability
	ABI                  ABIVersion
	Schema               SchemaVersion
	Profile              Profile
}

func (manifest Manifest) Validate() error {
	if manifest.ABI != CurrentABIVersion || manifest.Schema != CurrentSchemaVersion {
		return ErrIncompatibleVersion
	}
	if !manifest.Profile.Valid() || manifest.RequiredCapabilities&^CapabilityAll != 0 {
		return ErrInvalidManifest
	}
	if (manifest.Profile == ProfileWASI || manifest.Profile == ProfileBrowser) && manifest.RequiredCapabilities != 0 {
		return ErrInvalidManifest
	}
	return manifest.Limits.Validate()
}
