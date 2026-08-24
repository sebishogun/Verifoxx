// Package security defines service-wide safety ceilings and redaction rules.
package security

import (
	"errors"
	"time"
)

const (
	MaximumRequestTimeout  = 30 * time.Minute
	MaximumRequestBytes    = 64 << 20
	MaximumPolicyBytes     = 4 << 20
	MaximumASTDepth        = 128
	MaximumASTNodes        = 1 << 16
	MaximumOutputBytes     = 64 << 20
	MaximumBatchRows       = uint32(1 << 20)
	MaximumEvidenceRecords = uint32(1 << 18)
)

const (
	defaultRequestBytes = 8 << 20
	defaultOutputBytes  = 8 << 20
	defaultBatchRows    = uint32(64 << 10)
	defaultTimeout      = 30 * time.Second
)

// ErrInvalidLimits reports a disabled or unsafe service limit.
var ErrInvalidLimits = errors.New("security: invalid service limits")

// Limits contains the validated bounds shared by service and transport paths.
// Fields are ordered to keep same-width values contiguous on supported targets.
type Limits struct {
	RequestTimeout     time.Duration
	MaxRequestBytes    int
	MaxPolicyBytes     int
	MaxASTDepth        int
	MaxASTNodes        int
	MaxOutputBytes     int
	MaxBatchRows       uint32
	MaxEvidenceRecords uint32
}

// DefaultLimits returns the bounded standalone service defaults.
func DefaultLimits() Limits {
	return Limits{
		RequestTimeout:     defaultTimeout,
		MaxRequestBytes:    defaultRequestBytes,
		MaxPolicyBytes:     MaximumPolicyBytes,
		MaxASTDepth:        MaximumASTDepth,
		MaxASTNodes:        MaximumASTNodes,
		MaxOutputBytes:     defaultOutputBytes,
		MaxBatchRows:       defaultBatchRows,
		MaxEvidenceRecords: MaximumEvidenceRecords,
	}
}

// Validate rejects disabled limits and values above hard service ceilings.
func (limits Limits) Validate() error {
	if limits.RequestTimeout <= 0 || limits.RequestTimeout > MaximumRequestTimeout ||
		limits.MaxRequestBytes <= 0 || limits.MaxRequestBytes > MaximumRequestBytes ||
		limits.MaxPolicyBytes <= 0 || limits.MaxPolicyBytes > MaximumPolicyBytes ||
		limits.MaxASTDepth <= 0 || limits.MaxASTDepth > MaximumASTDepth ||
		limits.MaxASTNodes <= 0 || limits.MaxASTNodes > MaximumASTNodes ||
		limits.MaxOutputBytes <= 0 || limits.MaxOutputBytes > MaximumOutputBytes ||
		limits.MaxBatchRows == 0 || limits.MaxBatchRows > MaximumBatchRows ||
		limits.MaxEvidenceRecords == 0 || limits.MaxEvidenceRecords > MaximumEvidenceRecords {
		return ErrInvalidLimits
	}
	return nil
}
