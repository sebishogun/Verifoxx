package debug

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

var (
	ErrInvalidTransportConfig = errors.New("debug: invalid transport config")
	ErrInvalidFrame           = errors.New("debug: invalid protocol frame")
	ErrFrameTooLarge          = errors.New("debug: protocol frame too large")
	ErrTransportClosed        = errors.New("debug: transport closed")
)

const (
	maxTransportMessageBytes = 16 << 20
	maxTransportInFlight     = 64
	maxTransportQueueDepth   = 256
)

// TransportConfig bounds local protocol memory and in-flight commands.
type TransportConfig struct {
	MaxMessageBytes uint32
	MaxInFlight     int
	QueueDepth      int
}

// DefaultTransportConfig returns bounded settings suitable for the local TUI.
func DefaultTransportConfig() TransportConfig {
	return TransportConfig{MaxMessageBytes: 1 << 20, MaxInFlight: 8, QueueDepth: 16}
}

func (config TransportConfig) valid() bool {
	return config.MaxMessageBytes > 0 && config.MaxMessageBytes <= maxTransportMessageBytes &&
		config.MaxInFlight >= 2 && config.MaxInFlight <= maxTransportInFlight &&
		config.QueueDepth >= config.MaxInFlight && config.QueueDepth <= maxTransportQueueDepth
}

// Operation is one semantic debugger command on the local wire.
type Operation string

const (
	OperationSnapshot         Operation = "snapshot"
	OperationStepInstruction  Operation = "step_instruction"
	OperationStepNode         Operation = "step_node"
	OperationStepOver         Operation = "step_over"
	OperationContinue         Operation = "continue"
	OperationPause            Operation = "pause"
	OperationRestart          Operation = "restart"
	OperationReplay           Operation = "replay"
	OperationAddBreakpoint    Operation = "add_breakpoint"
	OperationRemoveBreakpoint Operation = "remove_breakpoint"
	OperationAddWatch         Operation = "add_watch"
	OperationRemoveWatch      Operation = "remove_watch"
	OperationResult           Operation = "result"
	OperationCancel           Operation = "cancel"
)

func (operation Operation) valid() bool {
	switch operation {
	case OperationSnapshot, OperationStepInstruction, OperationStepNode, OperationStepOver,
		OperationContinue, OperationPause, OperationRestart, OperationReplay,
		OperationAddBreakpoint, OperationRemoveBreakpoint, OperationAddWatch,
		OperationRemoveWatch, OperationResult, OperationCancel:
		return true
	default:
		return false
	}
}

// Request is one length-prefixed command envelope. ID is nonzero and unique
// among in-flight requests on one connection.
type Request struct {
	Operation    Operation            `json:"operation"`
	ID           uint64               `json:"id"`
	CancelID     uint64               `json:"cancel_id,omitempty"`
	Breakpoint   Breakpoint           `json:"breakpoint,omitempty"`
	Watch        Watch                `json:"watch,omitempty"`
	Instruction  schema.InstructionID `json:"instruction,omitempty"`
	BreakpointID BreakpointID         `json:"breakpoint_id,omitempty"`
	WatchID      WatchID              `json:"watch_id,omitempty"`
}

// ErrorCode is one stable protocol-level failure category.
type ErrorCode string

const (
	ErrorInvalidRequest ErrorCode = "invalid_request"
	ErrorFrameLimit     ErrorCode = "frame_limit"
	ErrorCanceled       ErrorCode = "canceled"
	ErrorSession        ErrorCode = "session"
	ErrorInternal       ErrorCode = "internal"
)

// ProtocolError is safe to return over the local semantic socket.
type ProtocolError struct {
	Message string    `json:"message"`
	Code    ErrorCode `json:"code"`
}

func (protocolError *ProtocolError) Error() string {
	if protocolError == nil {
		return ""
	}
	return protocolError.Message
}

// Response correlates an out-of-order command completion by request ID.
type Response struct {
	State        *State         `json:"state,omitempty"`
	Result       *result.Batch  `json:"result,omitempty"`
	Error        *ProtocolError `json:"error,omitempty"`
	ID           uint64         `json:"id"`
	BreakpointID BreakpointID   `json:"breakpoint_id,omitempty"`
	WatchID      WatchID        `json:"watch_id,omitempty"`
}

type frameReader struct {
	buffer []byte
}

func (reader *frameReader) read(source io.Reader, maximum uint32, destination any) error {
	if reader == nil || source == nil || destination == nil || maximum == 0 || maximum > maxTransportMessageBytes {
		return ErrInvalidFrame
	}
	var prefix [4]byte
	n, err := io.ReadFull(source, prefix[:])
	if err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return io.EOF
		}
		return fmt.Errorf("%w: length: %v", ErrInvalidFrame, err)
	}
	length := binary.BigEndian.Uint32(prefix[:])
	if length == 0 {
		return fmt.Errorf("%w: empty payload", ErrInvalidFrame)
	}
	if length > maximum {
		return ErrFrameTooLarge
	}
	if cap(reader.buffer) < int(length) {
		reader.buffer = make([]byte, int(length))
	} else {
		reader.buffer = reader.buffer[:int(length)]
	}
	if _, err := io.ReadFull(source, reader.buffer); err != nil {
		return fmt.Errorf("%w: payload: %v", ErrInvalidFrame, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(reader.buffer))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrInvalidFrame, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing data", ErrInvalidFrame)
	}
	return nil
}

func writeFrame(destination io.Writer, maximum uint32, value any) error {
	if destination == nil || value == nil || maximum == 0 || maximum > maxTransportMessageBytes {
		return ErrInvalidFrame
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode: %v", ErrInvalidFrame, err)
	}
	if len(payload) == 0 || uint64(len(payload)) > uint64(maximum) {
		return ErrFrameTooLarge
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(payload)))
	if err := writeAll(destination, prefix[:]); err != nil {
		return err
	}
	return writeAll(destination, payload)
}

func writeAll(destination io.Writer, data []byte) error {
	for len(data) != 0 {
		written, err := destination.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(data) {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
