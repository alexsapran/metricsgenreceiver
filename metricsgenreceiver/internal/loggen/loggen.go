package loggen

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/plog"
)

const hexChars = "0123456789abcdef"

// randomHexString generates a hex string of the given length using rng.
func randomHexString(rng *rand.Rand, length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = hexChars[rng.Intn(16)]
	}
	return string(b)
}

// ParseSeverity converts a severity string to plog.SeverityNumber.
// Empty or unknown values default to SeverityNumberError.
func ParseSeverity(s string) plog.SeverityNumber {
	switch strings.ToUpper(s) {
	case "TRACE":
		return plog.SeverityNumberTrace
	case "DEBUG":
		return plog.SeverityNumberDebug
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

// DefaultSeverityWeights returns realistic production severity distribution:
// TRACE 0%, DEBUG 3%, INFO 82%, WARN 8%, ERROR 7%, FATAL 0%
func DefaultSeverityWeights() [6]int {
	return [6]int{0, 3, 85, 93, 100, 100}
}

// AppProfile defines a log-generating application's behavior.
type AppProfile struct {
	Name string
	// ScopeName is the instrumentation scope name for log records from this profile.
	ScopeName string
	// Messages contains all message templates. GenerateLogRecord picks by severity.
	Messages []MessageTemplate
	// SeverityWeights: cumulative weights for TRACE, DEBUG, INFO, WARN, ERROR, FATAL.
	// e.g. [0, 3, 85, 93, 100, 100] means 0% TRACE, 3% DEBUG, 82% INFO, 8% WARN, 7% ERROR, 0% FATAL
	SeverityWeights [6]int
	// EmitTraceContext controls whether trace_id and span_id are set on log records.
	// Only profiles representing instrumented applications (e.g. Go with OTel SDK)
	// should set this to true.
	EmitTraceContext bool
	// LongTail holds pre-computed rare fields (<1% presence) emitted via a fast
	// single-rng-call-per-field path. Nil means no long-tail fields.
	LongTail *LongTailSet
}

// AttrGen pairs an attribute key with its generator, used in ordered slices
// to ensure deterministic rng consumption regardless of Go map iteration order.
type AttrGen struct {
	Key string
	Gen ArgGenerator
}

// RareAttrGen describes a rarely-present attribute. The generator is always
// invoked (to keep the rng stream deterministic) but the value is only emitted
// when the probability roll succeeds.
type RareAttrGen struct {
	Key         string
	Probability float64
	Gen         ArgGenerator
}

// MessageTemplate is a log message pattern with its severity.
type MessageTemplate struct {
	Severity plog.SeverityNumber
	Format   string         // format string with %s/%d/%v placeholders
	Args     []ArgGenerator // generators for each placeholder, in order
	// Attrs are optional record-level attributes in deterministic order.
	Attrs       []AttrGen
	AttrFromArg map[string]int // attr key -> index into Args (reuse same value for consistency)
	// RareAttrs are low-presence attributes (<1%). Always consume rng, conditionally emit.
	RareAttrs []RareAttrGen
}

// ArgGenerator produces a random argument for a message template placeholder.
// GenContext is passed for generators that need the timestamp (e.g. Timestamp).
type GenContext struct {
	Timestamp time.Time
}

type ArgGenerator func(rng *rand.Rand, ctx GenContext) any

// GenerateLogRecord picks a message template by severity, fills placeholders,
// and returns the log body, severity, and record-level attributes.
func GenerateLogRecord(rng *rand.Rand, profile AppProfile, timestamp time.Time) (body string, severity plog.SeverityNumber, attrs map[string]any) {
	ctx := GenContext{Timestamp: timestamp}
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
	hasAttrs := len(tmpl.AttrFromArg) > 0 || len(tmpl.Attrs) > 0 || len(tmpl.RareAttrs) > 0
	attrs = nil
	if hasAttrs {
		attrs = make(map[string]any)
		for k, idx := range tmpl.AttrFromArg {
			if idx >= 0 && idx < len(args) {
				v := args[idx]
				if tpl, ok := RouteTemplate(v); ok {
					attrs[k] = tpl
				} else {
					attrs[k] = v
				}
			}
		}
		for _, ag := range tmpl.Attrs {
			if _, ok := attrs[ag.Key]; ok {
				continue
			}
			v := ag.Gen(rng, ctx)
			if v != nil {
				attrs[ag.Key] = v
			}
		}
		for _, ra := range tmpl.RareAttrs {
			v := ra.Gen(rng, ctx)
			if rng.Float64() < ra.Probability {
				attrs[ra.Key] = v
			}
		}
	}
	return body, tmpl.Severity, attrs
}

func pickSeverityFromWeights(rng *rand.Rand, w [6]int) plog.SeverityNumber {
	n := rng.Intn(100)
	severities := [6]plog.SeverityNumber{
		plog.SeverityNumberTrace,
		plog.SeverityNumberDebug,
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
	longTail   *LongTailSet
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
		longTail:   p.LongTail,
	}
}

// HasTraceContext returns whether log records from this profile should include trace/span IDs.
func (pp *PreparedProfile) HasTraceContext() bool {
	return pp.profile.EmitTraceContext
}

// GetScopeName returns the instrumentation scope name for this profile.
func (pp *PreparedProfile) GetScopeName() string {
	if pp.profile.ScopeName != "" {
		return pp.profile.ScopeName
	}
	return "log-generator"
}

// MaxArgs returns the maximum number of arguments across all message templates,
// useful for pre-allocating a reusable args buffer.
func (pp *PreparedProfile) MaxArgs() int {
	maxA := 0
	for _, m := range pp.profile.Messages {
		if len(m.Args) > maxA {
			maxA = len(m.Args)
		}
	}
	return maxA
}

// OverrideSeverityWeights replaces the profile's severity weights.
func (pp *PreparedProfile) OverrideSeverityWeights(w [6]int) {
	pp.profile.SeverityWeights = w
}

// GenerateFromPreparedInto generates a log record into a reusable attrs map to avoid allocations.
// attrsOut must be non-nil; it is cleared and reused. argsBuf is a reusable slice for template
// arguments (cap >= MaxArgs()). bodyBuf is a reusable byte buffer for body formatting; the
// returned bodyBuf should be passed back to subsequent calls to retain the grown capacity.
func GenerateFromPreparedInto(rng *rand.Rand, pp *PreparedProfile, timestamp time.Time, attrsOut map[string]any, argsBuf []any, bodyBuf []byte) (body string, severity plog.SeverityNumber, bodyBufOut []byte) {
	for k := range attrsOut {
		delete(attrsOut, k)
	}
	ctx := GenContext{Timestamp: timestamp}
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
		return "no messages configured", plog.SeverityNumberInfo, bodyBuf
	}
	tmpl := msgs[rng.Intn(len(msgs))]
	args := argsBuf[:0]
	if cap(argsBuf) >= len(tmpl.Args) {
		args = argsBuf[:len(tmpl.Args)]
	} else {
		args = make([]any, len(tmpl.Args))
	}
	for i, gen := range tmpl.Args {
		args[i] = gen(rng, ctx)
	}
	// Uses sprintfSimple instead of fmt.Sprintf to avoid reflection overhead (see definition below).
	body, bodyBuf = sprintfSimple(bodyBuf, tmpl.Format, args)
	if len(tmpl.AttrFromArg) > 0 || len(tmpl.Attrs) > 0 || len(tmpl.RareAttrs) > 0 || pp.longTail != nil {
		for k, idx := range tmpl.AttrFromArg {
			if idx >= 0 && idx < len(args) {
				v := args[idx]
				if tpl, ok := RouteTemplate(v); ok {
					attrsOut[k] = tpl
				} else {
					attrsOut[k] = v
				}
			}
		}
		for _, ag := range tmpl.Attrs {
			if _, ok := attrsOut[ag.Key]; ok {
				continue
			}
			v := ag.Gen(rng, ctx)
			if v != nil {
				attrsOut[ag.Key] = v
			}
		}
		for _, ra := range tmpl.RareAttrs {
			v := ra.Gen(rng, ctx)
			if rng.Float64() < ra.Probability {
				attrsOut[ra.Key] = v
			}
		}
		if pp.longTail != nil {
			pp.longTail.EmitLongTail(rng, attrsOut)
		}
	}
	return body, tmpl.Severity, bodyBuf
}

// sprintfSimple is a fast-path replacement for fmt.Sprintf that avoids the
// reflection and format-string parsing overhead of the standard library.
// It only supports the verbs used in our log message format strings:
// %s, %d, %x, %q, %v, and %%. It does NOT support width, precision, or flags.
// The buf is reused across calls to minimise allocations; callers should keep
// the returned (grown) slice for the next call.
func sprintfSimple(buf []byte, format string, args []any) (string, []byte) {
	buf = buf[:0]
	argIdx := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			buf = append(buf, format[i])
			continue
		}
		i++
		switch format[i] {
		case 's':
			buf = appendAnyStr(buf, args[argIdx])
			argIdx++
		case 'd':
			buf = appendAnyInt(buf, args[argIdx])
			argIdx++
		case 'x':
			buf = appendAnyHex(buf, args[argIdx])
			argIdx++
		case 'q':
			buf = strconv.AppendQuote(buf, anyStr(args[argIdx]))
			argIdx++
		case 'v':
			buf = appendAnyStr(buf, args[argIdx])
			argIdx++
		case '%':
			buf = append(buf, '%')
		default:
			buf = append(buf, '%', format[i])
		}
	}
	return string(buf), buf
}

