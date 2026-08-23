package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sebishogun/verifoxx/internal/program"
)

var (
	// ErrInvalidPolicyPersistence reports an invalid publication dependency,
	// input, candidate, or compiled Program.
	ErrInvalidPolicyPersistence = errors.New("persistence: invalid policy operation")
	// ErrStoredPolicyNotFound reports an absent active policy or content hash.
	ErrStoredPolicyNotFound = errors.New("persistence: stored policy not found")
	// ErrPolicyVersionConflict reports different source for an existing policy
	// semantic version.
	ErrPolicyVersionConflict = errors.New("persistence: policy version conflicts with stored source")
	// ErrStoredPolicyCorrupt reports invalid or inconsistent durable metadata.
	ErrStoredPolicyCorrupt = errors.New("persistence: stored policy is corrupt")
	// ErrPolicyActivation reports a database-committed version that could not be
	// published or activated in the local registry.
	ErrPolicyActivation = errors.New("persistence: durable policy was not activated")
)

// PolicyID identifies one stable policy name in durable storage.
type PolicyID int64

// PolicyVersionID identifies one immutable durable policy version.
type PolicyVersionID int64

// Candidate is compiled policy identity borrowed by PolicyStore for one call.
type Candidate struct {
	Program         *program.Program
	Name            string
	SemanticVersion string
	CompilerVersion string
	Source          []byte
	ContentHash     [sha256.Size]byte
}

// PolicyVersion is one complete immutable policy-version row.
type PolicyVersion struct {
	Name            string
	SemanticVersion string
	CompilerVersion string
	PublishedAt     time.Time
	Source          []byte
	ContentHash     [sha256.Size]byte
	PolicyID        PolicyID
	ID              PolicyVersionID
}

// PolicyStore persists and loads canonical policy source and metadata.
type PolicyStore interface {
	PublishActive(context.Context, Candidate) (PolicyVersion, error)
	LoadActive(context.Context, string) (PolicyVersion, error)
	LoadByHash(context.Context, [sha256.Size]byte) (PolicyVersion, error)
}

// CompileFunc compiles retained source into one frozen Program.
type CompileFunc func([]byte) (*program.Program, error)

// Publisher coordinates durable publication with local immutable registry
// publication and activation. Its mutex covers only the cold commit-to-activate
// interval; compilation occurs before it.
type Publisher struct {
	store           PolicyStore
	compile         CompileFunc
	registry        *program.Registry
	compilerVersion string
	publish         sync.Mutex
}

// NewPublisher validates policy publication dependencies.
func NewPublisher(
	store PolicyStore,
	registry *program.Registry,
	compile CompileFunc,
	compilerVersion string,
) (*Publisher, error) {
	if store == nil || registry == nil || compile == nil || !validTrimmedPolicyText(compilerVersion) {
		return nil, fmt.Errorf("%w: publisher dependencies", ErrInvalidPolicyPersistence)
	}
	return &Publisher{
		store:           store,
		compile:         compile,
		registry:        registry,
		compilerVersion: compilerVersion,
	}, nil
}

// Publish compiles source, commits its canonical version, then publishes and
// activates the corresponding immutable Program.
func (publisher *Publisher) Publish(
	ctx context.Context,
	source []byte,
) (*program.Program, PolicyVersion, error) {
	if publisher == nil || ctx == nil || len(source) == 0 {
		return nil, PolicyVersion{}, fmt.Errorf("%w: publish input", ErrInvalidPolicyPersistence)
	}
	if err := ctx.Err(); err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("%w: %w", ErrInvalidPolicyPersistence, err)
	}

	compiled, err := publisher.compile(source)
	if err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("compile policy: %w", err)
	}
	candidate, err := candidateFromProgram(compiled, source, publisher.compilerVersion)
	if err != nil {
		return nil, PolicyVersion{}, err
	}

	publisher.publish.Lock()
	defer publisher.publish.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("%w: %w", ErrInvalidPolicyPersistence, err)
	}

	version, err := publisher.store.PublishActive(ctx, candidate)
	if err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("persist active policy: %w", err)
	}
	if err := validateStoredCandidate(version, candidate); err != nil {
		return nil, version, fmt.Errorf("%w: stored version: %w", ErrPolicyActivation, err)
	}

	canonical, err := publisher.registry.Publish(compiled)
	if err != nil {
		return nil, version, fmt.Errorf("%w: publish registry: %w", ErrPolicyActivation, err)
	}
	if err := validateProgramVersion(canonical, version); err != nil {
		return nil, version, fmt.Errorf("%w: canonical program: %w", ErrPolicyActivation, err)
	}
	if err := publisher.registry.Activate(version.ContentHash); err != nil {
		return nil, version, fmt.Errorf("%w: activate registry: %w", ErrPolicyActivation, err)
	}
	return canonical, version, nil
}

