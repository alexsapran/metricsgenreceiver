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

// GenerateFromPrepared generates a log record using pre-bucketed messages.
func GenerateFromPrepared(rng *rand.Rand, pp *PreparedProfile, timestamp time.Time) (body string, severity plog.SeverityNumber, attrs map[string]any) {
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
		return "no messages configured", plog.SeverityNumberInfo, nil
	}
	tmpl := msgs[rng.Intn(len(msgs))]
	args := make([]any, len(tmpl.Args))
	for i, gen := range tmpl.Args {
		args[i] = gen(rng, ctx)
	}
	body = fmt.Sprintf(tmpl.Format, args...)
	hasAttrs := len(tmpl.AttrFromArg) > 0 || len(tmpl.Attrs) > 0 || len(tmpl.RareAttrs) > 0 || pp.longTail != nil
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
		if pp.longTail != nil {
			pp.longTail.EmitLongTail(rng, attrs)
		}
	}
	return body, tmpl.Severity, attrs
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

// RandomIP generates uniform random IPs across the full IPv4 space.
// Deprecated: prefer ZipfianIP for realistic workloads with a finite IP pool.
var RandomIP ArgGenerator = func(rng *rand.Rand, _ GenContext) any {
	return net.IPv4(byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256))).String()
}

// IPPoolConfig holds optional IP pool configuration for ZipfianIP.
type IPPoolConfig struct {
	CIDRs    []string // CIDR ranges to draw IPs from (default: ["10.0.0.0/8"])
	PoolSize int      // number of IPs in the pool (default: scale * 10, minimum 500)
	ZipfSkew float64  // Zipf s parameter (default: 1.5); higher = more skewed
}

// ZipfianIP returns an ArgGenerator that selects from a pre-generated pool of IPs
// using a Zipfian (power-law) distribution. The pool is built deterministically
// from the configured CIDRs using the provided rng. If cfg is nil, defaults are used.
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
	const hexChars = "0123456789abcdef"
	return func(r *rand.Rand, _ GenContext) any {
		b := make([]byte, length)
		for i := range b {
			b[i] = hexChars[r.Intn(16)]
		}
		return string(b)
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

// HTTPMethod returns an ArgGenerator for HTTP method (for attrs).
func HTTPMethod(method string) ArgGenerator {
	return func(*rand.Rand, GenContext) any { return method }
}

// HTTPStatus returns an ArgGenerator that yields the given status (for attrs).
func HTTPStatus(status int) ArgGenerator {
	return func(*rand.Rand, GenContext) any { return status }
}

var goStackPackages = []string{
	"main", "net/http", "runtime", "encoding/json", "database/sql",
	"github.com/gin-gonic/gin", "go.opentelemetry.io/otel", "internal/handler",
	"server", "db", "cache", "worker", "grpc/client",
}
var goStackFiles = []string{
	"handler.go", "server.go", "main.go", "query.go", "connection.go",
	"client.go", "processor.go", "middleware.go", "router.go", "context.go",
}
var goStackFuncs = []string{
	"(*Handler).ServeHTTP", "(*Server).ListenAndServe", "main.main",
	"(*DB).Query", "(*Conn).Exec", "(*Client).Call", "(*Processor).Run",
	"(*Middleware).Handle", "(*Router).ServeHTTP", "(*Context).Next",
}

const tracePoolSize = 100

// buildGoStackTrace generates a single Go stack trace string of the given target length.
func buildGoStackTrace(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	b.WriteString("goroutine ")
	b.WriteString(strconv.Itoa(r.Intn(100) + 1))
	b.WriteString(" [running]:\n")
	frames := 8 + r.Intn(80)
	for i := 0; i < frames && b.Len() < targetLen; i++ {
		pkg := goStackPackages[r.Intn(len(goStackPackages))]
		b.WriteString(pkg)
		b.WriteByte('.')
		b.WriteString(goStackFuncs[r.Intn(len(goStackFuncs))])
		b.WriteString("(0x")
		b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
		b.WriteString(")\n\t/app/")
		b.WriteString(pkg)
		b.WriteByte('/')
		b.WriteString(goStackFiles[r.Intn(len(goStackFiles))])
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(r.Intn(200) + 1))
		b.WriteString(" +0x")
		b.WriteString(strconv.FormatInt(int64(r.Intn(0x2000)+0x100), 16))
		b.WriteByte('\n')
	}
	return b.String()
}

