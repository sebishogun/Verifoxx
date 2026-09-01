// Command loadgen applies a fixed, bounded HTTP or gRPC evaluation workload.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	nornrunev1 "github.com/sebishogun/nornrune/api/gen/nornrune/v1"
	"github.com/sebishogun/nornrune/internal/fixtures"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	defaultRequests    uint64 = 1000
	maximumRequests    uint64 = 1_000_000
	defaultConcurrency        = 4
	maximumConcurrency        = 256
	defaultTimeout            = 30 * time.Second
	maximumTimeout            = 10 * time.Minute
	maximumResponse           = 4 << 20
)

type options struct {
	protocol    string
	target      string
	requests    uint64
	timeout     time.Duration
	concurrency int
}

type loadReport struct {
	Protocol           string  `json:"protocol"`
	Target             string  `json:"target"`
	RequestedRequests  uint64  `json:"requested_requests"`
	CompletedRequests  uint64  `json:"completed_requests"`
	Concurrency        int     `json:"concurrency"`
	ElapsedNanoseconds int64   `json:"elapsed_nanoseconds"`
	RequestsPerSecond  float64 `json:"requests_per_second"`
}

type runStats struct {
	firstError error
	elapsed    time.Duration
	completed  uint64
}

type requester interface {
	request(context.Context) error
	close() error
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	config, err := parseOptions(args, stderr)
	if err != nil {
		return err
	}
	client, err := newRequester(config)
	if err != nil {
		return err
	}
	stats := runLoad(ctx, config, client)
	report := loadReport{
		Protocol: config.protocol, Target: config.target,
		RequestedRequests: config.requests, CompletedRequests: stats.completed,
		Concurrency: config.concurrency, ElapsedNanoseconds: stats.elapsed.Nanoseconds(),
	}
	if stats.elapsed > 0 {
		report.RequestsPerSecond = float64(stats.completed) / stats.elapsed.Seconds()
	}
	encodeErr := json.NewEncoder(stdout).Encode(report)
	closeErr := client.close()
	if encodeErr != nil || closeErr != nil {
		return errors.Join(encodeErr, closeErr, stats.firstError)
	}
	if stats.firstError != nil {
		return fmt.Errorf("loadgen: stopped after %d of %d requests: %w", stats.completed, config.requests, stats.firstError)
	}
	if stats.completed != config.requests {
		return fmt.Errorf("loadgen: completed %d of %d requests", stats.completed, config.requests)
	}
	return nil
}

func parseOptions(args []string, stderr io.Writer) (options, error) {
	config := options{}
	flags := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&config.protocol, "protocol", "http", "protocol: http or grpc")
	flags.StringVar(&config.target, "target", "", "server host:port")
	flags.Uint64Var(&config.requests, "requests", defaultRequests, "fixed request count")
	flags.IntVar(&config.concurrency, "concurrency", defaultConcurrency, "concurrent workers")
	flags.DurationVar(&config.timeout, "timeout", defaultTimeout, "total run timeout")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("loadgen: positional arguments are not supported")
	}
	if config.protocol != "http" && config.protocol != "grpc" {
		return options{}, errors.New("loadgen: protocol must be http or grpc")
	}
	if config.requests == 0 || config.requests > maximumRequests {
		return options{}, fmt.Errorf("loadgen: requests must be within [1, %d]", maximumRequests)
	}
	if config.concurrency <= 0 || config.concurrency > maximumConcurrency || uint64(config.concurrency) > config.requests {
		return options{}, fmt.Errorf("loadgen: concurrency must be within [1, min(requests, %d)]", maximumConcurrency)
	}
	if config.timeout <= 0 || config.timeout > maximumTimeout {
		return options{}, fmt.Errorf("loadgen: timeout must be within (0, %s]", maximumTimeout)
	}
	if config.target == "" {
		if config.protocol == "http" {
			config.target = "127.0.0.1:8080"
		} else {
			config.target = "127.0.0.1:9090"
		}
	}
	if err := validateTarget(config.target); err != nil {
		return options{}, err
	}
	return config, nil
}