// ReloadActive loads the durable active version for name, compiles it when
// absent from the registry, and activates the canonical Program.
func (publisher *Publisher) ReloadActive(
	ctx context.Context,
	name string,
) (*program.Program, PolicyVersion, error) {
	if publisher == nil || ctx == nil || !validTrimmedPolicyText(name) {
		return nil, PolicyVersion{}, fmt.Errorf("%w: active reload input", ErrInvalidPolicyPersistence)
	}
	if err := ctx.Err(); err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("%w: %w", ErrInvalidPolicyPersistence, err)
	}

	publisher.publish.Lock()
	defer publisher.publish.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("%w: %w", ErrInvalidPolicyPersistence, err)
	}
	version, err := publisher.store.LoadActive(ctx, name)
	if err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("load active policy: %w", err)
	}
	if err := ValidatePolicyVersion(version); err != nil {
		return nil, version, err
	}
	if version.Name != name {
		return nil, version, fmt.Errorf("%w: active policy name", ErrStoredPolicyCorrupt)
	}
	return publisher.activateLoaded(version)
}

// ReloadHash loads one durable content hash, compiles it when absent from the
// registry, and activates the canonical Program.
func (publisher *Publisher) ReloadHash(
	ctx context.Context,
	hash [sha256.Size]byte,
) (*program.Program, PolicyVersion, error) {
	if publisher == nil || ctx == nil || hash == [sha256.Size]byte{} {
		return nil, PolicyVersion{}, fmt.Errorf("%w: hash reload input", ErrInvalidPolicyPersistence)
	}
	if err := ctx.Err(); err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("%w: %w", ErrInvalidPolicyPersistence, err)
	}

	publisher.publish.Lock()
	defer publisher.publish.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("%w: %w", ErrInvalidPolicyPersistence, err)
	}
	version, err := publisher.store.LoadByHash(ctx, hash)
	if err != nil {
		return nil, PolicyVersion{}, fmt.Errorf("load policy hash: %w", err)
	}
	if err := ValidatePolicyVersion(version); err != nil {
		return nil, version, err
	}
	if version.ContentHash != hash {
		return nil, version, fmt.Errorf("%w: loaded policy hash", ErrStoredPolicyCorrupt)
	}
	return publisher.activateLoaded(version)
}

func (publisher *Publisher) activateLoaded(version PolicyVersion) (*program.Program, PolicyVersion, error) {
	canonical, err := publisher.registry.LoadOrCompile(version.ContentHash, func() (*program.Program, error) {
		compiled, err := publisher.compile(version.Source)
		if err != nil {
			return nil, fmt.Errorf("compile stored policy: %w", err)
		}
		if err := validateProgramVersion(compiled, version); err != nil {
			return nil, err
		}
		return compiled, nil
	})
	if err != nil {
		return nil, version, fmt.Errorf("%w: reload registry: %w", ErrPolicyActivation, err)
	}
	if err := validateProgramVersion(canonical, version); err != nil {
		return nil, version, fmt.Errorf("%w: canonical program: %w", ErrPolicyActivation, err)
	}
	if err := publisher.registry.Activate(version.ContentHash); err != nil {
		return nil, version, fmt.Errorf("%w: activate registry: %w", ErrPolicyActivation, err)
	}
	return canonical, version, nil
}

