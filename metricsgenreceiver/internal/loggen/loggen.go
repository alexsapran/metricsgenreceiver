package loggen

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

// ParseSeverity converts a severity string to plog.SeverityNumber.
// Empty or unknown values default to SeverityNumberError.
func ParseSeverity(s string) plog.SeverityNumber {
	switch strings.ToUpper(s) {
	case "INFO":
		return plog.SeverityNumberInfo
	case "WARN":
		return plog.SeverityNumberWarn
	case "ERROR":
		return plog.SeverityNumberError
	case "FATAL":
		return plog.SeverityNumberFatal
	default:
		return plog.SeverityNumberError
	}
}

// AppProfile defines a log-generating application's behavior.
type AppProfile struct {
	Name string
	// Messages contains all message templates. GenerateLogRecord picks by severity.
	Messages []MessageTemplate
	// SeverityWeights: cumulative weights for INFO, WARN, ERROR, FATAL.
	// e.g. [70, 90, 98, 100] means 70% INFO, 20% WARN, 8% ERROR, 2% FATAL
	SeverityWeights [4]int
	// EmitTraceContext controls whether trace_id and span_id are set on log records.
	// Only profiles representing instrumented applications (e.g. Go with OTel SDK)
	// should set this to true.
	EmitTraceContext bool
}

// MessageTemplate is a log message pattern with its severity.
type MessageTemplate struct {
	Severity plog.SeverityNumber
	Format   string         // format string with %s/%d/%v placeholders
	Args     []ArgGenerator // generators for each placeholder, in order
	// Attrs are optional record-level attributes. Use AttrFromArg to reuse format args.
	Attrs       map[string]ArgGenerator
	AttrFromArg map[string]int // attr key -> index into Args (reuse same value for consistency)
}

// ArgGenerator produces a random argument for a message template placeholder.
// GenContext is passed for generators that need the timestamp (e.g. Timestamp).
type GenContext struct {
	Timestamp time.Time
}

type ArgGenerator func(rng *rand.Rand, ctx *GenContext) any

