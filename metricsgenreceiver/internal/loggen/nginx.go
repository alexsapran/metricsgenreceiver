package loggen

import (
	"math/rand"

	"go.opentelemetry.io/collector/pdata/plog"
)

// nginxRouteTemplates are parameterized routes for http.url (low cardinality).
// Use {id} placeholder; RouteWithRandomID substitutes it for the log body.
var nginxRouteTemplates = []string{
	"/api/v1/users/{id}",
	"/api/v1/users/{id}/profile",
	"/api/v1/orders/{id}",
	"/api/v1/orders/{id}/items",
	"/api/v1/orders/{id}/status",
	"/api/v2/search",
	"/api/v1/products/{id}",
	"/api/v1/products/{id}/reviews",
	"/api/v1/cart/{id}",
	"/api/v1/checkout",
	"/api/v2/analytics",
	"/api/v1/config",
	"/api/v1/notifications/{id}",
	"/api/v2/users/{id}",
	"/api/v1/sessions/{id}",
	"/api/v1/payments/{id}",
	"/api/v1/invoices/{id}",
	"/api/v2/orders/{id}",
	"/api/v1/shipping/{id}",
	"/api/v1/reviews/{id}",
	"/api/v2/products/{id}",
	"/api/v1/categories/{id}",
	"/api/v1/tags/{id}",
	"/api/v2/recommendations",
	"/api/v1/export",
	"/api/v1/import",
	"/api/v2/metrics",
}

func NginxProfile(rng *rand.Rand, ipCfg *IPPoolConfig) *AppProfile {
	if rng == nil {
		rng = rand.New(rand.NewSource(0))
	}
	crossCutting := ErrorMessageAttrs(rng, GoStackTrace(800, 8500, rng))
	longTail := LongTailAttrs(rng)
	msgs := append(
		append(nginxAccessLogs(rng, ipCfg), nginxDebugLogs()...),
		nginxWarnLogs(rng, ipCfg)...,
	)
	return &AppProfile{
		Name:            "nginx",
		ScopeName:       "io.opentelemetry.nginx",
		SeverityWeights: DefaultSeverityWeights(),
		Messages:        appendCrossCutting(msgs, crossCutting),
		LongTail:        longTail,
	}
}

func nginxAccessLogs(rng *rand.Rand, ipCfg *IPPoolConfig) []MessageTemplate {
	tsLayout := "02/Jan/2006:15:04:05 -0700"
	zipfIP := ZipfianIP(5000, rng, ipCfg)
	routeGen := RouteWithRandomID(nginxRouteTemplates)
	return []MessageTemplate{
		{
			Severity:     plog.SeverityNumberInfo,
			Format:       "%s - - [%s] \"GET %s HTTP/1.1\" %d %d \"-\" \"%s\"",
			Args:         []ArgGenerator{zipfIP, Timestamp(tsLayout), routeGen, RandomHTTPStatus, RandomBytes, RandomUserAgent},
			AttrFromArg:  map[string]int{"net.peer.ip": 0, "http.status_code": 3, "http.url": 2, "http.response.body.size": 4, "user_agent.original": 5},
			Attrs:        []AttrGen{{"http.method", Static("GET")}, {"http.flavor", RandomFrom("1.1", "2.0")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s - - [%s] \"POST %s HTTP/1.1\" %d %d \"%s\" \"%s\"",
			Args: []ArgGenerator{
				zipfIP, Timestamp(tsLayout),
				RandomPath([]string{"/api/v1/orders", "/api/v1/users", "/api/v1/products"}),
				RandomHTTPStatus, RandomBytes,
				RandomFrom("-", "https://example.com/", "https://app.example.com/dashboard"),
				RandomUserAgent,
			},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "http.status_code": 3, "http.url": 2, "http.response.body.size": 4, "user_agent.original": 6},
			Attrs:       []AttrGen{{"http.method", Static("POST")}, {"http.request.body.size", RandomInt(0, 50000)}},
		},
		{
			Severity:     plog.SeverityNumberInfo,
			Format:       "%s - - [%s] \"GET /health HTTP/1.1\" 200 15 \"-\" \"kube-probe/1.28\"",
			Args:         []ArgGenerator{zipfIP, Timestamp(tsLayout)},
			AttrFromArg:  map[string]int{"net.peer.ip": 0},
			Attrs:        []AttrGen{{"http.method", Static("GET")}, {"http.status_code", HTTPStatus(200)}, {"http.url", Static("/health")}},
		},
		{
			Severity:    plog.SeverityNumberInfo,
			Format:      "%s - - [%s] \"GET /static/js/app.%s.js HTTP/1.1\" 304 0 \"%s\" \"%s\"",
			Args:        []ArgGenerator{zipfIP, Timestamp(tsLayout), RandomID(8), RandomFrom("-", "https://app.example.com/"), RandomUserAgent},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "user_agent.original": 4},
			Attrs:       []AttrGen{{"http.method", Static("GET")}, {"http.status_code", HTTPStatus(304)}, {"http.url", Static("/static/js/app.js")}, {"http.flavor", RandomFrom("1.1", "2.0")}},
		},
		{
			Severity:    plog.SeverityNumberInfo,
			Format:      "%s - - [%s] \"GET /api/v1/products?page=%d&limit=20 HTTP/1.1\" 200 %d \"-\" \"%s\"",
			Args:        []ArgGenerator{zipfIP, Timestamp(tsLayout), RandomInt(1, 50), RandomBytes, RandomUserAgent},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "http.response.body.size": 3, "user_agent.original": 4},
			Attrs:       []AttrGen{{"http.method", Static("GET")}, {"http.status_code", HTTPStatus(200)}, {"http.url", Static("/api/v1/products")}, {"http.flavor", RandomFrom("1.1", "2.0")}},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s - - [%s] \"GET %s HTTP/1.1\" %d %d \"-\" \"%s\"",
			Args: []ArgGenerator{
				zipfIP, Timestamp(tsLayout),
				RandomPath([]string{"/", "/api/v1/health", "/metrics", "/favicon.ico", "/api/v1/config"}),
				RandomHTTPStatus, RandomBytes, RandomUserAgent,
			},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "http.status_code": 3, "http.url": 2, "http.response.body.size": 4, "user_agent.original": 5},
			Attrs:       []AttrGen{{"http.method", Static("GET")}},
		},
		{
			Severity:    plog.SeverityNumberInfo,
			Format:      "%s - - [%s] \"DELETE /api/v1/users/%s HTTP/1.1\" %d %d \"-\" \"%s\"",
			Args:        []ArgGenerator{zipfIP, Timestamp(tsLayout), RandomID(8), RandomFromInt(200, 204, 404), RandomBytes, RandomUserAgent},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "http.status_code": 3, "http.response.body.size": 4, "user_agent.original": 5},
			Attrs:       []AttrGen{{"http.method", Static("DELETE")}, {"http.url", Static("/api/v1/users/{id}")}},
		},
	}
}