func validateTarget(target string) error {
	host, encodedPort, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return errors.New("loadgen: target must be host:port")
	}
	port, err := strconv.ParseUint(encodedPort, 10, 16)
	if err != nil || port == 0 {
		return errors.New("loadgen: target port must be within [1, 65535]")
	}
	return nil
}

func runLoad(ctx context.Context, config options, client requester) runStats {
	runContext, cancelRun := context.WithTimeout(ctx, config.timeout)
	startedAt := time.Now()
	partitions := make([]uint64, config.concurrency)
	partitionRequests(partitions, config.requests)
	var completed atomic.Uint64
	var firstError error
	var firstErrorOnce sync.Once
	var workers sync.WaitGroup
	workers.Add(config.concurrency)
	for worker := range partitions {
		assigned := partitions[worker]
		go func() {
			defer workers.Done()
			for range assigned {
				if runContext.Err() != nil {
					return
				}
				if err := client.request(runContext); err != nil {
					firstErrorOnce.Do(func() {
						firstError = err
						cancelRun()
					})
					return
				}
				completed.Add(1)
			}
		}()
	}
	workers.Wait()
	if firstError == nil && completed.Load() != config.requests {
		firstError = runContext.Err()
		if firstError == nil {
			firstError = errors.New("loadgen: request budget was not completed")
		}
	}
	cancelRun()
	return runStats{firstError: firstError, elapsed: time.Since(startedAt), completed: completed.Load()}
}

func partitionRequests(partitions []uint64, requests uint64) {
	workers := uint64(len(partitions))
	base := requests / workers
	remainder := requests % workers
	for worker := range partitions {
		partitions[worker] = base
		if uint64(worker) < remainder {
			partitions[worker]++
		}
	}
}

func newRequester(config options) (requester, error) {
	if config.protocol == "http" {
		payload, err := json.Marshal(struct {
			Requests json.RawMessage `json:"requests"`
			Evidence json.RawMessage `json:"evidence"`
		}{Requests: json.RawMessage(fixtures.RequestsJSON()), Evidence: json.RawMessage(fixtures.EvidenceJSON())})
		if err != nil {
			return nil, fmt.Errorf("loadgen: encode HTTP payload: %w", err)
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConns = config.concurrency
		transport.MaxIdleConnsPerHost = config.concurrency
		return &httpRequester{
			payload: payload, endpoint: "http://" + config.target + "/v1/evaluate",
			client: &http.Client{Transport: transport},
		}, nil
	}
	connection, err := grpc.NewClient(
		config.target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maximumResponse), grpc.WaitForReady(true)),
	)
	if err != nil {
		return nil, fmt.Errorf("loadgen: create gRPC client: %w", err)
	}
	return &grpcRequester{
		client:     nornrunev1.NewPolicyServiceClient(connection),
		connection: connection,
		payload: &nornrunev1.EvaluateBatchRequest{
			RequestsJson: []byte(fixtures.RequestsJSON()),
			EvidenceJson: []byte(fixtures.EvidenceJSON()),
		},
	}, nil
}

type httpRequester struct {
	client   *http.Client
	endpoint string
	payload  []byte
}

func (client *httpRequester) request(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(client.payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return err
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maximumResponse+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("HTTP status %s", response.Status)
	}
	if len(body) > maximumResponse {
		return errors.New("HTTP response exceeds size limit")
	}
	if !json.Valid(body) {
		return errors.New("HTTP response is not JSON")
	}
	return nil
}

func (client *httpRequester) close() error {
	client.client.CloseIdleConnections()
	return nil
}

type grpcRequester struct {
	client     nornrunev1.PolicyServiceClient
	connection *grpc.ClientConn
	payload    *nornrunev1.EvaluateBatchRequest
}

func (client *grpcRequester) request(ctx context.Context) error {
	response, err := client.client.EvaluateBatch(ctx, client.payload)
	if err != nil {
		return err
	}
	if !json.Valid(response.GetResultJson()) {
		return errors.New("gRPC response result_json is not JSON")
	}
	return nil
}

func (client *grpcRequester) close() error {
	return client.connection.Close()
}
