package metricsgenreceiver

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/loggen"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/logstats"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/logstmpl"
	"github.com/elastic/metricsgenreceiver/metricsgenreceiver/internal/metadata"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componentstatus"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	"go.uber.org/zap"
)

type LogsGenReceiver struct {
	cfg       *Config
	obsreport *receiverhelper.ObsReport
	settings  receiver.Settings

	baseRand          *rand.Rand
	nextLogs          consumer.Logs
	cancel            context.CancelFunc
	scenarios         []LogScenario
	progress          *LogsProgress
	needleOccurrences map[string]*atomic.Uint64
	stats             *logstats.ShardedLogStats
}

type LogScenario struct {
	config    LogScenarioCfg
	resources []pcommon.Resource
	prepared  *loggen.PreparedProfile
	volume    volumeState
}

type volumeState struct {
	multiplier         float64
	remainingIntervals int
}

// resolveVolumeMultiplier returns the effective multiplier for this interval and
// updates the state for the next call. Must be called on the main goroutine
// before fan-out so all workers for a scenario see the same volume.
func resolveVolumeMultiplier(vs *volumeState, rng *rand.Rand, vp *VolumeProfileCfg) float64 {
	if vp == nil {
		return 1.0
	}
	if vs.remainingIntervals > 0 {
		vs.remainingIntervals--
		return vs.multiplier
	}
	roll := rng.Float64()
	switch {
	case roll < vp.BurstProbability:
		vs.multiplier = vp.BurstMultiplierMin + rng.Float64()*(vp.BurstMultiplierMax-vp.BurstMultiplierMin)
		vs.remainingIntervals = vp.BurstDurationMin + rng.Intn(vp.BurstDurationMax-vp.BurstDurationMin+1) - 1
		return vs.multiplier
	case roll < vp.BurstProbability+vp.QuietProbability:
		vs.multiplier = vp.QuietMultiplier
		vs.remainingIntervals = vp.QuietDurationMin + rng.Intn(vp.QuietDurationMax-vp.QuietDurationMin+1) - 1
		return vs.multiplier
	default:
		return 1.0
	}
}

type LogsProgress struct {
	start    time.Time
	logCount atomic.Uint64
}

func newLogsProgress() *LogsProgress {
	return &LogsProgress{
		start: time.Now(),
	}
}

func (p *LogsProgress) duration() time.Duration {
	return time.Since(p.start)
}

func (p *LogsProgress) logsPerSecond() float64 {
	return float64(p.logCount.Load()) / p.duration().Seconds()
}

func newLogsGenReceiver(cfg *Config, set receiver.Settings) (*LogsGenReceiver, error) {
	obsreport, err := receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
		ReceiverID:             set.ID,
		ReceiverCreateSettings: set,
	})
	if err != nil {
		return nil, err
	}

	nowish := time.Now().Truncate(time.Second)
	if cfg.StartTime.IsZero() {
		cfg.StartTime = nowish.Add(-cfg.StartNowMinus)
	}
	if cfg.EndTime.IsZero() {
		cfg.EndTime = nowish.Add(-cfg.EndNowMinus)
	}

	baseRand := rand.New(rand.NewSource(cfg.Seed))

	scenarios := make([]LogScenario, 0, len(cfg.LogScenarios))
	needleNames := make(map[string]struct{})
	for _, scn := range cfg.LogScenarios {
		resources, err := logstmpl.GetLogResources(scn.Path, cfg.StartTime, scn.Scale, scn.TemplateVars, baseRand)
		if err != nil {
			return nil, err
		}
		profile := loggen.GetAppProfile(scn.Path)
		if profile == nil {
			serviceName := "unknown"
			if len(resources) > 0 {
				if v, ok := resources[0].Attributes().Get("service.name"); ok {
					serviceName = v.Str()
				}
			}
			profile = loggen.GenericProfile(serviceName)
		}
		prepared := loggen.PrepareProfile(profile)
		if scn.SeverityWeights != nil {
			prepared.OverrideSeverityWeights(*scn.SeverityWeights)
		}
		scenarios = append(scenarios, LogScenario{
			config:    scn,
			resources: resources,
			prepared:  prepared,
		})
		for _, needle := range scn.Needles {
			needleNames[needle.Name] = struct{}{}
		}
	}

	needleOccurrences := make(map[string]*atomic.Uint64, len(needleNames))
	for name := range needleNames {
		needleOccurrences[name] = &atomic.Uint64{}
	}

	// Shard 0: sequential scenarios (Concurrency==0). Shards 1+:
	// unique shard per concurrent worker across all scenarios (they run in parallel).
	totalConcurrentShards := 0
	for _, scn := range cfg.LogScenarios {
		if scn.Concurrency > 0 {
			totalConcurrentShards += scn.Concurrency
		}
	}
	numShards := 1 + totalConcurrentShards
	if numShards < 1 {
		numShards = 1
	}
	stats := logstats.NewShardedLogStats(numShards)

	return &LogsGenReceiver{
		cfg:               cfg,
		settings:          set,
		baseRand:          baseRand,
		obsreport:         obsreport,
		scenarios:         scenarios,
		progress:          newLogsProgress(),
		needleOccurrences: needleOccurrences,
		stats:             stats,
	}, nil
}

