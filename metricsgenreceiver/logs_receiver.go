package metricsgenreceiver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strconv"
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
	done              chan struct{}
}

type LogScenario struct {
	config              LogScenarioCfg
	resources           []pcommon.Resource
	prepared            *loggen.PreparedProfile
	volume              volumeState
	instanceMultipliers []float64
}

type volumeState struct {
	multiplier         float64
	remainingIntervals int
}

func diurnalMultiplier(t time.Time, cfg *DiurnalProfileCfg) float64 {
	if cfg == nil {
		return 1.0
	}
	peakMult := cfg.PeakMultiplier
	if peakMult <= 0 {
		peakMult = 3.0
	}
	troughMult := cfg.TroughMultiplier
	if troughMult <= 0 {
		troughMult = 0.2
	}
	peakH := cfg.PeakHour
	troughH := cfg.TroughHour
	hour := t.Hour()
	phase := (hour - peakH + 24) % 24
	troughPhase := (troughH - peakH + 24) % 24
	var angle float64
	if phase <= troughPhase {
		angle = math.Pi * float64(phase) / float64(troughPhase)
	} else {
		angle = math.Pi + math.Pi*float64(phase-troughPhase)/float64(24-troughPhase)
	}
	base := troughMult + (peakMult-troughMult)*(math.Cos(angle)+1)/2
	baseUnix := t.UnixNano()
	maxMult := base
	for _, cb := range cfg.CronBursts {
		mod := baseUnix % int64(cb.Interval)
		if mod < 0 {
			mod += int64(cb.Interval)
		}
		if mod < int64(cb.Duration) && cb.Multiplier > maxMult {
			maxMult = cb.Multiplier
		}
	}
	return maxMult
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

func applyInstanceMultiplier(baseLogs int, multipliers []float64, idx int) int {
	if multipliers == nil {
		return baseLogs
	}
	n := int(math.Round(float64(baseLogs) * multipliers[idx]))
	if n < 1 && baseLogs > 0 {
		n = 1
	}
	return n
}

// buildInstanceMultipliers pre-computes a per-instance volume multiplier using
// a log-normal distribution seeded from rng. The returned slice has length
// equal to scale; each element is a multiplier around 1.0. When sigma is 0 the
// slice is nil (flat distribution, no extra work on the hot path).
func buildInstanceMultipliers(rng *rand.Rand, scale int, sigma float64) []float64 {
	if sigma == 0 || scale == 0 {
		return nil
	}
	m := make([]float64, scale)
	sum := 0.0
	for i := range m {
		m[i] = math.Exp(sigma * rng.NormFloat64())
		sum += m[i]
	}
	mean := sum / float64(scale)
	for i := range m {
		m[i] /= mean
	}
	return m
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
		var ipCfg *loggen.IPPoolConfig
		if scn.IPPool != nil {
			ipCfg = &loggen.IPPoolConfig{
				CIDRs:    scn.IPPool.CIDRs,
				ZipfSkew: scn.IPPool.ZipfSkew,
			}
		}
		profile := loggen.GetAppProfile(scn.Path, baseRand, ipCfg, scn.Scale)
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

	for i := range scenarios {
		scenarios[i].instanceMultipliers = buildInstanceMultipliers(baseRand, scenarios[i].config.Scale, scenarios[i].config.InstanceVolumeSkew)
	}

	needleOccurrences := make(map[string]*atomic.Uint64, len(needleNames))
	for name := range needleNames {
		needleOccurrences[name] = &atomic.Uint64{}
	}

	// Shard 0: sequential scenarios (Concurrency==0). Shards 1+:
	// unique shard per concurrent worker across all scenarios (they run in parallel).
	// Track field cardinality on shard 0 plus the first worker shard of each
	// concurrent scenario so every scenario contributes to the cardinality report.
	totalConcurrentShards := 0
	cardinalityShards := []int{0}
	for _, scn := range cfg.LogScenarios {
		if scn.Concurrency > 0 {
			cardinalityShards = append(cardinalityShards, 1+totalConcurrentShards)
			totalConcurrentShards += scn.Concurrency
		}
	}
	numShards := 1 + totalConcurrentShards
	if numShards < 1 {
		numShards = 1
	}
	stats := logstats.NewShardedLogStats(numShards, cardinalityShards)

	return &LogsGenReceiver{
		cfg:               cfg,
		settings:          set,
		baseRand:          baseRand,
		obsreport:         obsreport,
		scenarios:         scenarios,
		progress:          newLogsProgress(),
		needleOccurrences: needleOccurrences,
		stats:             stats,
		done:              make(chan struct{}),
	}, nil
}

func (r *LogsGenReceiver) Start(ctx context.Context, host component.Host) error {
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	go func() {
		defer close(r.done)
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

		diurnalMult := diurnalMultiplier(currentTime, scn.config.DiurnalProfile)
		volumeMult := resolveVolumeMultiplier(&scn.volume, r.baseRand, scn.config.VolumeProfile)
		effectiveLogs := int(float64(scn.config.LogsPerInterval) * diurnalMult * volumeMult)
		if effectiveLogs < 1 && scn.config.LogsPerInterval > 0 {
			effectiveLogs = 1
		}

		if scn.config.Concurrency == 0 {
			shard := r.stats.Shard(0)
			reusableAttrs := make(map[string]any, 8)
			argsBuf := make([]any, scn.prepared.MaxArgs())
			var bodyBuf []byte
			r.obsreport.StartLogsOp(ctx)
			logs := plog.NewLogs()
			for i := 0; i < scn.config.Scale; i++ {
				instanceLogs := applyInstanceMultiplier(effectiveLogs, scn.instanceMultipliers, i)
				if instanceLogs <= 0 {
					continue
				}
				bodyBuf = r.appendInstanceLogs(r.baseRand, currentTime, *scn, scn.resources[i], shard, instanceLogs, reusableAttrs, argsBuf, bodyBuf, &logs)
			}
			logCount := logs.LogRecordCount()
			totalLogs += uint64(logCount)
			err := r.nextLogs.ConsumeLogs(ctx, logs)
			r.obsreport.EndLogsOp(ctx, metadata.Type.String(), logCount, err)
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
				reusableAttrs := make(map[string]any, 8)
				argsBuf := make([]any, scenario.prepared.MaxArgs())
				var bodyBuf []byte
				r.obsreport.StartLogsOp(ctx)
				batch := plog.NewLogs()
				for j := 0; j < scale/concurrency; j++ {
					idx := j + wi*scale/concurrency
					instanceLogs := applyInstanceMultiplier(logs, scenario.instanceMultipliers, idx)
					if instanceLogs <= 0 {
						continue
					}
					bodyBuf = r.appendInstanceLogs(rng, currentTime, scenario, scenario.resources[idx], sh, instanceLogs, reusableAttrs, argsBuf, bodyBuf, &batch)
				}
				logCount := batch.LogRecordCount()
				atomic.AddUint64(&totalLogs, uint64(logCount))
				err := r.nextLogs.ConsumeLogs(ctx, batch)
				r.obsreport.EndLogsOp(ctx, metadata.Type.String(), logCount, err)
			}(rng, shard, workerIdx, effectiveLogs)
		}
		concurrentShardBase += concurrency
	}
	wg.Wait()
	return totalLogs
}

