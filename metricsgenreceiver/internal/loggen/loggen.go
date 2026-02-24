package loggen

import (
	"fmt"
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
// TRACE 0.5%, DEBUG 2%, INFO 85%, WARN 7%, ERROR 5%, FATAL 0.5%
func DefaultSeverityWeights() [6]int {
	return [6]int{0, 2, 87, 94, 99, 100}
}

// AppProfile defines a log-generating application's behavior.
type AppProfile struct {
	Name string
	// ScopeName is the instrumentation scope name for log records from this profile.
	ScopeName string
	// Messages contains all message templates. GenerateLogRecord picks by severity.
	Messages []MessageTemplate
	// SeverityWeights: cumulative weights for TRACE, DEBUG, INFO, WARN, ERROR, FATAL.
	// e.g. [0, 2, 87, 94, 99, 100] means 0.5% TRACE, 2% DEBUG, 85% INFO, 7% WARN, 5% ERROR, 0.5% FATAL
	SeverityWeights [6]int
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
func GenerateLogRecord(rng *rand.Rand, profile AppProfile, timestamp time.Time) (body string, severity plog.SeverityNumber, attrs map[string]any) {
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
		for k, gen := range tmpl.Attrs {
			if _, ok := attrs[k]; ok {
				continue
			}
			attrs[k] = gen(rng, ctx)
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

// GetScopeName returns the instrumentation scope name for this profile.
func (pp *PreparedProfile) GetScopeName() string {
	if pp.profile.ScopeName != "" {
		return pp.profile.ScopeName
	}
	return "log-generator"
}

// OverrideSeverityWeights replaces the profile's severity weights.
func (pp *PreparedProfile) OverrideSeverityWeights(w [6]int) {
	pp.profile.SeverityWeights = w
}

// GenerateFromPrepared generates a log record using pre-bucketed messages.
func GenerateFromPrepared(rng *rand.Rand, pp *PreparedProfile, timestamp time.Time) (body string, severity plog.SeverityNumber, attrs map[string]any) {
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
		for k, gen := range tmpl.Attrs {
			if _, ok := attrs[k]; ok {
				continue
			}
			attrs[k] = gen(rng, ctx)
		}
	}
	return body, tmpl.Severity, attrs
}

// GenerateFromPreparedInto generates a log record into a reusable attrs map to avoid allocations.
// attrsOut must be non-nil; it is cleared and reused.
func GenerateFromPreparedInto(rng *rand.Rand, pp *PreparedProfile, timestamp time.Time, attrsOut map[string]any) (body string, severity plog.SeverityNumber) {
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
				v := args[idx]
				if tpl, ok := RouteTemplate(v); ok {
					attrsOut[k] = tpl
				} else {
					attrsOut[k] = v
				}
			}
		}
		for k, gen := range tmpl.Attrs {
			if _, ok := attrsOut[k]; ok {
				continue
			}
			attrsOut[k] = gen(rng, ctx)
		}
	}
	return body, tmpl.Severity
}

// --- ArgGenerator helpers ---

// RandomIP generates uniform random IPs across the full IPv4 space.
// Deprecated: prefer ZipfianIP for realistic workloads with a finite IP pool.
var RandomIP ArgGenerator = func(rng *rand.Rand, _ *GenContext) any {
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
		ranges = append(ranges, cidrRange{base: 0x0A000000, hostMax: 0x00FFFFFF}) // 10.0.0.0/8
	}

	pool := make([]string, poolSize)
	for i := 0; i < poolSize; i++ {
		cr := ranges[rng.Intn(len(ranges))]
		host := uint32(rng.Int63n(int64(cr.hostMax))) + 1
		ip := cr.base | host
		pool[i] = net.IPv4(byte(ip>>24), byte(ip>>16), byte(ip>>8), byte(ip)).String()
	}
	return func(r *rand.Rand, _ *GenContext) any {
		zipf := rand.NewZipf(r, skew, 1, uint64(poolSize-1))
		return pool[zipf.Uint64()]
	}
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
	return func(r *rand.Rand, ctx *GenContext) any {
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
	frames := 8 + r.Intn(25)
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
	return func(r *rand.Rand, _ *GenContext) any { return pool[r.Intn(len(pool))] }
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
	frames := 10 + r.Intn(30)
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
	return func(r *rand.Rand, _ *GenContext) any { return pool[r.Intn(len(pool))] }
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
	return func(r *rand.Rand, _ *GenContext) any { return pool[r.Intn(len(pool))] }
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
	return func(r *rand.Rand, _ *GenContext) any { return pool[r.Intn(len(pool))] }
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
	return func(r *rand.Rand, _ *GenContext) any { return pool[r.Intn(len(pool))] }
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
	return func(r *rand.Rand, _ *GenContext) any { return pool[r.Intn(len(pool))] }
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
	return func(r *rand.Rand, _ *GenContext) any { return pool[r.Intn(len(pool))] }
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
	return func(r *rand.Rand, _ *GenContext) any { return pool[r.Intn(len(pool))] }
}