// GenerateLogRecord picks a message template by severity, fills placeholders,
// and returns the log body, severity, and record-level attributes.
func GenerateLogRecord(rng *rand.Rand, profile AppProfile, timestamp time.Time) (body string, severity plog.SeverityNumber, attrs map[string]string) {
	ctx := &GenContext{Timestamp: timestamp}
	sev := pickSeverityFromWeights(rng, profile.SeverityWeights)
	msgs := filterMessagesBySeverity(profile.Messages, sev)
	if len(msgs) == 0 {
		// fallback: use first INFO message if any
		for _, m := range profile.Messages {
			if m.Severity == plog.SeverityNumberInfo {
				msgs = append(msgs, m)
				break
			}
		}
		if len(msgs) == 0 && len(profile.Messages) > 0 {
			msgs = profile.Messages[:1]
			sev = profile.Messages[0].Severity
		}
	}
	if len(msgs) == 0 {
		return "no messages configured", plog.SeverityNumberInfo, nil
	}
	tmpl := msgs[rng.Intn(len(msgs))]
	args := make([]any, len(tmpl.Args))
	for i, gen := range tmpl.Args {
		args[i] = gen(rng, ctx)
	}
	body = fmt.Sprintf(tmpl.Format, args...)
	attrs = nil
	if len(tmpl.AttrFromArg) > 0 || len(tmpl.Attrs) > 0 {
		attrs = make(map[string]string)
		for k, idx := range tmpl.AttrFromArg {
			if idx >= 0 && idx < len(args) {
				attrs[k] = fmt.Sprintf("%v", args[idx])
			}
		}
		for k, gen := range tmpl.Attrs {
			if _, ok := attrs[k]; ok {
				continue
			}
			switch v := gen(rng, ctx).(type) {
			case string:
				attrs[k] = v
			case int:
				attrs[k] = fmt.Sprintf("%d", v)
			case int64:
				attrs[k] = fmt.Sprintf("%d", v)
			default:
				attrs[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	return body, tmpl.Severity, attrs
}

func pickSeverityFromWeights(rng *rand.Rand, w [4]int) plog.SeverityNumber {
	n := rng.Intn(100)
	severities := [4]plog.SeverityNumber{
		plog.SeverityNumberInfo,
		plog.SeverityNumberWarn,
		plog.SeverityNumberError,
		plog.SeverityNumberFatal,
	}
	for i, weight := range w {
		if n < weight {
			return severities[i]
		}
	}
	return plog.SeverityNumberInfo
}

func filterMessagesBySeverity(msgs []MessageTemplate, sev plog.SeverityNumber) []MessageTemplate {
	var out []MessageTemplate
	for _, m := range msgs {
		if m.Severity == sev {
			out = append(out, m)
		}
	}
	return out
}

// PreparedProfile holds a profile with pre-bucketed messages by severity for fast lookup.
type PreparedProfile struct {
	profile    AppProfile
	bySeverity map[plog.SeverityNumber][]MessageTemplate
}

// PrepareProfile pre-computes severity-bucketed message slices to avoid per-record allocations.
func PrepareProfile(p *AppProfile) *PreparedProfile {
	bySev := make(map[plog.SeverityNumber][]MessageTemplate)
	for _, m := range p.Messages {
		bySev[m.Severity] = append(bySev[m.Severity], m)
	}
	return &PreparedProfile{
		profile:    *p,
		bySeverity: bySev,
	}
}

// HasTraceContext returns whether log records from this profile should include trace/span IDs.
func (pp *PreparedProfile) HasTraceContext() bool {
	return pp.profile.EmitTraceContext
}

// GenerateFromPrepared generates a log record using pre-bucketed messages.
func GenerateFromPrepared(rng *rand.Rand, pp *PreparedProfile, timestamp time.Time) (body string, severity plog.SeverityNumber, attrs map[string]string) {
	ctx := &GenContext{Timestamp: timestamp}
	sev := pickSeverityFromWeights(rng, pp.profile.SeverityWeights)
	msgs := pp.bySeverity[sev]
	if len(msgs) == 0 {
		msgs = pp.bySeverity[plog.SeverityNumberInfo]
		if len(msgs) == 0 && len(pp.profile.Messages) > 0 {
			msgs = pp.profile.Messages[:1]
			sev = pp.profile.Messages[0].Severity
		}
	}
	if len(msgs) == 0 {
		return "no messages configured", plog.SeverityNumberInfo, nil
	}
	tmpl := msgs[rng.Intn(len(msgs))]
	args := make([]any, len(tmpl.Args))
	for i, gen := range tmpl.Args {
		args[i] = gen(rng, ctx)
	}
	body = fmt.Sprintf(tmpl.Format, args...)
	attrs = nil
	if len(tmpl.AttrFromArg) > 0 || len(tmpl.Attrs) > 0 {
		attrs = make(map[string]string)
		for k, idx := range tmpl.AttrFromArg {
			if idx >= 0 && idx < len(args) {
				attrs[k] = fmt.Sprintf("%v", args[idx])
			}
		}
		for k, gen := range tmpl.Attrs {
			if _, ok := attrs[k]; ok {
				continue
			}
			switch v := gen(rng, ctx).(type) {
			case string:
				attrs[k] = v
			case int:
				attrs[k] = fmt.Sprintf("%d", v)
			case int64:
				attrs[k] = fmt.Sprintf("%d", v)
			default:
				attrs[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	return body, tmpl.Severity, attrs
}

// GenerateFromPreparedInto generates a log record into a reusable attrs map to avoid allocations.
// attrsOut must be non-nil; it is cleared and reused.
func GenerateFromPreparedInto(rng *rand.Rand, pp *PreparedProfile, timestamp time.Time, attrsOut map[string]string) (body string, severity plog.SeverityNumber) {
	for k := range attrsOut {
		delete(attrsOut, k)
	}
	ctx := &GenContext{Timestamp: timestamp}
	sev := pickSeverityFromWeights(rng, pp.profile.SeverityWeights)
	msgs := pp.bySeverity[sev]
	if len(msgs) == 0 {
		msgs = pp.bySeverity[plog.SeverityNumberInfo]
		if len(msgs) == 0 && len(pp.profile.Messages) > 0 {
			msgs = pp.profile.Messages[:1]
			sev = pp.profile.Messages[0].Severity
		}
	}
	if len(msgs) == 0 {
		return "no messages configured", plog.SeverityNumberInfo
	}
	tmpl := msgs[rng.Intn(len(msgs))]
	args := make([]any, len(tmpl.Args))
	for i, gen := range tmpl.Args {
		args[i] = gen(rng, ctx)
	}
	body = fmt.Sprintf(tmpl.Format, args...)
	if len(tmpl.AttrFromArg) > 0 || len(tmpl.Attrs) > 0 {
		for k, idx := range tmpl.AttrFromArg {
			if idx >= 0 && idx < len(args) {
				attrsOut[k] = fmt.Sprintf("%v", args[idx])
			}
		}
		for k, gen := range tmpl.Attrs {
			if _, ok := attrsOut[k]; ok {
				continue
			}
			switch v := gen(rng, ctx).(type) {
			case string:
				attrsOut[k] = v
			case int:
				attrsOut[k] = fmt.Sprintf("%d", v)
			case int64:
				attrsOut[k] = fmt.Sprintf("%d", v)
			default:
				attrsOut[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	return body, tmpl.Severity
}

// --- ArgGenerator helpers ---

var RandomIP ArgGenerator = func(rng *rand.Rand, _ *GenContext) any {
	return net.IPv4(byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256))).String()
}

func RandomPath(paths []string) ArgGenerator {
	return func(r *rand.Rand, _ *GenContext) any { return paths[r.Intn(len(paths))] }
}

// RandomPathWithSuffix appends a random suffix (e.g. ID) to a randomly chosen base path.
func RandomPathWithSuffix(bases []string, suffixGen ArgGenerator) ArgGenerator {
	return func(r *rand.Rand, ctx *GenContext) any {
		base := bases[r.Intn(len(bases))]
		suffix := suffixGen(r, ctx)
		return base + fmt.Sprintf("%v", suffix)
	}
}

func Static(s string) ArgGenerator {
	return func(*rand.Rand, *GenContext) any { return s }
}

var RandomHTTPStatus ArgGenerator = func(rng *rand.Rand, _ *GenContext) any {
	// Realistic distribution: mostly 200, some 201, 301, 304, 400, 404, 500, 502, 503
	weights := []struct {
		status int
		weight int
	}{
		{200, 70}, {201, 8}, {301, 3}, {304, 4}, {400, 2}, {404, 5}, {500, 3}, {502, 2}, {503, 3},
	}
	total := 0
	for _, w := range weights {
		total += w.weight
	}
	n := rng.Intn(total)
	for _, w := range weights {
		n -= w.weight
		if n < 0 {
			return w.status
		}
	}
	return 200
}

var RandomBytes ArgGenerator = func(rng *rand.Rand, _ *GenContext) any {
	// Common response sizes: 0, 15 (health), small, medium, large
	n := rng.Intn(100)
	switch {
	case n < 10:
		return 0
	case n < 25:
		return rng.Intn(100) + 10
	case n < 60:
		return rng.Intn(2000) + 100
	default:
		return rng.Intn(50000) + 2000
	}
}

func RandomDuration(minMs, maxMs int) ArgGenerator {
	return func(r *rand.Rand, _ *GenContext) any {
		return r.Intn(maxMs-minMs+1) + minMs
	}
}

func RandomID(length int) ArgGenerator {
	const hexChars = "0123456789abcdef"
	return func(r *rand.Rand, _ *GenContext) any {
		b := make([]byte, length)
		for i := range b {
			b[i] = hexChars[r.Intn(16)]
		}
		return string(b)
	}
}

var RandomUserAgent ArgGenerator = func(rng *rand.Rand, _ *GenContext) any {
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36",
		"curl/8.4.0",
		"kube-probe/1.28",
		"Prometheus/2.45.0",
		"Googlebot/2.1",
		"PostmanRuntime/7.32.3",
	}
	return userAgents[rng.Intn(len(userAgents))]
}

func Timestamp(layout string) ArgGenerator {
	return func(_ *rand.Rand, ctx *GenContext) any {
		return ctx.Timestamp.Format(layout)
	}
}

func RandomFrom(choices ...string) ArgGenerator {
	return func(r *rand.Rand, _ *GenContext) any {
		return choices[r.Intn(len(choices))]
	}
}

func RandomInt(min, max int) ArgGenerator {
	return func(r *rand.Rand, _ *GenContext) any {
		return r.Intn(max-min+1) + min
	}
}

// RandomFromInt returns an ArgGenerator that picks from the given integers.
func RandomFromInt(choices ...int) ArgGenerator {
	return func(r *rand.Rand, _ *GenContext) any {
		return choices[r.Intn(len(choices))]
	}
}

// HTTPMethod returns an ArgGenerator for HTTP method (for attrs).
func HTTPMethod(method string) ArgGenerator {
	return func(*rand.Rand, *GenContext) any { return method }
}

// HTTPStatus returns an ArgGenerator that yields the given status (for attrs).
func HTTPStatus(status int) ArgGenerator {
	return func(*rand.Rand, *GenContext) any { return status }
}