// appendInstanceLogs generates log records for a single instance and appends
// them as a new ResourceLogs entry into the provided batch. This avoids
// allocating a separate plog.Logs per instance.
func (r *LogsGenReceiver) appendInstanceLogs(rng *rand.Rand, currentTime time.Time, scn LogScenario, instanceResource pcommon.Resource, statsShard *logstats.LogStats, logsPerInterval int, reusableAttrs map[string]any, argsBuf []any, bodyBuf []byte, batch *plog.Logs) []byte {
	rl := batch.ResourceLogs().AppendEmpty()
	instanceResource.CopyTo(rl.Resource())

	sl := rl.ScopeLogs().AppendEmpty()
	sl.Scope().SetName(scn.prepared.GetScopeName())

	for i := 0; i < logsPerInterval; i++ {
		lr := sl.LogRecords().AppendEmpty()
		instanceTime := addLogJitter(currentTime, r.cfg.IntervalJitterStdDev, r.cfg.Interval, rng)
		lr.SetTimestamp(pcommon.NewTimestampFromTime(instanceTime))

		var body string
		var sev plog.SeverityNumber
		body, sev, bodyBuf = loggen.GenerateFromPreparedInto(rng, scn.prepared, instanceTime, reusableAttrs, argsBuf, bodyBuf)
		lr.SetSeverityNumber(sev)
		lr.SetSeverityText(severityText(sev))
		lr.Body().SetStr(body)

		for k, v := range reusableAttrs {
			switch val := v.(type) {
			case nil:
				continue
			case int:
				lr.Attributes().PutInt(k, int64(val))
			case int64:
				lr.Attributes().PutInt(k, val)
			case float64:
				lr.Attributes().PutDouble(k, val)
			case string:
				lr.Attributes().PutStr(k, val)
			case bool:
				lr.Attributes().PutBool(k, val)
			case []any:
				sl := lr.Attributes().PutEmptySlice(k)
				sl.EnsureCapacity(len(val))
				for _, elem := range val {
					switch e := elem.(type) {
					case int:
						sl.AppendEmpty().SetInt(int64(e))
					case int64:
						sl.AppendEmpty().SetInt(e)
					case float64:
						sl.AppendEmpty().SetDouble(e)
					case string:
						sl.AppendEmpty().SetStr(e)
					case bool:
						sl.AppendEmpty().SetBool(e)
					default:
						sl.AppendEmpty().SetStr(anyToString(e))
					}
				}
			default:
				lr.Attributes().PutStr(k, anyToString(v))
			}
		}

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

	return bodyBuf
}

func (r *LogsGenReceiver) getNewRand() *rand.Rand {
	return rand.New(rand.NewSource(r.baseRand.Int63()))
}

// anyToString converts an unknown value to string without fmt.Sprintf allocation
// overhead. Covers the realistic types our generators may produce; the fmt
// fallback exists only as a safety net for truly unexpected types.
func anyToString(v any) string {
	switch val := v.(type) {
	case int:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	case string:
		return val
	default:
		return fmt.Sprintf("%v", v)
	}
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