func appendAnyStr(buf []byte, v any) []byte {
	switch s := v.(type) {
	case string:
		return append(buf, s...)
	case fmt.Stringer:
		return append(buf, s.String()...)
	case int:
		return strconv.AppendInt(buf, int64(s), 10)
	default:
		return append(buf, fmt.Sprint(v)...)
	}
}

func appendAnyInt(buf []byte, v any) []byte {
	switch n := v.(type) {
	case int:
		return strconv.AppendInt(buf, int64(n), 10)
	case int64:
		return strconv.AppendInt(buf, n, 10)
	case string:
		return append(buf, n...)
	default:
		return append(buf, fmt.Sprint(v)...)
	}
}

func appendAnyHex(buf []byte, v any) []byte {
	switch n := v.(type) {
	case int:
		return strconv.AppendInt(buf, int64(n), 16)
	case int64:
		return strconv.AppendInt(buf, n, 16)
	case uint64:
		return strconv.AppendUint(buf, n, 16)
	default:
		return append(buf, fmt.Sprint(v)...)
	}
}

func anyStr(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case fmt.Stringer:
		return s.String()
	default:
		return fmt.Sprint(v)
	}
}

// --- ArgGenerator helpers ---

// IPPoolConfig holds optional IP pool configuration for ZipfianIP.
type IPPoolConfig struct {
	CIDRs    []string // CIDR ranges to draw IPs from (default: ["10.0.0.0/8"])
	PoolSize int      // number of IPs in the pool (default: scale * 10, minimum 500)
	ZipfSkew float64  // Zipf s parameter (default: 1.5); higher = more skewed
}

