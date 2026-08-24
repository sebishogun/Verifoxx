package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sebishogun/verifoxx/internal/security"
)

var (
	errInvalidServerConfig = errors.New("httpapi: invalid server configuration")
	errBodyTooLarge        = errors.New("httpapi: request body too large")
	errBodyRead            = errors.New("httpapi: request body unreadable")
)

const maxBodyBytes int64 = security.MaximumRequestBytes

// Config fixes request storage and deadline limits for one HTTP adapter.
type Config struct {
	MaxBodyBytes   int64
	RequestTimeout time.Duration
}

func (config Config) valid() bool {
	if config.MaxBodyBytes <= 0 || config.MaxBodyBytes > maxBodyBytes {
		return false
	}
	limits := security.DefaultLimits()
	limits.RequestTimeout = config.RequestTimeout
	limits.MaxRequestBytes = int(config.MaxBodyBytes)
	limits.MaxOutputBytes = int(config.MaxBodyBytes)
	return limits.Validate() == nil
}

func (config Config) maxPolicyBytes() int64 {
	return min(config.MaxBodyBytes, int64(security.MaximumPolicyBytes))
}

func requireJSONContentType(request *http.Request) bool {
	if request == nil || request.Header.Get("Content-Encoding") != "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func readRequestBody(ctx context.Context, response http.ResponseWriter, request *http.Request, limit int64) ([]byte, error) {
	if request == nil || request.Body == nil {
		return nil, errBodyRead
	}
	controller := http.NewResponseController(response)
	if deadline, ok := ctx.Deadline(); ok {
		if err := controller.SetReadDeadline(deadline); err == nil {
			defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
		} else if !errors.Is(err, http.ErrNotSupported) {
			return nil, errBodyRead
		}
	}
	if request.ContentLength > limit {
		return nil, errBodyTooLarge
	}
	reader := http.MaxBytesReader(response, request.Body, limit)
	body, err := io.ReadAll(reader)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return nil, errBodyTooLarge
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			return nil, context.DeadlineExceeded
		}
		return nil, errBodyRead
	}
	if len(body) == 0 {
		return nil, errBodyRead
	}
	return body, nil
}
