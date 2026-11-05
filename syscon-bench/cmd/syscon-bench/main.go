// Command syscon-bench walks a scenario, pushes generated logs through a Sink,
// and runs the query client on the same clock.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"syscon-bench/internal/generate"
	"syscon-bench/internal/metrics"
	"syscon-bench/internal/query"
	"syscon-bench/internal/scenario"
	"syscon-bench/internal/storage"
)

const usageText = `Usage:
  syscon-bench prepare [--logs dir] [--fields file] [--messages file] [--lines n]
  syscon-bench run --scenario <file> [--fields file] [--messages file]
      [--storage-name <id> --storage-endpoint <url>]
      [--via vector] [--vector-endpoint <url>]
  syscon-bench write --out <file> [--bytes n] [--scenario <file>]
      [--min-size n] [--max-size n] [--error-rate f] [--seed n]
  syscon-bench blackhole [--addr host:port] [--username <user> --password <pass>]
  syscon-bench file [--addr host:port] [--path file] [--username <user> --password <pass>]
  syscon-bench dashboard [--addr host:port] [--username <user> --password <pass>]

prepare reads up to one million lines from each *.log directly under --logs,
masks usr/ord/pay/bk ids and UUIDs as #user-id, #order-id, #payment-id,
#booking-id, and #track-id, and writes the field pool plus that message file.

run loads those files first. instance-id, ip-address, host-name, and service
are each drawn from their 1000 values. message is one masked line with the
placeholders filled in. It then walks the scenario clock and POSTs batches.
error_rate sets log-severity to error and status-code to 500; the request is
still sent. query_rate starts searches in this process: full-text, exact
match, and filter, using field values from events the sink has accepted.

Storage names: blackhole, clickhouse, elasticsearch, loki, victorialogs.
Each engine adapter inserts over that engine's HTTP API and runs the matching
search. --storage-name and --storage-endpoint override the scenario storage
together and set protocol from the URL scheme. Auth stays as written in the
file and is sent when the scenario auth type is basic. --via vector posts
ingest to the local sidecar as newline-delimited JSON and leaves queries on
the storage endpoint. The storage name is unchanged.

write fills a JSONL file with contract events until it reaches --bytes
(default 2GiB). It does not start the scenario clock and does not push metrics.
--scenario supplies min_size, max_size, and the first segment error_rate.
Explicit size and error-rate flags replace those values.

blackhole listens for those POSTs and discards the body.
file listens for the same POSTs and appends each event as one JSON line.
dashboard listens for the client metrics POST and serves the latest body on
GET /metrics for Prometheus. GET /metrics is open. POST uses the basic auth
when --username and --password are set.

Example, from syscon-bench/:
  syscon-bench prepare --logs ../envoy-log-generator/logs
  syscon-bench dashboard --addr :8001 --username admin --password admin
  syscon-bench run --scenario log-generator/scenarios/test-scenario-01.yaml \
      --storage-name clickhouse --storage-endpoint http://localhost:8123
`

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("syscon-bench: ")
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		return errors.New("a command is required")
	}
	switch args[0] {
	case "prepare":
		return cmdPrepare(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "write":
		return cmdWrite(args[1:])
	case "blackhole":
		return cmdBlackhole(args[1:])
	case "file":
		return cmdFile(args[1:])
	case "dashboard":
		return cmdDashboard(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scenarioPath := fs.String("scenario", "", "scenario file")
	fieldsPath := fs.String("fields", "data/fields.csv", "pool of 1000 instance-id, ip-address, host-name, and service values")
	messagesPath := fs.String("messages", "data/messages.txt", "masked log lines, one message per line")
	storageName := fs.String("storage-name", "", "override storage name")
	storageEndpoint := fs.String("storage-endpoint", "", "override storage endpoint")
	via := fs.String("via", "", "set to vector to push ingest to the local sidecar")
	vectorEndpoint := fs.String("vector-endpoint", "http://127.0.0.1:8686", "Vector HTTP source")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *scenarioPath == "" {
		return errors.New("--scenario is required")
	}
	if (*storageName == "") != (*storageEndpoint == "") {
		return errors.New("--storage-name and --storage-endpoint must be set together")
	}
	if *via != "" && *via != "vector" {
		return fmt.Errorf("--via %q is not supported", *via)
	}
	sc, err := scenario.Load(*scenarioPath)
	if err != nil {
		return err
	}
	if *storageName != "" {
		if err := sc.OverrideStorage(*storageName, *storageEndpoint); err != nil {
			return err
		}
	}
	engine, err := openSink(sc.Storage)
	if err != nil {
		return err
	}
	sink := engine
	var querier storage.Querier
	if q, ok := engine.(storage.Querier); ok {
		querier = q
	}
	if *via == "vector" {
		if querier == nil {
			return errors.New("--via vector requires a storage querier")
		}
		sink, err = storage.NewVector(*vectorEndpoint)
		if err != nil {
			return err
		}
		log.Printf("ingest via vector at %s; queries stay on %s", *vectorEndpoint, sc.Storage.Endpoint)
	}
	cat, err := generate.LoadCatalog(*fieldsPath, *messagesPath)
	if err != nil {
		return err
	}
	log.Printf("loaded %d field values and %d messages", generate.PoolSize, len(cat.Messages))
	series, err := metrics.New(prometheus.DefaultRegisterer, prometheus.DefaultGatherer, sc.Storage.Name, sc.Name)
	if err != nil {
		return err
	}
	defer series.Unregister()

	user, pass := "", ""
	if sc.Dashboard.Auth.Type == "basic" {
		user, pass = sc.Dashboard.Auth.Username, sc.Dashboard.Auth.Password
	}
	pusher := &metrics.Pusher{
		Endpoint: sc.Dashboard.Endpoint,
		Username: user,
		Password: pass,
		Metrics:  series,
		Logger:   log.Default(),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opt := generate.Options{
		TickInterval: metrics.PushInterval,
		Tick:         pusher.At,
		Catalog:      cat,
	}
	if querier != nil {
		recent := query.NewRecent(0)
		opt.Accepted = recent.Add
		opt.QueryAt = (&query.Client{
			Querier: querier,
			Recent:  recent,
			Metrics: series,
			Logger:  log.Default(),
		}).Execute
	}
	_, err = generate.Run(ctx, sc, metricSink{next: sink, series: series}, opt)
	return err
}

func openSink(t scenario.Target) (storage.Sink, error) {
	user, pass := "", ""
	if t.Auth.Type == "basic" {
		user, pass = t.Auth.Username, t.Auth.Password
	}
	switch t.Name {
	case "blackhole":
		return storage.NewBlackhole(t.Endpoint, user, pass)
	case "clickhouse":
		return storage.NewClickHouse(t.Endpoint, user, pass)
	case "elasticsearch":
		return storage.NewElasticsearch(t.Endpoint, user, pass)
	case "loki":
		return storage.NewLoki(t.Endpoint, user, pass)
	case "victorialogs":
		return storage.NewVictoriaLogs(t.Endpoint, user, pass)
	default:
		return nil, fmt.Errorf("storage %q is not implemented", t.Name)
	}
}

// metricSink counts accepted message bytes and sink failures on the bench series.
type metricSink struct {
	next   storage.Sink
	series *metrics.Metrics
}

func (s metricSink) Push(ctx context.Context, batch []storage.Event) error {
	err := s.next.Push(ctx, batch)
	if err != nil {
		s.series.IngestFailed(len(batch))
		return err
	}
	n := 0
	for _, ev := range batch {
		n += len(ev.Message)
	}
	s.series.IngestAccepted(len(batch), n)
	return nil
}

func cmdWrite(args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("out", "", "JSONL file to write")
	target := fs.Int64("bytes", 2147483648, "stop when the file reaches this many bytes")
	scenarioPath := fs.String("scenario", "", "scenario whose min_size, max_size, and first error_rate are used")
	minSize := fs.Int("min-size", 0, "message bytes; overrides the scenario")
	maxSize := fs.Int("max-size", 0, "message bytes; overrides the scenario")
	errorRate := fs.Float64("error-rate", -1, "fraction of events marked as errors; overrides the scenario")
	seed := fs.Uint64("seed", 1, "rng seed")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	min, max := 100, 200000
	rate := 0.01
	if *scenarioPath != "" {
		sc, err := scenario.Load(*scenarioPath)
		if err != nil {
			return err
		}
		min, max = sc.Configs.MinSize, sc.Configs.MaxSize
		rate = sc.Segments[0].ErrorRate
	}
	if *minSize != 0 {
		min = *minSize
	}
	if *maxSize != 0 {
		max = *maxSize
	}
	if *errorRate >= 0 {
		rate = *errorRate
	}
	res, err := generate.WriteFile(*out, *target, min, max, rate, *seed)
	if err != nil {
		return err
	}
	fmt.Printf("events=%d\nbytes=%d\npath=%s\n", res.Events, res.Bytes, *out)
	return nil
}

func cmdPrepare(args []string) error {
	fs := flag.NewFlagSet("prepare", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	logsDir := fs.String("logs", filepath.Join("..", "envoy-log-generator", "logs"), "directory of *.log files")
	fieldsPath := fs.String("fields", filepath.Join("data", "fields.csv"), "field pool to write")
	messagesPath := fs.String("messages", filepath.Join("data", "messages.txt"), "masked message lines to write")
	lines := fs.Int("lines", 1_000_000, "maximum lines taken from each log file")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	total, err := generate.Prepare(*logsDir, *fieldsPath, *messagesPath, *lines)
	if err != nil {
		return err
	}
	log.Printf("wrote %s and %d message lines to %s", *fieldsPath, total, *messagesPath)
	return nil
}

func cmdDashboard(args []string) error {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", ":8001", "listen address")
	user := fs.String("username", "", "basic auth username for the metrics POST")
	pass := fs.String("password", "", "basic auth password for the metrics POST")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if (*user == "") != (*pass == "") {
		return errors.New("dashboard: --username and --password must be set together")
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *user != "" {
		log.Printf("dashboard requiring basic auth for %s", *user)
	}
	log.Printf("dashboard listening on %s", ln.Addr())
	return serve(ctx, ln, (&metrics.Receiver{Username: *user, Password: *pass, Logger: log.Default()}).Handler())
}

func cmdBlackhole(args []string) error {
	fs := flag.NewFlagSet("blackhole", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", ":8000", "listen address")
	user := fs.String("username", "", "basic auth username")
	pass := fs.String("password", "", "basic auth password")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if (*user == "") != (*pass == "") {
		return errors.New("blackhole: --username and --password must be set together")
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveBlackhole(ctx, ln, *user, *pass)
}

func cmdFile(args []string) error {
	fs := flag.NewFlagSet("file", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", ":8000", "listen address")
	path := fs.String("path", "events.jsonl", "file that receives one JSON event per line")
	user := fs.String("username", "", "basic auth username")
	pass := fs.String("password", "", "basic auth password")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, usageText)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if (*user == "") != (*pass == "") {
		return errors.New("file: --username and --password must be set together")
	}
	store, err := storage.OpenFileLog(*path)
	if err != nil {
		return err
	}
	defer store.Close()
	abs, err := filepath.Abs(store.Path())
	if err != nil {
		abs = store.Path()
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go logFileCount(ctx, store)
	if *user != "" {
		log.Printf("file requiring basic auth for %s", *user)
	}
	log.Printf("file listening on %s, writing %s", ln.Addr(), abs)
	err = serve(ctx, ln, store.Handler(*user, *pass))
	log.Printf("file stored %d events in %s", store.Count(), abs)
	return err
}

func logFileCount(ctx context.Context, store *storage.FileLog) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	var prev int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			n := store.Count()
			log.Printf("file stored %d events (+%d)", n, n-prev)
			prev = n
		}
	}
}

func serveBlackhole(ctx context.Context, ln net.Listener, user, pass string) error {
	if user != "" {
		log.Printf("blackhole requiring basic auth for %s", user)
	}
	log.Printf("blackhole listening on %s", ln.Addr())
	return serve(ctx, ln, storage.Handler(user, pass))
}

func serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		errc <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		err := <-errc
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