// buildIPPool generates a deterministic pool of IP strings from the given CIDRs.
func buildIPPool(rng *rand.Rand, cidrs []string, poolSize int) []string {
	type cidrRange struct {
		base    uint32
		hostMax uint32
	}
	ranges := make([]cidrRange, 0, len(cidrs))
	for _, c := range cidrs {
		_, ipNet, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		ip4 := ipNet.IP.To4()
		if ip4 == nil {
			continue
		}
		mask := ipNet.Mask
		base := uint32(ip4[0])<<24 | uint32(ip4[1])<<16 | uint32(ip4[2])<<8 | uint32(ip4[3])
		inverseMask := ^(uint32(mask[0])<<24 | uint32(mask[1])<<16 | uint32(mask[2])<<8 | uint32(mask[3]))
		ranges = append(ranges, cidrRange{base: base, hostMax: inverseMask})
	}
	if len(ranges) == 0 {
		ranges = append(ranges, cidrRange{base: 0x0A000000, hostMax: 0x00FFFFFF})
	}

	pool := make([]string, poolSize)
	for i := 0; i < poolSize; i++ {
		cr := ranges[rng.Intn(len(ranges))]
		host := uint32(rng.Int63n(int64(cr.hostMax))) + 1
		ip := cr.base | host
		pool[i] = net.IPv4(byte(ip>>24), byte(ip>>16), byte(ip>>8), byte(ip)).String()
	}
	return pool
}

const zipfSelectionSize = 4096

func ZipfianIP(poolSize int, rng *rand.Rand, cfg *IPPoolConfig) ArgGenerator {
	cidrs := []string{"10.0.0.0/8"}
	skew := 1.5
	if cfg != nil {
		if len(cfg.CIDRs) > 0 {
			cidrs = cfg.CIDRs
		}
		if cfg.ZipfSkew > 1.0 {
			skew = cfg.ZipfSkew
		}
		if cfg.PoolSize > 0 {
			poolSize = cfg.PoolSize
		}
	}
	if poolSize < 1 {
		poolSize = 500
	}

	pool := buildIPPool(rng, cidrs, poolSize)

	// Pre-compute Zipfian selection indices to avoid per-call NewZipf overhead.
	selection := make([]string, zipfSelectionSize)
	zipf := rand.NewZipf(rng, skew, 1, uint64(poolSize-1))
	for i := range selection {
		selection[i] = pool[zipf.Uint64()]
	}
	return func(r *rand.Rand, _ GenContext) any {
		return selection[r.Intn(zipfSelectionSize)]
	}
}

func RandomPath(paths []string) ArgGenerator {
	return func(r *rand.Rand, _ GenContext) any { return paths[r.Intn(len(paths))] }
}