func nginxDebugLogs() []MessageTemplate {
	tsLayout := "2006/01/02 15:04:05"
	pid := RandomInt(1, 99999)
	tid := RandomInt(0, 1)
	connID := RandomInt(1000, 99999)
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberDebug,
			Format:   "%s [debug] %d#%d: *%d http process request line",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID},
		},
		{
			Severity: plog.SeverityNumberDebug,
			Format:   "%s [debug] %d#%d: *%d http header: \"Host: %s\"",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, RandomFrom("api.example.com", "localhost", "app.example.com")},
		},
	}
}

func nginxWarnLogs(rng *rand.Rand, ipCfg *IPPoolConfig) []MessageTemplate {
	tsLayout := "2006/01/02 15:04:05"
	pid := RandomInt(1, 99999)
	tid := RandomInt(0, 1)
	connID := RandomInt(1000, 99999)
	zipfIP := ZipfianIP(5000, rng, ipCfg)
	upstreamIP := zipfIP
	port := RandomInt(8080, 9090)
	path := RandomPath([]string{"/api/v1/users", "/api/v1/orders", "/health"})
	server := RandomFrom("localhost", "_", "api.example.com")
	tmpfile := RandomFrom("/tmp/nginx/proxy/0/00/0000000000", "/tmp/nginx/proxy/1/01/0000000001")
	return []MessageTemplate{
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%s [warn] %d#%d: *%d upstream server temporarily disabled while connecting to upstream, client: %s, server: %s, request: \"GET %s HTTP/1.1\", upstream: \"http://%s:%d%s\"",
			Args: []ArgGenerator{
				Timestamp(tsLayout), pid, tid, connID,
				zipfIP, server, path,
				upstreamIP, port, path,
			},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%s [warn] %d#%d: *%d an upstream response is buffered to a temporary file %s, client: %s, server: %s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, tmpfile, zipfIP, server},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%s [warn] %d#%d: *%d upstream timed out, client: %s, server: %s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, zipfIP, server},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d connect() failed (111: Connection refused) while connecting to upstream, client: %s, server: %s, request: \"GET %s HTTP/1.1\", upstream: \"http://%s:%d%s\"",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, zipfIP, server, path, upstreamIP, port, path},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d upstream timed out (110: Connection timed out) while reading response header from upstream, client: %s, server: %s, request: \"POST %s HTTP/1.1\"",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, zipfIP, server, path},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d no live upstreams while connecting to upstream, client: %s, server: %s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, zipfIP, server},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d connect() failed (111: Connection refused) while connecting to upstream, client: %s, server: %s, request: \"GET %s HTTP/1.1\", upstream: \"http://%s:%d%s\"\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, zipfIP, server, path, upstreamIP, port, path, NginxErrorDetails(1500, 6000, rng)},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d upstream timed out (110: Connection timed out) while reading response header from upstream, client: %s, server: %s\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, zipfIP, server, NginxErrorDetails(1500, 7000, rng)},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d no live upstreams while connecting to upstream, client: %s, server: %s\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, zipfIP, server, NginxErrorDetails(1500, 7000, rng)},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s [emerg] %d#%d: host not found in upstream \"%s\" in /etc/nginx/conf.d/upstream.conf:3",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, RandomFrom("backend-api", "mysql-primary", "redis-cache")},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s [emerg] %d#%d: bind() to 0.0.0.0:%d failed (98: Address already in use)",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, RandomInt(80, 8080)},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s [emerg] %d#%d: host not found in upstream \"%s\" in /etc/nginx/conf.d/upstream.conf:3\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, RandomFrom("backend-api", "mysql-primary", "redis-cache"), NginxErrorDetails(2000, 7000, rng)},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s [emerg] %d#%d: bind() to 0.0.0.0:%d failed (98: Address already in use)\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, RandomInt(80, 8080), NginxErrorDetails(2000, 7000, rng)},
		},
		{
			Severity: plog.SeverityNumberFatal,
			Format:   "%s [emerg] %d#%d: malloc() failed (12: Cannot allocate memory)\n%s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, NginxErrorDetails(2500, 8000, rng)},
		},
	}
}