// ValidateCandidate rejects incomplete or inconsistent compiled identity.
func ValidateCandidate(candidate Candidate) error {
	if !validPolicyMetadata(
		candidate.Source,
		candidate.Name,
		candidate.SemanticVersion,
		candidate.CompilerVersion,
		candidate.ContentHash,
	) {
		return fmt.Errorf("%w: candidate metadata", ErrInvalidPolicyPersistence)
	}
	if candidate.Program == nil || candidate.Program.ContentHash != candidate.ContentHash ||
		!bytes.Equal(candidate.Program.InputBytes, candidate.Source) {
		return fmt.Errorf("%w: candidate Program identity", ErrInvalidPolicyPersistence)
	}
	name, nameOK := candidate.Program.Symbol(candidate.Program.PolicyName)
	semanticVersion, versionOK := candidate.Program.Symbol(candidate.Program.PolicyVersion)
	if !nameOK || !versionOK || string(name) != candidate.Name || string(semanticVersion) != candidate.SemanticVersion {
		return fmt.Errorf("%w: candidate Program metadata", ErrInvalidPolicyPersistence)
	}
	return nil
}

// ValidatePolicyVersion rejects incomplete or inconsistent durable identity.
func ValidatePolicyVersion(version PolicyVersion) error {
	if version.PolicyID <= 0 || version.ID <= 0 || version.PublishedAt.IsZero() ||
		!validPolicyMetadata(
			version.Source,
			version.Name,
			version.SemanticVersion,
			version.CompilerVersion,
			version.ContentHash,
		) {
		return fmt.Errorf("%w: version metadata", ErrStoredPolicyCorrupt)
	}
	return nil
}

func validPolicyMetadata(
	source []byte,
	name string,
	semanticVersion string,
	compilerVersion string,
	hash [sha256.Size]byte,
) bool {
	if len(source) == 0 || !validTrimmedPolicyText(name) ||
		!validTrimmedPolicyText(semanticVersion) ||
		!validTrimmedPolicyText(compilerVersion) || hash == [sha256.Size]byte{} {
		return false
	}
	calculated := sha256.Sum256(source)
	return subtle.ConstantTimeCompare(calculated[:], hash[:]) == 1
}

func validTrimmedPolicyText(value string) bool {
	return value != "" && value == strings.TrimSpace(value)
}

func candidateFromProgram(
	compiled *program.Program,
	source []byte,
	compilerVersion string,
) (Candidate, error) {
	if compiled == nil || !bytes.Equal(compiled.InputBytes, source) {
		return Candidate{}, fmt.Errorf("%w: compiled source", ErrInvalidPolicyPersistence)
	}
	name, nameOK := compiled.Symbol(compiled.PolicyName)
	semanticVersion, versionOK := compiled.Symbol(compiled.PolicyVersion)
	if !nameOK || !versionOK {
		return Candidate{}, fmt.Errorf("%w: compiled metadata", ErrInvalidPolicyPersistence)
	}
	candidate := Candidate{
		Program:         compiled,
		Source:          compiled.InputBytes,
		Name:            string(name),
		SemanticVersion: string(semanticVersion),
		CompilerVersion: compilerVersion,
		ContentHash:     compiled.ContentHash,
	}
	if err := ValidateCandidate(candidate); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

func validateStoredCandidate(version PolicyVersion, candidate Candidate) error {
	if err := ValidatePolicyVersion(version); err != nil {
		return err
	}
	if version.Name != candidate.Name ||
		version.SemanticVersion != candidate.SemanticVersion ||
		version.ContentHash != candidate.ContentHash ||
		!bytes.Equal(version.Source, candidate.Source) {
		return fmt.Errorf("%w: stored candidate identity", ErrStoredPolicyCorrupt)
	}
	return nil
}

func validateProgramVersion(compiled *program.Program, version PolicyVersion) error {
	if compiled == nil || compiled.ContentHash != version.ContentHash ||
		!bytes.Equal(compiled.InputBytes, version.Source) {
		return fmt.Errorf("%w: compiled identity", ErrStoredPolicyCorrupt)
	}
	name, nameOK := compiled.Symbol(compiled.PolicyName)
	semanticVersion, versionOK := compiled.Symbol(compiled.PolicyVersion)
	if !nameOK || !versionOK || string(name) != version.Name || string(semanticVersion) != version.SemanticVersion {
		return fmt.Errorf("%w: compiled metadata", ErrStoredPolicyCorrupt)
	}
	return nil
}