// RandomPathWithSuffix appends a random suffix (e.g. ID) to a randomly chosen base path.
func RandomPathWithSuffix(bases []string, suffixGen ArgGenerator) ArgGenerator {
	return func(r *rand.Rand, ctx GenContext) any {
		base := bases[r.Intn(len(bases))]
		suffix := suffixGen(r, ctx)
		return base + fmt.Sprintf("%v", suffix)
	}
}

// routeWithTemplate holds a full URL for the log body and the route template for http.url attribute.
// Implements fmt.Stringer to render the body when used in format strings.
type routeWithTemplate struct {
	body    string
	template string
}

func (r routeWithTemplate) String() string { return r.body }

// RouteWithRandomID returns an ArgGenerator that picks a route template, substitutes {id}
// with a random ID for the body, and returns routeWithTemplate so AttrFromArg for http.url
// can extract the low-cardinality template. Templates use {id} as placeholder.
func RouteWithRandomID(templates []string) ArgGenerator {
	return func(r *rand.Rand, ctx GenContext) any {
		tpl := templates[r.Intn(len(templates))]
		id := RandomID(8)(r, ctx).(string)
		body := strings.ReplaceAll(tpl, "{id}", id)
		return routeWithTemplate{body: body, template: tpl}
	}
}

// RouteTemplate extracts the template from routeWithTemplate for attr storage.
func RouteTemplate(v any) (string, bool) {
	if r, ok := v.(routeWithTemplate); ok {
		return r.template, true
	}
	return "", false
}

func Static(s string) ArgGenerator {
	return func(*rand.Rand, GenContext) any { return s }
}

var RandomHTTPStatus ArgGenerator = func(rng *rand.Rand, _ GenContext) any {
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

var RandomBytes ArgGenerator = func(rng *rand.Rand, _ GenContext) any {
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
	return func(r *rand.Rand, _ GenContext) any {
		return r.Intn(maxMs-minMs+1) + minMs
	}
}

func RandomID(length int) ArgGenerator {
	return func(r *rand.Rand, _ GenContext) any {
		return randomHexString(r, length)
	}
}

var RandomUserAgent ArgGenerator = func(rng *rand.Rand, _ GenContext) any {
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
	return func(_ *rand.Rand, ctx GenContext) any {
		return ctx.Timestamp.Format(layout)
	}
}

func RandomFrom(choices ...string) ArgGenerator {
	return func(r *rand.Rand, _ GenContext) any {
		return choices[r.Intn(len(choices))]
	}
}

func RandomInt(min, max int) ArgGenerator {
	return func(r *rand.Rand, _ GenContext) any {
		return r.Intn(max-min+1) + min
	}
}

// RandomFromInt returns an ArgGenerator that picks from the given integers.
func RandomFromInt(choices ...int) ArgGenerator {
	return func(r *rand.Rand, _ GenContext) any {
		return choices[r.Intn(len(choices))]
	}
}

// HTTPStatus returns an ArgGenerator that yields the given status (for attrs).
func HTTPStatus(status int) ArgGenerator {
	return func(*rand.Rand, GenContext) any { return status }
}

// RandomUUID generates a version-4 UUID (xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx).
// Consumes exactly 16 bytes from rng per call for deterministic streams.
var RandomUUID ArgGenerator = func(rng *rand.Rand, _ GenContext) any {
	var buf [16]byte
	rng.Read(buf[:])
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	var out [36]byte
	hex.Encode(out[0:8], buf[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], buf[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], buf[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], buf[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], buf[10:16])
	return string(out[:])
}

// LogNormalInt returns an ArgGenerator that produces log-normally distributed
// integers. Useful for latency, response times, and size distributions where
// most values cluster near the median with a long right tail.
func LogNormalInt(median, sigma float64) ArgGenerator {
	return func(rng *rand.Rand, _ GenContext) any {
		v := median * math.Exp(sigma*rng.NormFloat64())
		if v < 0 {
			return 0
		}
		return int(math.Round(v))
	}
}

// OptionalAttr wraps a generator so it always consumes rng (preserving
// determinism) but returns nil when the probability roll fails. Nil values
// are skipped during attribute serialization.
func OptionalAttr(probability float64, gen ArgGenerator) ArgGenerator {
	return func(rng *rand.Rand, ctx GenContext) any {
		val := gen(rng, ctx)
		if rng.Float64() < probability {
			return val
		}
		return nil
	}
}

// SliceAttr returns an ArgGenerator that produces a variable-length []any.
// Length is drawn from [minLen, maxLen] inclusive, then each element is
// generated in order for deterministic output.
func SliceAttr(elemGen ArgGenerator, minLen, maxLen int) ArgGenerator {
	return func(rng *rand.Rand, ctx GenContext) any {
		n := minLen + rng.Intn(maxLen-minLen+1)
		out := make([]any, n)
		for i := range out {
			out[i] = elemGen(rng, ctx)
		}
		return out
	}
}



