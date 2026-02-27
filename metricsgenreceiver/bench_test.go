package metricsgenreceiver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver/receivertest"
)

type nopConsumer struct {
	count atomic.Uint64
}

func (c *nopConsumer) ConsumeLogs(_ context.Context, ld plog.Logs) error {
	c.count.Add(uint64(ld.LogRecordCount()))
	return nil
}

func (c *nopConsumer) Capabilities() consumer.Capabilities {
	return consumer.Capabilities{MutatesData: false}
}

func benchConfig() *Config {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	return &Config{
		StartTime:            startTime,
		EndTime:              startTime.Add(1 * time.Hour),
		Interval:             5 * time.Second,
		IntervalJitterStdDev: 10 * time.Millisecond,
		RealTime:             false,
		Seed:                 42,
		LogScenarios: []LogScenarioCfg{
			{
				Path:            "builtin/k8s-nginx",
				Scale:           30,
				LogsPerInterval: 50,
				Concurrency:     10,
				TemplateVars:    map[string]any{"nodes": 3},
			},
			{
				Path:            "builtin/k8s-mysql",
				Scale:           15,
				LogsPerInterval: 20,
				TemplateVars:    map[string]any{"nodes": 3},
			},
			{
				Path:            "builtin/k8s-redis",
				Scale:           15,
				LogsPerInterval: 30,
				TemplateVars:    map[string]any{"nodes": 3},
			},
			{
				Path:            "builtin/k8s-goapp",
				Scale:           30,
				LogsPerInterval: 40,
				Concurrency:     10,
				TemplateVars:    map[string]any{"nodes": 3},
			},
			{
				Path:             "builtin/k8s-proxy",
				Scale:            24,
				LogsPerInterval:  35,
				Concurrency:      8,
				EmitTraceContext: true,
				TemplateVars:     map[string]any{"nodes": 5},
			},
		},
	}
}

func runBench(b *testing.B, cfg *Config) {
	b.Helper()
	factory := NewFactory()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		nc := &nopConsumer{}
		cfgCopy := *cfg
		rcv, err := factory.CreateLogs(context.Background(), receivertest.NewNopSettings(typ), &cfgCopy, nc)
		if err != nil {
			b.Fatal(err)
		}
		logsRcv := rcv.(*LogsGenReceiver)

		b.StartTimer()
		if err := rcv.Start(context.Background(), nil); err != nil {
			b.Fatal(err)
		}
		<-logsRcv.done
		b.StopTimer()

		if err := rcv.Shutdown(context.Background()); err != nil {
			b.Fatal(err)
		}

		b.ReportMetric(logsRcv.progress.logsPerSecond(), "logs/sec")
		b.ReportMetric(float64(logsRcv.progress.logCount.Load()), "logs")
	}
}

func BenchmarkLogsGenReceiver(b *testing.B) {
	b.Run("full", func(b *testing.B) {
		runBench(b, benchConfig())
	})

	b.Run("single-nginx", func(b *testing.B) {
		startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		cfg := &Config{
			StartTime:            startTime,
			EndTime:              startTime.Add(1 * time.Hour),
			Interval:             5 * time.Second,
			IntervalJitterStdDev: 10 * time.Millisecond,
			RealTime:             false,
			Seed:                 42,
			LogScenarios: []LogScenarioCfg{
				{
					Path:            "builtin/k8s-nginx",
					Scale:           30,
					LogsPerInterval: 50,
					Concurrency:     10,
					TemplateVars:    map[string]any{"nodes": 3},
				},
			},
		}
		runBench(b, cfg)
	})
}
