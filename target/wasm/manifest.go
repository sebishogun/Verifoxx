package wasm

import internalwasm "github.com/sebishogun/nornrune/internal/target/wasm"

var (
	ErrIncompatibleVersion = internalwasm.ErrIncompatibleVersion
	ErrInvalidManifest     = internalwasm.ErrInvalidManifest
	ErrInvalidLimits       = internalwasm.ErrInvalidLimits
)

type Capability = internalwasm.Capability

const (
	CapabilityRequestMetadata = internalwasm.CapabilityRequestMetadata
	CapabilityClock           = internalwasm.CapabilityClock
	CapabilityStorage         = internalwasm.CapabilityStorage
	CapabilityNetwork         = internalwasm.CapabilityNetwork
	CapabilityLogging         = internalwasm.CapabilityLogging
	CapabilityAll             = internalwasm.CapabilityAll
)

type Profile = internalwasm.Profile

const (
	ProfileInvalid    = internalwasm.ProfileInvalid
	ProfileWASI       = internalwasm.ProfileWASI
	ProfileBrowser    = internalwasm.ProfileBrowser
	ProfileEnvoy      = internalwasm.ProfileEnvoy
	ProfileIstio      = internalwasm.ProfileIstio
	ProfileCloudflare = internalwasm.ProfileCloudflare
)

type Limits = internalwasm.Limits
type Manifest = internalwasm.Manifest
