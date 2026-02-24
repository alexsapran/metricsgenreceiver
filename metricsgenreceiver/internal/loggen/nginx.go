package loggen

import (
	"go.opentelemetry.io/collector/pdata/plog"
)

var nginxPaths = []string{
	"/api/v1/orders", "/health", "/api/v1/products", "/", "/api/v1/health", "/metrics", "/favicon.ico",
}

func NginxProfile() *AppProfile {
	return &AppProfile{
		Name:            "nginx",
		ScopeName:       "io.opentelemetry.nginx",
		SeverityWeights: DefaultSeverityWeights(),
		Messages: append(
			append(nginxAccessLogs(), nginxDebugLogs()...),
			nginxWarnLogs()...,
		),
	}
}

func nginxAccessLogs() []MessageTemplate {
	tsLayout := "02/Jan/2006:15:04:05 -0700"
	return []MessageTemplate{
		{
			Severity:     plog.SeverityNumberInfo,
			Format:       "%s - - [%s] \"GET %s HTTP/1.1\" %d %d \"-\" \"%s\"",
			Args:         []ArgGenerator{RandomIP, Timestamp(tsLayout), RandomPathWithSuffix([]string{"/api/v1/users/"}, RandomID(8)), RandomHTTPStatus, RandomBytes, RandomUserAgent},
			AttrFromArg:  map[string]int{"net.peer.ip": 0, "http.status_code": 3, "http.url": 2},
			Attrs:        map[string]ArgGenerator{"http.method": HTTPMethod("GET")},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s - - [%s] \"POST %s HTTP/1.1\" %d %d \"%s\" \"%s\"",
			Args: []ArgGenerator{
				RandomIP, Timestamp(tsLayout),
				RandomPath([]string{"/api/v1/orders", "/api/v1/users", "/api/v1/products"}),
				RandomHTTPStatus, RandomBytes,
				RandomFrom("-", "https://example.com/", "https://app.example.com/dashboard"),
				RandomUserAgent,
			},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "http.status_code": 3, "http.url": 2},
			Attrs:       map[string]ArgGenerator{"http.method": HTTPMethod("POST")},
		},
		{
			Severity:     plog.SeverityNumberInfo,
			Format:       "%s - - [%s] \"GET /health HTTP/1.1\" 200 15 \"-\" \"kube-probe/1.28\"",
			Args:         []ArgGenerator{RandomIP, Timestamp(tsLayout)},
			AttrFromArg:  map[string]int{"net.peer.ip": 0},
			Attrs:        map[string]ArgGenerator{"http.method": HTTPMethod("GET"), "http.status_code": HTTPStatus(200), "http.url": Static("/health")},
		},
		{
			Severity:    plog.SeverityNumberInfo,
			Format:      "%s - - [%s] \"GET /static/js/app.%s.js HTTP/1.1\" 304 0 \"%s\" \"%s\"",
			Args:        []ArgGenerator{RandomIP, Timestamp(tsLayout), RandomID(8), RandomFrom("-", "https://app.example.com/"), RandomUserAgent},
			AttrFromArg: map[string]int{"net.peer.ip": 0},
			Attrs:       map[string]ArgGenerator{"http.method": HTTPMethod("GET"), "http.status_code": HTTPStatus(304), "http.url": Static("/static/js/app.js")},
		},
		{
			Severity:    plog.SeverityNumberInfo,
			Format:      "%s - - [%s] \"GET /api/v1/products?page=%d&limit=20 HTTP/1.1\" 200 %d \"-\" \"%s\"",
			Args:        []ArgGenerator{RandomIP, Timestamp(tsLayout), RandomInt(1, 50), RandomBytes, RandomUserAgent},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "http.status_code": 3},
			Attrs:       map[string]ArgGenerator{"http.method": HTTPMethod("GET"), "http.url": Static("/api/v1/products")},
		},
		{
			Severity: plog.SeverityNumberInfo,
			Format:   "%s - - [%s] \"GET %s HTTP/1.1\" %d %d \"-\" \"%s\"",
			Args: []ArgGenerator{
				RandomIP, Timestamp(tsLayout),
				RandomPath([]string{"/", "/api/v1/health", "/metrics", "/favicon.ico", "/api/v1/config"}),
				RandomHTTPStatus, RandomBytes, RandomUserAgent,
			},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "http.status_code": 3, "http.url": 2},
			Attrs:       map[string]ArgGenerator{"http.method": HTTPMethod("GET")},
		},
		{
			Severity:    plog.SeverityNumberInfo,
			Format:      "%s - - [%s] \"DELETE /api/v1/users/%s HTTP/1.1\" %d %d \"-\" \"%s\"",
			Args:        []ArgGenerator{RandomIP, Timestamp(tsLayout), RandomID(8), RandomFromInt(200, 204, 404), RandomBytes, RandomUserAgent},
			AttrFromArg: map[string]int{"net.peer.ip": 0, "http.status_code": 3},
			Attrs:       map[string]ArgGenerator{"http.method": HTTPMethod("DELETE"), "http.url": Static("/api/v1/users")},
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

func nginxWarnLogs() []MessageTemplate {
	tsLayout := "2006/01/02 15:04:05"
	pid := RandomInt(1, 99999)
	tid := RandomInt(0, 1)
	connID := RandomInt(1000, 99999)
	upstreamIP := RandomIP
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
				RandomIP, server, path,
				upstreamIP, port, path,
			},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%s [warn] %d#%d: *%d an upstream response is buffered to a temporary file %s, client: %s, server: %s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, tmpfile, RandomIP, server},
		},
		{
			Severity: plog.SeverityNumberWarn,
			Format:   "%s [warn] %d#%d: *%d upstream timed out, client: %s, server: %s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, RandomIP, server},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d connect() failed (111: Connection refused) while connecting to upstream, client: %s, server: %s, request: \"GET %s HTTP/1.1\", upstream: \"http://%s:%d%s\"",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, RandomIP, server, path, upstreamIP, port, path},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d upstream timed out (110: Connection timed out) while reading response header from upstream, client: %s, server: %s, request: \"POST %s HTTP/1.1\"",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, RandomIP, server, path},
		},
		{
			Severity: plog.SeverityNumberError,
			Format:   "%s [error] %d#%d: *%d no live upstreams while connecting to upstream, client: %s, server: %s",
			Args:     []ArgGenerator{Timestamp(tsLayout), pid, tid, connID, RandomIP, server},
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
	}
}
