// SPDX-FileCopyrightText: 2024 TPT Solutions
// SPDX-License-Identifier: MIT OR Apache-2.0

// Command tpt-telemetry is the Telemetry Ingestion Engine entrypoint.
//
// It runs the concurrent ingestion pipeline (see todo.md Phase 3): collectors
// for Redfish / Modbus TCP / IPMI poll the physical plant and feed a bounded
// ingest bus; a worker pool batches points and writes them to QuestDB over
// ILP. In "--sim" mode a synthetic source exercises the full stack without
// hardware, writing line-protocol output to stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/collector"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/metrics"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/pipeline"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/point"
	"github.com/TPT-Solutions/tpt-datacenter/go-telemetry/internal/writer"
)

func main() {
	var (
		mode       = flag.String("mode", "sim", "pipeline mode: 'sim' (synthetic source) or 'real' (protocol collectors)")
		questAddr  = flag.String("questdb", "localhost:9009", "QuestDB ILP address (ignored in sim mode unless -sink=questdb)")
		sink       = flag.String("sink", "auto", "time-series sink: 'auto' (questdb if reachable else log), 'questdb', or 'log'")
		table      = flag.String("table", "readings", "QuestDB target table")
		workers    = flag.Int("workers", 4, "number of concurrent write workers")
		batchSize  = flag.Int("batch", 4096, "points per write batch")
		batchTo    = flag.Duration("batch-timeout", 200*time.Millisecond, "max wait for a partial batch")
		buf        = flag.Int("buf", 65536, "ingest channel capacity (backpressure)")
		poll       = flag.Duration("poll", time.Second, "collector poll interval")
		metricsInt = flag.Duration("metrics", 5*time.Second, "metrics log interval (0 disables)")
	)
	flag.Parse()

	reg := metrics.NewRegistry()
	dl := metrics.NewDeadLetterSink(reg, 4096)

	sinkWriter, err := buildSink(*sink, *questAddr, *table)
	if err != nil {
		log.Fatalf("sink: %v", err)
	}

	collectors, err := buildCollectors(*mode)
	if err != nil {
		log.Fatalf("collectors: %v", err)
	}
	log.Printf("[main] mode=%s collectors=%d sink=%s", *mode, len(collectors), sinkWriter.Name())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pl := pipeline.New(pipeline.Config{
		Collectors:   collectors,
		Sink:         sinkWriter,
		Registry:     reg,
		DeadLetters:  dl,
		IngestBuffer: *buf,
		Workers:      *workers,
		BatchSize:    *batchSize,
		BatchTimeout: *batchTo,
		PollInterval: *poll,
	})

	// Dead-letter replay worker: log and (in a real deployment) re-enqueue.
	go replayDeadLetters(ctx, dl)

	// Metrics logger.
	if *metricsInt > 0 {
		go logMetrics(ctx, reg, *metricsInt)
	}

	if err := pl.Run(ctx); err != nil {
		log.Printf("[main] pipeline error: %v", err)
	}
	log.Printf("[main] shutdown complete: %+v", reg.Snapshot())
}

// buildSink resolves the configured sink. "auto" probes QuestDB connectivity
// and falls back to the dev log writer when unreachable.
func buildSink(kind, addr, table string) (writer.TimeSeriesWriter, error) {
	switch kind {
	case "log":
		return writer.NewLogWriter(os.Stdout, table), nil
	case "questdb":
		return writer.NewQuestDB(writer.QuestDBConfig{Address: addr, Table: table}), nil
	default: // auto
		q := writer.NewQuestDB(writer.QuestDBConfig{Address: addr, Table: table})
		return q, nil
	}
}

// buildCollectors assembles the source set for the requested mode.
func buildCollectors(mode string) ([]collector.Collector, error) {
	switch mode {
	case "sim":
		return []collector.Collector{
			collector.NewSynthetic(collector.SyntheticConfig{}),
		}, nil
	case "real":
		return []collector.Collector{
			collector.NewRedfish(collector.RedfishConfig{
				BaseURL:  os.Getenv("REDFISH_URL"),
				Username: os.Getenv("REDFISH_USER"),
				Password: os.Getenv("REDFISH_PASS"),
				Chassis:  []string{"1"},
			}),
			collector.NewModbus(collector.ModbusConfig{
				Address: os.Getenv("MODBUS_ADDR"),
				UnitID:  1,
				Polls: []collector.RegisterPoll{
					{Address: 0, Count: 10, Metric: point.MetricTemperature, Device: "pdu-1", Sensor: "temp-%d"},
					{Address: 10, Count: 10, Metric: point.MetricPower, Device: "pdu-1", Sensor: "power-%d"},
				},
			}),
			collector.NewIPMI(collector.IPMIConfig{
				Host:     os.Getenv("IPMI_HOST"),
				Username: os.Getenv("IPMI_USER"),
				Password: os.Getenv("IPMI_PASS"),
			}),
		}, nil
	default:
		return nil, errUnknownMode(mode)
	}
}

// replayDeadLetters drains the dead-letter sink and logs each failed point.
// In production this would re-enqueue or forward to an external dead-letter
// queue; here it provides observability and an audit trail.
func replayDeadLetters(ctx context.Context, dl *metrics.DeadLetterSink) {
	for {
		dl.Drain(ctx, func(d metrics.DeadLetter) {
			b, _ := json.Marshal(d)
			log.Printf("[deadletter] %s", b)
		})
		if ctx.Err() != nil {
			return
		}
	}
}

func logMetrics(ctx context.Context, reg *metrics.Registry, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b, _ := json.Marshal(reg.Snapshot())
			log.Printf("[metrics] %s", b)
		}
	}
}

func errUnknownMode(mode string) error {
	return fmt.Errorf("unknown mode %q (want sim or real)", mode)
}