func (r *LogsGenReceiver) Start(ctx context.Context, host component.Host) error {
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	go func() {
		nextLog := r.progress.start.Add(10 * time.Second)
		ticker := time.NewTicker(r.cfg.Interval)
		defer ticker.Stop()
		currentTime := r.cfg.StartTime
		for currentTime.UnixNano() < r.cfg.EndTime.UnixNano() {
			if ctx.Err() != nil {
				return
			}
			if time.Now().After(nextLog) {
				progressPct := currentTime.Sub(r.cfg.StartTime).Seconds() / r.cfg.EndTime.Sub(r.cfg.StartTime).Seconds()
				r.settings.Logger.Info("generating logs progress",
					zap.Int("progress_percent", int(progressPct*100)),
					zap.Uint64("logs", r.progress.logCount.Load()),
					zap.Float64("logs_per_second", r.progress.logsPerSecond()),
				)
				nextLog = nextLog.Add(10 * time.Second)
			}
			r.progress.logCount.Add(r.produceLogs(ctx, currentTime))

			if r.cfg.RealTime {
				<-ticker.C
			}
			currentTime = currentTime.Add(r.cfg.Interval)
		}
		if r.cfg.ExitAfterEnd {
			if r.cfg.ExitAfterEndTimeout > 0 {
				r.settings.Logger.Info("finished generating logs, waiting before exiting",
					zap.Duration("exit_after_end_timeout", r.cfg.ExitAfterEndTimeout),
				)
				time.Sleep(r.cfg.ExitAfterEndTimeout)
			} else {
				r.settings.Logger.Info("finished generating logs, exiting immediately")
			}
			componentstatus.ReportStatus(host, componentstatus.NewFatalErrorEvent(errors.New("exiting because exit_after_end is set to true")))
		}
	}()

	return nil
}

func addLogJitter(t time.Time, stdDev time.Duration, interval time.Duration, ra *rand.Rand) time.Time {
	if stdDev == 0 {
		return t
	}
	jitter := time.Duration(int64(math.Abs(ra.NormFloat64() * float64(stdDev))))
	if jitter >= interval {
		jitter = interval - 1
	}
	return t.Add(jitter)
}

func severityText(sev plog.SeverityNumber) string {
	switch sev {
	case plog.SeverityNumberTrace:
		return "TRACE"
	case plog.SeverityNumberDebug:
		return "DEBUG"
	case plog.SeverityNumberInfo:
		return "INFO"
	case plog.SeverityNumberWarn:
		return "WARN"
	case plog.SeverityNumberError:
		return "ERROR"
	case plog.SeverityNumberFatal:
		return "FATAL"
	default:
		return "INFO"
	}
}

func (r *LogsGenReceiver) produceLogs(ctx context.Context, currentTime time.Time) uint64 {
	var totalLogs uint64
	wg := sync.WaitGroup{}
	concurrentShardBase := 1

	for idx := range r.scenarios {
		scn := &r.scenarios[idx]
		if scn.config.LogsPerInterval == 0 {
			continue
		}

		mult := resolveVolumeMultiplier(&scn.volume, r.baseRand, scn.config.VolumeProfile)
		effectiveLogs := int(float64(scn.config.LogsPerInterval) * mult)
		if effectiveLogs < 1 && scn.config.LogsPerInterval > 0 {
			effectiveLogs = 1
		}

		if scn.config.Concurrency == 0 {
			shard := r.stats.Shard(0)
			for i := 0; i < scn.config.Scale; i++ {
				resource := scn.resources[i]
				totalLogs += uint64(r.produceLogsForInstance(ctx, r.baseRand, currentTime, *scn, resource, shard, effectiveLogs))
			}
			continue
		}
		scenario := *scn
		scale := scenario.config.Scale
		concurrency := scenario.config.Concurrency
		for i := 0; i < concurrency; i++ {
			rng := r.getNewRand()
			shardIdx := concurrentShardBase + i
			shard := r.stats.Shard(shardIdx)
			workerIdx := i
			wg.Add(1)
			go func(rng *rand.Rand, sh *logstats.LogStats, wi int, logs int) {
				defer wg.Done()
				var count uint64
				for j := 0; j < scale/concurrency; j++ {
					idx := j + wi*scale/concurrency
					resource := scenario.resources[idx]
					count += uint64(r.produceLogsForInstance(ctx, rng, currentTime, scenario, resource, sh, logs))
				}
				atomic.AddUint64(&totalLogs, count)
			}(rng, shard, workerIdx, effectiveLogs)
		}
		concurrentShardBase += concurrency
	}
	wg.Wait()
	return totalLogs
}

