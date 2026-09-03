package telemetry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	spanExportTimeout  = 30 * time.Second
	admissionClosed    = uint64(1) << 63
	admissionCountMask = admissionClosed - 1
)

type spanProcessorRequest struct {
	ctx      context.Context
	response chan error
}

type boundedSpanProcessor struct {
	exporter    sdktrace.SpanExporter
	shutdownErr error
	counters    *internaltelemetry.Counters
	queue       chan sdktrace.ReadOnlySpan
	requests    chan spanProcessorRequest
	stop        chan struct{}
	quiesced    chan struct{}
	done        chan struct{}
	interval    time.Duration
	batchSize   int

	shutdownOnce sync.Once
	admission    atomic.Uint64
}

func newBoundedSpanProcessor(
	exporter sdktrace.SpanExporter,
	counters *internaltelemetry.Counters,
	queueSize uint32,
	interval time.Duration,
) *boundedSpanProcessor {
	batchSize := min(int(queueSize), 512)
	processor := &boundedSpanProcessor{
		exporter: exporter, counters: counters,
		queue: make(chan sdktrace.ReadOnlySpan, int(queueSize)), requests: make(chan spanProcessorRequest),
		stop: make(chan struct{}), quiesced: make(chan struct{}), done: make(chan struct{}),
		interval: interval, batchSize: batchSize,
	}
	go processor.run()
	return processor
}

func (*boundedSpanProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}

func (processor *boundedSpanProcessor) OnEnd(span sdktrace.ReadOnlySpan) {
	if processor == nil || span == nil || !span.SpanContext().IsSampled() {
		return
	}
	state := processor.admission.Add(1)
	if state&admissionClosed != 0 {
		processor.admission.Add(^uint64(0))
		processor.counters.AddExportDrop(1)
		return
	}
	select {
	case processor.queue <- span:
	default:
		processor.counters.AddExportDrop(1)
	}
	if processor.admission.Add(^uint64(0)) == admissionClosed {
		close(processor.quiesced)
	}
}

func (processor *boundedSpanProcessor) ForceFlush(ctx context.Context) error {
	if processor == nil || ctx == nil {
		return ErrInvalidConfig
	}
	if processor.admission.Load()&admissionClosed != 0 {
		return nil
	}
	return processor.request(ctx)
}

func (processor *boundedSpanProcessor) Shutdown(ctx context.Context) error {
	if processor == nil || ctx == nil {
		return ErrInvalidConfig
	}
	processor.beginShutdown()
	select {
	case <-processor.done:
		return processor.shutdownErr
	default:
	}
	select {
	case <-processor.done:
		return processor.shutdownErr
	case <-ctx.Done():
		select {
		case <-processor.done:
			return processor.shutdownErr
		default:
			return ctx.Err()
		}
	}
}

func (processor *boundedSpanProcessor) beginShutdown() {
	if processor == nil {
		return
	}
	processor.shutdownOnce.Do(func() {
		previous := processor.admission.Or(admissionClosed)
		if previous&admissionCountMask == 0 {
			close(processor.quiesced)
		}
		close(processor.stop)
	})
}

func (processor *boundedSpanProcessor) request(ctx context.Context) error {
	response := make(chan error, 1)
	request := spanProcessorRequest{ctx: ctx, response: response}
	select {
	case processor.requests <- request:
	case <-processor.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-response:
		return err
	case <-processor.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (processor *boundedSpanProcessor) run() {
	timer := time.NewTimer(processor.interval)
	defer timer.Stop()
	batch := make([]sdktrace.ReadOnlySpan, 0, processor.batchSize)

	export := func(ctx context.Context) error {
		if len(batch) == 0 {
			return nil
		}
		err := processor.exporter.ExportSpans(ctx, batch)
		if err != nil {
			processor.counters.AddExportDrop(1)
		}
		clear(batch)
		batch = batch[:0]
		return err
	}

	drain := func(ctx context.Context) error {
		var joined error
		for {
			for len(batch) < cap(batch) {
				select {
				case span := <-processor.queue:
					batch = append(batch, span)
				default:
					if len(batch) != 0 {
						joined = errors.Join(joined, export(ctx))
					}
					return joined
				}
			}
			joined = errors.Join(joined, export(ctx))
			if err := ctx.Err(); err != nil {
				return errors.Join(joined, err)
			}
		}
	}

	resetTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(processor.interval)
	}
	dropRemaining := func() {
		dropped := uint64(len(batch))
		clear(batch)
		batch = batch[:0]
		for {
			select {
			case <-processor.queue:
				dropped++
			default:
				processor.counters.AddExportDrop(dropped)
				return
			}
		}
	}
	shutdown := func() {
		<-processor.quiesced
		ctx, cancel := context.WithTimeout(context.Background(), spanExportTimeout)
		err := drain(ctx)
		if ctx.Err() != nil {
			dropRemaining()
		}
		err = errors.Join(err, processor.exporter.Shutdown(ctx), ctx.Err())
		cancel()
		processor.shutdownErr = err
		close(processor.done)
	}

	for {
		select {
		case <-processor.stop:
			shutdown()
			return
		default:
		}
		select {
		case <-processor.stop:
			shutdown()
			return
		case span := <-processor.queue:
			batch = append(batch, span)
			if len(batch) == cap(batch) {
				ctx, cancel := context.WithTimeout(context.Background(), spanExportTimeout)
				_ = export(ctx)
				cancel()
				resetTimer()
			}
		case request := <-processor.requests:
			err := drain(request.ctx)
			request.response <- err
			resetTimer()
		case <-timer.C:
			ctx, cancel := context.WithTimeout(context.Background(), spanExportTimeout)
			_ = drain(ctx)
			cancel()
			timer.Reset(processor.interval)
		}
	}
}