// GoStackTrace pre-generates a pool of Go stack traces and returns an ArgGenerator
// that picks from the pool at runtime (zero allocation in the hot path).
func GoStackTrace(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildGoStackTrace(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

var javaStackPackages = []string{
	"com.example.service", "com.example.controller", "org.springframework",
	"java.util", "java.lang", "io.netty", "org.hibernate", "com.fasterxml.jackson",
}
var javaStackClasses = []string{
	"UserService", "OrderController", "RestTemplate", "HttpClient",
	"TransactionManager", "EntityManager", "ObjectMapper", "HandlerAdapter",
}
var javaStackMethods = []string{
	"getUser", "handle", "execute", "process", "invoke", "doFilter",
	"findById", "save", "serialize", "deserialize",
}
var javaStackFiles = []string{
	"userservice", "ordercontroller", "resttemplate", "httpclient",
	"transactionmanager", "entitymanager", "objectmapper", "handleradapter",
}

func buildJavaStackTrace(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	exceptions := [...]string{"RuntimeException", "NullPointerException", "IOException", "SQLException"}
	msgs := [...]string{"Connection timeout", "null pointer", "connection refused", "deadline exceeded"}
	b.WriteString(exceptions[r.Intn(len(exceptions))])
	b.WriteString(": ")
	b.WriteString(msgs[r.Intn(len(msgs))])
	b.WriteByte('\n')
	frames := 10 + r.Intn(80)
	for i := 0; i < frames && b.Len() < targetLen; i++ {
		b.WriteString("\tat ")
		b.WriteString(javaStackPackages[r.Intn(len(javaStackPackages))])
		b.WriteByte('.')
		b.WriteString(javaStackClasses[r.Intn(len(javaStackClasses))])
		b.WriteByte('.')
		b.WriteString(javaStackMethods[r.Intn(len(javaStackMethods))])
		b.WriteByte('(')
		b.WriteString(javaStackFiles[r.Intn(len(javaStackFiles))])
		b.WriteString(".java:")
		b.WriteString(strconv.Itoa(r.Intn(200) + 1))
		b.WriteString(")\n")
	}
	return b.String()
}

// JavaStackTrace pre-generates a pool of Java-like stack traces.
func JavaStackTrace(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildJavaStackTrace(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildMySQLCrashTrace(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	trx1, trx2 := r.Intn(90000)+10000, r.Intn(90000)+10000
	table := mysqlTables[r.Intn(len(mysqlTables))]
	b.WriteString("*** (1) TRANSACTION:\nTRANSACTION ")
	b.WriteString(strconv.Itoa(trx1))
	b.WriteString(", ACTIVE ")
	b.WriteString(strconv.Itoa(r.Intn(30) + 1))
	b.WriteString(" sec starting index read\nmysql tables in use 1, locked 1\nLOCK WAIT ")
	b.WriteString(strconv.Itoa(r.Intn(5) + 1))
	b.WriteString(" lock struct(s), heap size ")
	b.WriteString(strconv.Itoa(r.Intn(2000) + 500))
	b.WriteString(", ")
	b.WriteString(strconv.Itoa(r.Intn(100) + 1))
	b.WriteString(" row lock(s)\nMySQL thread id ")
	b.WriteString(strconv.Itoa(r.Intn(99999) + 1))
	b.WriteString(", OS thread handle 0x")
	b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
	b.WriteString(", query id ")
	b.WriteString(strconv.Itoa(r.Intn(999999) + 1))
	b.WriteString("\nUPDATE ")
	b.WriteString(table)
	b.WriteString(" SET status = %s WHERE id = %s\n*** (2) WAITING FOR THIS LOCK TO BE GRANTED:\nRECORD LOCKS space id ")
	b.WriteString(strconv.Itoa(r.Intn(100) + 1))
	b.WriteString(" page no ")
	b.WriteString(strconv.Itoa(r.Intn(100) + 1))
	b.WriteString(" n bits ")
	b.WriteString(strconv.Itoa(r.Intn(64) + 8))
	b.WriteString(" index PRIMARY of table `")
	b.WriteString(mysqlDBNames[r.Intn(len(mysqlDBNames))])
	b.WriteString("`.`")
	b.WriteString(table)
	b.WriteString("`\n*** (2) TRANSACTION:\nTRANSACTION ")
	b.WriteString(strconv.Itoa(trx2))
	b.WriteString(", ACTIVE ")
	b.WriteString(strconv.Itoa(r.Intn(20) + 1))
	b.WriteString(" sec fetching rows\n*** WE ROLL BACK TRANSACTION (1)\n")
	for b.Len() < targetLen {
		b.WriteString("---TRANSACTION ")
		b.WriteString(strconv.Itoa(r.Intn(90000) + 10000))
		b.WriteString(", ACTIVE ")
		b.WriteString(strconv.Itoa(r.Intn(10)))
		b.WriteString(" sec\nmysql tables in use 1, locked 1\n")
		b.WriteString(strconv.Itoa(r.Intn(3) + 1))
		b.WriteString(" lock struct(s)\n")
	}
	return b.String()
}

// MySQLCrashTrace pre-generates a pool of InnoDB deadlock/crash traces.
func MySQLCrashTrace(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildMySQLCrashTrace(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

var nginxErrorMsgs = [...]string{
	"upstream prematurely closed connection while reading response header",
	"recv() failed (104: Connection reset by peer) while reading upstream",
	"send() failed (111: Connection refused) while sending to upstream",
	"SSL_do_handshake() failed (SSL: error:0A0C0103:SSL routines::internal error)",
	"open() \"/var/cache/nginx/proxy/7/00/0000000007\" failed (13: Permission denied)",
}

func buildNginxErrorDetails(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	pid := strconv.Itoa(r.Intn(99999) + 1)
	tid := strconv.Itoa(r.Intn(2))
	connBase := r.Intn(89999) + 10000
	for b.Len() < targetLen {
		b.WriteString("2024/01/15 12:00:00 [error] ")
		b.WriteString(pid)
		b.WriteByte('#')
		b.WriteString(tid)
		b.WriteString(": *")
		b.WriteString(strconv.Itoa(connBase + r.Intn(100)))
		b.WriteByte(' ')
		b.WriteString(nginxErrorMsgs[r.Intn(len(nginxErrorMsgs))])
		b.WriteByte('\n')
	}
	return b.String()
}

// NginxErrorDetails pre-generates a pool of multi-line nginx error output.
func NginxErrorDetails(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildNginxErrorDetails(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildLargeJSONPayload(r *rand.Rand, targetLen int) string {
	const hexChars = "0123456789abcdef"
	var b strings.Builder
	b.Grow(targetLen + 256)
	b.WriteString(`{"items":[`)
	id := make([]byte, 8)
	for b.Len() < targetLen {
		if b.Len() > 20 {
			b.WriteByte(',')
		}
		for i := range id {
			id[i] = hexChars[r.Intn(16)]
		}
		b.WriteString(`{"id":"`)
		b.Write(id)
		b.WriteString(`","name":"product-`)
		b.WriteString(strconv.Itoa(r.Intn(10000)))
		b.WriteString(`","price":`)
		b.WriteString(strconv.Itoa(r.Intn(500) + 10))
		b.WriteByte('}')
	}
	b.WriteString("]}")
	return b.String()
}

// LargeJSONPayload pre-generates a pool of large JSON request/response bodies.
func LargeJSONPayload(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildLargeJSONPayload(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildSQLExplainPlan(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 256)
	table := mysqlTables[r.Intn(len(mysqlTables))]
	b.WriteString("EXPLAIN SELECT * FROM ")
	b.WriteString(table)
	b.WriteString(" WHERE id = %s\n+----+-------------+-------+------+---------------+------+---------+------+------+----------+\n| id | select_type | table | type | possible_keys | key  | key_len | ref  | rows | Extra    |\n+----+-------------+-------+------+---------------+------+---------+------+------+----------+\n")
	for b.Len() < targetLen {
		b.WriteString("| ")
		b.WriteString(strconv.Itoa(r.Intn(5) + 1))
		b.WriteString(" | SIMPLE       | ")
		b.WriteString(table)
		b.WriteString("   | ALL  | NULL          | NULL | NULL    | NULL | ")
		b.WriteString(strconv.Itoa(r.Intn(100000) + 100))
		b.WriteString("   |          |\n")
	}
	b.WriteString("+----+-------------+-------+------+---------------+------+---------+------+------+----------+\n")
	return b.String()
}

// SQLExplainPlan pre-generates a pool of MySQL EXPLAIN output.
func SQLExplainPlan(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildSQLExplainPlan(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildRedisSlowlogOutput(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 256)
	cmds := [...]string{"GET", "SET", "HGETALL", "LRANGE", "SMEMBERS", "ZRANGE"}
	id := make([]byte, 12)
	const hexChars = "0123456789abcdef"
	for b.Len() < targetLen {
		b.WriteString(strconv.Itoa(r.Intn(100) + 1))
		b.WriteString(") 1) (integer) ")
		b.WriteString(strconv.Itoa(r.Intn(999)))
		b.WriteString("\n   2) (integer) ")
		b.WriteString(strconv.Itoa(r.Intn(999999)))
		b.WriteString("\n   3) (integer) ")
		b.WriteString(strconv.Itoa(r.Intn(50000)))
		b.WriteString("\n   4) 1) \"")
		b.WriteString(cmds[r.Intn(len(cmds))])
		b.WriteString("\"\n      2) \"")
		for i := range id {
			id[i] = hexChars[r.Intn(16)]
		}
		b.Write(id)
		b.WriteString("\"\n")
	}
	return b.String()
}

// RedisSlowlogOutput pre-generates a pool of Redis SLOWLOG output.
func RedisSlowlogOutput(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildRedisSlowlogOutput(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

func buildRedisCrashReport(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)
	pid := r.Intn(99999) + 1
	b.WriteString("=== REDIS BUG REPORT START: Cut & paste starting from here ===\nRedis version: ")
	b.WriteString(redisVersions[r.Intn(len(redisVersions))])
	b.WriteString("\nRedis pid:")
	b.WriteString(strconv.Itoa(pid))
	b.WriteString("\nOS:Linux 5.15.0 x86_64\nUptime: 0.0 sec\nFatal signal: 11 (SIGSEGV) at 0x")
	b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
	b.WriteString(", pid ")
	b.WriteString(strconv.Itoa(pid))
	b.WriteString(", tid ")
	b.WriteString(strconv.Itoa(r.Intn(99999) + 1))
	b.WriteString("\nBacktrace:\n")
	frames := 15 + r.Intn(25)
	for i := 0; i < frames && b.Len() < targetLen; i++ {
		b.WriteByte('#')
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" 0x")
		b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
		b.WriteString(" in ?? () from /usr/lib/redis/redis-server\n")
	}
	b.WriteString("=== REDIS BUG REPORT END. PLEASE INCLUDE EVERYTHING ABOVE ===\n")
	for b.Len() < targetLen {
		b.WriteString("Thread ")
		b.WriteString(strconv.Itoa(r.Intn(20)))
		b.WriteString(": 0x")
		b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffff, 16))
		b.WriteByte('\n')
	}
	return b.String()
}

// RedisCrashReport pre-generates a pool of Redis crash report output.
func RedisCrashReport(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildRedisCrashReport(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
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

// commonErrorMessages are realistic error messages seen across production
// services. Used by ErrorMessageAttrs to populate error.message at ~70%.
var commonErrorMessages = []string{
	"connection refused",
	"context deadline exceeded",
	"connection reset by peer",
	"i/o timeout",
	"TLS handshake timeout",
	"no such host",
	"broken pipe",
	"connection timed out",
	"request canceled",
	"EOF",
	"permission denied",
	"resource temporarily unavailable",
	"too many open files",
}

// buildLargeErrorMessage generates a single large error payload with a mix of
// realistic patterns: multi-line stack traces, JSON error responses, and
// connection error cascades.
func buildLargeErrorMessage(r *rand.Rand, targetLen int) string {
	var b strings.Builder
	b.Grow(targetLen + 512)

	// Pick a pattern
	switch r.Intn(3) {
	case 0: // JSON error response
		b.WriteString(`{"error":{"root_cause":[{"type":"`)
		causes := [...]string{"search_phase_execution_exception", "index_not_found_exception", "mapper_parsing_exception", "resource_already_exists_exception"}
		b.WriteString(causes[r.Intn(len(causes))])
		b.WriteString(`","reason":"`)
		reasons := [...]string{
			"all shards failed", "no such index", "failed to parse field",
			"resource already exists", "circuit breaking exception: [request] Data too large",
		}
		b.WriteString(reasons[r.Intn(len(reasons))])
		b.WriteString(`"}],"type":"`)
		b.WriteString(causes[r.Intn(len(causes))])
		b.WriteString(`","reason":"`)
		b.WriteString(reasons[r.Intn(len(reasons))])
		b.WriteString(`","phase":"query","grouped":true,"failed_shards":[`)
		for b.Len() < targetLen {
			if b.Len() > 300 {
				b.WriteByte(',')
			}
			b.WriteString(`{"shard":`)
			b.WriteString(strconv.Itoa(r.Intn(10)))
			b.WriteString(`,"index":"logs-`)
			b.WriteString(strconv.Itoa(r.Intn(100)))
			b.WriteString(`","node":"`)
			b.WriteString(strconv.FormatUint(r.Uint64()&0xffffffffffff, 16))
			b.WriteString(`","reason":{"type":"`)
			b.WriteString(causes[r.Intn(len(causes))])
			b.WriteString(`","reason":"`)
			b.WriteString(reasons[r.Intn(len(reasons))])
			b.WriteString(`"}}`)
		}
		b.WriteString(`]},"status":500}`)

	case 1: // Connection error cascade
		services := [...]string{"elasticsearch", "kibana", "apm-server", "fleet-server", "logstash"}
		errs := [...]string{
			"connection refused", "connection reset by peer", "i/o timeout",
			"TLS handshake timeout", "no route to host", "context deadline exceeded",
		}
		for b.Len() < targetLen {
			b.WriteString("error connecting to ")
			b.WriteString(services[r.Intn(len(services))])
			b.WriteString(" at 10.")
			b.WriteString(strconv.Itoa(r.Intn(256)))
			b.WriteByte('.')
			b.WriteString(strconv.Itoa(r.Intn(256)))
			b.WriteByte('.')
			b.WriteString(strconv.Itoa(r.Intn(256)))
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(r.Intn(10000) + 9000))
			b.WriteString(": ")
			b.WriteString(errs[r.Intn(len(errs))])
			b.WriteString("; attempt ")
			b.WriteString(strconv.Itoa(r.Intn(10) + 1))
			b.WriteString(" of 10\n")
		}

	case 2: // Stack trace with wrapped errors
		b.WriteString("github.com/elastic/cloud-on-k8s/pkg/controller")
		b.WriteString(": reconciliation failed: ")
		wraps := [...]string{
			"transient error", "context canceled", "connection lost",
			"timeout waiting for condition", "resource version conflict",
		}
		b.WriteString(wraps[r.Intn(len(wraps))])
		b.WriteByte('\n')
		b.WriteString(buildGoStackTrace(r, targetLen-b.Len()))
	}
	return b.String()
}

// LargeErrorMessage pre-generates a pool of large error message payloads.
func LargeErrorMessage(minBytes, maxBytes int, rng *rand.Rand) ArgGenerator {
	pool := make([]string, tracePoolSize)
	for i := range pool {
		targetLen := rng.Intn(maxBytes-minBytes+1) + minBytes
		pool[i] = buildLargeErrorMessage(rng, targetLen)
	}
	return func(r *rand.Rand, _ GenContext) any { return pool[r.Intn(len(pool))] }
}

// ErrorMessageAttrs returns the two cross-cutting AttrGen entries that every
// profile should append to each MessageTemplate.Attrs:
//   - error.message at ~70% presence (pool-based, 13 messages)
//   - log.origin.stack_trace at ~25% presence (pool-based stack traces)
//
// stackTraceGen should be a pre-pooled generator such as GoStackTrace or
// JavaStackTrace. Pass nil to use a default Go stack trace pool.
func ErrorMessageAttrs(rng *rand.Rand, stackTraceGen ArgGenerator) []AttrGen {
	shortGen := RandomFrom(commonErrorMessages...)
	mediumGen := LargeErrorMessage(200, 2000, rng)
	largeGen := LargeErrorMessage(2000, 80000, rng)

	// ~93% short, ~5% medium, ~2% large
	mixedErrorMsgGen := func(r *rand.Rand, ctx GenContext) any {
		roll := r.Intn(100)
		switch {
		case roll < 93:
			return shortGen(r, ctx)
		case roll < 98:
			return mediumGen(r, ctx)
		default:
			return largeGen(r, ctx)
		}
	}

	if stackTraceGen == nil {
		stackTraceGen = GoStackTrace(800, 8500, rng)
	}
	return []AttrGen{
		{"error.message", OptionalAttr(0.60, mixedErrorMsgGen)},
		{"log.origin.stack_trace", OptionalAttr(0.20, stackTraceGen)},
	}
}

// appendCrossCutting appends the cross-cutting attrs to every MessageTemplate
// in the slice and returns the modified slice.
func appendCrossCutting(msgs []MessageTemplate, extra []AttrGen) []MessageTemplate {
	for i := range msgs {
		msgs[i].Attrs = append(msgs[i].Attrs, extra...)
	}
	return msgs
}

// LongTailField holds a pre-generated pool of values for a rare field.
// Hot-path cost: one rng.Int63() to decide emit + pool index.
type LongTailField struct {
	Key       string
	Pool      []string // pre-generated string values
	Threshold int64    // emit when rng.Int63n(longTailDenominator) < threshold
}

const longTailPoolBits = 8 // pool size = 256
const longTailPoolSize = 1 << longTailPoolBits
const longTailPoolMask = longTailPoolSize - 1
const longTailDenominator = 10000 // probability denominator (threshold 50 = 0.5%)

// LongTailSet is a pre-computed batch of long-tail fields. The hot path
// uses a pre-generated schedule of firing positions to avoid per-field
// probability rolls, consuming exactly one rng.Int63() per record.
type LongTailSet struct {
	Fields   []LongTailField
	schedule [][]scheduleEntry // pre-computed: for each schedule slot, which fields fire
}

type scheduleEntry struct {
	fieldIdx int
	poolIdx  int
}

const longTailScheduleSize = 65536
const longTailScheduleMask = longTailScheduleSize - 1

// buildStrPool creates a pool of string values from choices.
func buildStrPool(rng *rand.Rand, choices []string) []string {
	pool := make([]string, longTailPoolSize)
	for i := range pool {
		pool[i] = choices[rng.Intn(len(choices))]
	}
	return pool
}

// buildIntStrPool creates a pool of string-encoded ints in [min, max].
func buildIntStrPool(rng *rand.Rand, min, max int) []string {
	pool := make([]string, longTailPoolSize)
	span := max - min + 1
	for i := range pool {
		pool[i] = strconv.Itoa(rng.Intn(span) + min)
	}
	return pool
}

// buildIntChoiceStrPool creates a pool of string-encoded ints from choices.
func buildIntChoiceStrPool(rng *rand.Rand, choices []int) []string {
	pool := make([]string, longTailPoolSize)
	for i := range pool {
		pool[i] = strconv.Itoa(choices[rng.Intn(len(choices))])
	}
	return pool
}

// buildHexStrPool creates a pool of random hex strings.
func buildHexStrPool(rng *rand.Rand, length int) []string {
	const hexChars = "0123456789abcdef"
	pool := make([]string, longTailPoolSize)
	b := make([]byte, length)
	for i := range pool {
		for j := range b {
			b[j] = hexChars[rng.Intn(16)]
		}
		pool[i] = string(b)
	}
	return pool
}

// EmitLongTail consumes exactly one rng.Int63() per call and uses a pre-
// computed schedule to decide which fields fire. The schedule was generated
// at construction time by simulating each field's probability independently.
func (lt *LongTailSet) EmitLongTail(rng *rand.Rand, attrsOut map[string]any) {
	r := rng.Int63()
	slot := int(r) & longTailScheduleMask
	entries := lt.schedule[slot]
	for _, e := range entries {
		f := &lt.Fields[e.fieldIdx]
		attrsOut[f.Key] = f.Pool[e.poolIdx]
	}
}

// LongTailAttrs builds a LongTailSet with 61 rare fields (<1% presence)
// representing config-dump and diagnostic event patterns from production
// OTel/K8s workloads. Values are pre-stringified to avoid interface boxing.
func LongTailAttrs(rng *rand.Rand) *LongTailSet {
	fields := []LongTailField{
		// --- Config-dump fields (~0.1-0.5% presence) ---
		// Threshold N means N/10000 = N*0.01% probability.
		{"config.file.path", buildStrPool(rng, []string{"/etc/app/config.yaml", "/opt/config/settings.json", "/usr/local/etc/app.conf", "/app/config/production.yaml"}), 30},
		{"config.file.hash", buildHexStrPool(rng, 32), 30},
		{"config.reload.count", buildIntStrPool(rng, 1, 50), 20},
		{"config.reload.last_status", buildStrPool(rng, []string{"success", "failed", "skipped"}), 20},
		{"config.environment", buildStrPool(rng, []string{"production", "staging", "canary", "development"}), 50},
		{"config.feature_flags", buildStrPool(rng, []string{"dark_launch=true,new_ui=false", "dark_launch=false,new_ui=true", "beta=true"}), 20},
		{"config.max_connections", buildIntChoiceStrPool(rng, []int{100, 256, 512, 1024, 2048}), 30},
		{"config.worker_threads", buildIntChoiceStrPool(rng, []int{2, 4, 8, 16, 32}), 30},
		{"config.tls.enabled", buildStrPool(rng, []string{"true", "false"}), 40},
		{"config.tls.cert_expiry_days", buildIntStrPool(rng, 1, 365), 20},
		{"config.log_level", buildStrPool(rng, []string{"debug", "info", "warn", "error"}), 50},
		{"config.memory_limit_mb", buildIntChoiceStrPool(rng, []int{256, 512, 1024, 2048, 4096, 8192}), 30},
		{"config.cpu_limit_millicores", buildIntChoiceStrPool(rng, []int{250, 500, 1000, 2000, 4000}), 30},

		// --- Diagnostic / health-check fields ---
		{"process.runtime.jvm.gc.count", buildIntStrPool(rng, 0, 5000), 40},
		{"process.runtime.jvm.gc.pause_ms", buildIntStrPool(rng, 1, 500), 40},
		{"process.runtime.jvm.heap_used_mb", buildIntStrPool(rng, 64, 4096), 30},
		{"process.runtime.jvm.threads.count", buildIntStrPool(rng, 10, 500), 30},
		{"process.runtime.go.goroutines", buildIntStrPool(rng, 1, 10000), 50},
		{"process.runtime.go.mem.heap_alloc_mb", buildIntStrPool(rng, 8, 2048), 50},
		{"process.runtime.go.gc.pause_ns", buildIntStrPool(rng, 10000, 50000000), 30},
		{"process.cpu_seconds_total", buildIntStrPool(rng, 1, 100000), 40},
		{"process.memory_rss_mb", buildIntStrPool(rng, 32, 8192), 40},
		{"process.open_fds", buildIntStrPool(rng, 10, 65000), 30},
		{"process.uptime_seconds", buildIntStrPool(rng, 1, 2592000), 30},

		// --- K8s scheduling/lifecycle fields ---
		{"k8s.pod.restart_count", buildIntChoiceStrPool(rng, []int{0, 0, 0, 1, 1, 2, 3, 5}), 50},
		{"k8s.container.ready", buildStrPool(rng, []string{"true", "true", "true", "false"}), 40},
		{"k8s.pod.phase", buildStrPool(rng, []string{"Running", "Running", "Pending", "Succeeded", "Failed"}), 30},
		{"k8s.pod.qos_class", buildStrPool(rng, []string{"Guaranteed", "Burstable", "BestEffort"}), 30},
		{"k8s.node.condition.ready", buildStrPool(rng, []string{"True", "True", "True", "False"}), 20},
		{"k8s.node.condition.memory_pressure", buildStrPool(rng, []string{"False", "False", "True"}), 10},
		{"k8s.node.condition.disk_pressure", buildStrPool(rng, []string{"False", "False", "True"}), 10},
		{"k8s.deployment.revision", buildIntStrPool(rng, 1, 200), 30},
		{"k8s.hpa.current_replicas", buildIntStrPool(rng, 1, 50), 20},
		{"k8s.hpa.desired_replicas", buildIntStrPool(rng, 1, 50), 20},

		// --- Network / connectivity diagnostics ---
		{"net.sock.peer.addr", buildStrPool(rng, buildIPPool(rng, []string{"10.0.0.0/8"}, 200)), 40},
		{"net.sock.host.addr", buildStrPool(rng, []string{"0.0.0.0", "127.0.0.1", "10.0.0.1"}), 30},
		{"net.sock.host.port", buildIntChoiceStrPool(rng, []int{8080, 8443, 9090, 9200, 3306, 6379}), 30},
		{"net.host.connection.type", buildStrPool(rng, []string{"wifi", "cell", "wired", "unknown"}), 20},
		{"dns.lookup_duration_ms", buildIntStrPool(rng, 0, 200), 20},
		{"net.protocol.name", buildStrPool(rng, []string{"http", "https", "grpc", "amqp", "redis"}), 40},
		{"net.protocol.version", buildStrPool(rng, []string{"1.0", "1.1", "2.0", "3.0"}), 40},
		{"tls.client.server_name", buildStrPool(rng, []string{"api.example.com", "internal.svc.local", "search.cloud.internal", "dashboard.cloud.internal"}), 20},
		{"tls.client.certificate.serial", buildHexStrPool(rng, 20), 10},

		// --- Cloud / infrastructure metadata ---
		{"cloud.account.id", buildStrPool(rng, []string{"123456789012", "987654321098", "112233445566"}), 30},
		{"cloud.availability_zone", buildStrPool(rng, []string{"eu-west-1a", "eu-west-1b", "eu-west-1c", "us-east-1a", "us-east-1b"}), 40},
		{"cloud.machine.type", buildStrPool(rng, []string{"m5.xlarge", "m5.2xlarge", "c5.4xlarge", "r5.2xlarge", "t3.medium"}), 30},
		{"cloud.region", buildStrPool(rng, []string{"eu-west-1", "us-east-1", "ap-southeast-1"}), 40},
		{"host.cpu.utilization", buildIntStrPool(rng, 1, 100), 30},
		{"host.disk.io.read_bytes", buildIntStrPool(rng, 0, 1000000000), 20},
		{"host.disk.io.write_bytes", buildIntStrPool(rng, 0, 1000000000), 20},
		{"host.network.io.receive_bytes", buildIntStrPool(rng, 0, 1000000000), 20},
		{"host.network.io.transmit_bytes", buildIntStrPool(rng, 0, 1000000000), 20},

		// --- Distributed tracing / correlation ---
		{"session.id", buildHexStrPool(rng, 16), 50},
		{"enduser.id", buildHexStrPool(rng, 12), 30},
		{"enduser.role", buildStrPool(rng, []string{"admin", "user", "service-account", "readonly"}), 20},
		{"enduser.scope", buildStrPool(rng, []string{"read", "write", "admin", "monitoring"}), 20},
		{"thread.id", buildIntStrPool(rng, 1, 65535), 40},
		{"thread.name", buildStrPool(rng, []string{"main", "worker-0", "worker-1", "grpc-default-executor-0", "http-nio-8080-exec-1", "pool-1-thread-1"}), 40},
		{"code.function", buildStrPool(rng, []string{"handleRequest", "processMessage", "executeQuery", "serialize", "authenticate", "authorize", "validate"}), 50},
		{"code.namespace", buildStrPool(rng, []string{"com.example.service", "internal.handler", "net.http", "database.sql", "grpc.server"}), 40},
		{"code.filepath", buildStrPool(rng, []string{"handler.go:142", "service.py:89", "Controller.java:201", "middleware.ts:56", "query.go:77"}), 40},
		{"code.lineno", buildIntStrPool(rng, 1, 2000), 40},
	}
	schedule := buildLongTailSchedule(rng, fields)
	return &LongTailSet{Fields: fields, schedule: schedule}
}

// buildLongTailSchedule pre-computes which fields fire for each schedule slot.
// For each slot, it independently rolls each field's probability and records
// the ones that fire along with a pool index. This replaces per-record rng
// calls with a single schedule lookup.
func buildLongTailSchedule(rng *rand.Rand, fields []LongTailField) [][]scheduleEntry {
	schedule := make([][]scheduleEntry, longTailScheduleSize)
	for slot := range schedule {
		var entries []scheduleEntry
		for fi, f := range fields {
			if rng.Int63()%longTailDenominator < f.Threshold {
				poolIdx := rng.Intn(longTailPoolSize)
				entries = append(entries, scheduleEntry{fieldIdx: fi, poolIdx: poolIdx})
			} else {
				rng.Intn(longTailPoolSize) // consume for determinism
			}
		}
		schedule[slot] = entries
	}
	return schedule
}