func (r *LogsGenReceiver) produceLogsForInstance(ctx context.Context, rng *rand.Rand, currentTime time.Time, scn LogScenario, instanceResource pcommon.Resource, statsShard *logstats.LogStats, logsPerInterval int) int {
	if logsPerInterval <= 0 {
		return 0
	}

	r.obsreport.StartLogsOp(ctx)
	logs := plog.NewLogs()
	rl := logs.ResourceLogs().AppendEmpty()
	instanceResource.CopyTo(rl.Resource())

	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName(scn.prepared.GetScopeName())

	reusableAttrs := make(map[string]string, 8)
	for i := 0; i < logsPerInterval; i++ {
		lr := sl.LogRecords().AppendEmpty()
		instanceTime := addLogJitter(currentTime, r.cfg.IntervalJitterStdDev, r.cfg.Interval, rng)
		lr.SetTimestamp(pcommon.NewTimestampFromTime(instanceTime))

		body, sev := loggen.GenerateFromPreparedInto(rng, scn.prepared, instanceTime, reusableAttrs)
		lr.SetSeverityNumber(sev)
		lr.SetSeverityText(severityText(sev))
		lr.Body().SetStr(body)

		for k, v := range reusableAttrs {
			lr.Attributes().PutStr(k, v)
		}

		// Deterministic needle injection: check each needle (always call rng.Float64 for determinism)
		var replaced bool
		for _, needle := range scn.config.Needles {
			roll := rng.Float64()
			if !replaced && roll < needle.Rate {
				lr.Body().SetStr(needle.Message)
				needleSev := loggen.ParseSeverity(needle.Severity)
				lr.SetSeverityNumber(needleSev)
				lr.SetSeverityText(severityText(needleSev))
				lr.Attributes().PutStr("needle.name", needle.Name)
				for k, v := range needle.Attributes {
					lr.Attributes().PutStr(k, v)
				}
				if cnt := r.needleOccurrences[needle.Name]; cnt != nil {
					cnt.Add(1)
				}
				replaced = true
			}
		}

		statsShard.Record(lr.SeverityText(), instanceResource, lr)

		if scn.config.EmitTraceContext && scn.prepared.HasTraceContext() {
			var traceID [16]byte
			var spanID [8]byte
			rng.Read(traceID[:])
			rng.Read(spanID[:])
			lr.SetTraceID(traceID)
			lr.SetSpanID(spanID)
		}
	}

	logCount := logs.LogRecordCount()
	err := r.nextLogs.ConsumeLogs(ctx, logs)
	r.obsreport.EndLogsOp(ctx, metadata.Type.String(), logCount, err)
	return logCount
}

func (r *LogsGenReceiver) getNewRand() *rand.Rand {
	return rand.New(rand.NewSource(r.baseRand.Int63()))
}

func (r *LogsGenReceiver) Shutdown(_ context.Context) error {
	if r.cancel != nil {
		r.cancel()
	}
	// Build needle occurrences map for summary (only non-zero)
	needleCounts := make(map[string]uint64)
	for name, cnt := range r.needleOccurrences {
		if n := cnt.Load(); n > 0 {
			needleCounts[name] = n
		}
	}
	merged := r.stats.Merge()
	r.settings.Logger.Info(merged.Summary(needleCounts))
	r.settings.Logger.Info("finished generating logs",
		zap.Uint64("logs", r.progress.logCount.Load()),
		zap.String("duration", r.progress.duration().Round(time.Millisecond).String()),
		zap.Float64("logs_per_second", r.progress.logsPerSecond()),
	)
	return nil
}
