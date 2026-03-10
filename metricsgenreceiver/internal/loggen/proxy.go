package loggen

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"

	"go.opentelemetry.io/collector/pdata/plog"
)


const proxyPoolSize = 4096

// proxyLogNormalPool pre-generates log-normally distributed ints.
func proxyLogNormalPool(rng *rand.Rand, median, sigma float64) ArgGenerator {
	pool := make([]int, proxyPoolSize)
	for i := range pool {
		v := median * math.Exp(sigma*rng.NormFloat64())
		if v < 0 {
			v = 0
		}
		pool[i] = int(math.Round(v))
	}
	return func(r *rand.Rand, _ GenContext) any {
		return pool[r.Intn(proxyPoolSize)]
	}
}

// proxyLogNormalPoolClamped is like proxyLogNormalPool but caps values at maxVal.
func proxyLogNormalPoolClamped(rng *rand.Rand, median, sigma float64, maxVal int) ArgGenerator {
	pool := make([]int, proxyPoolSize)
	for i := range pool {
		v := median * math.Exp(sigma*rng.NormFloat64())
		if v < 0 {
			v = 0
		}
		iv := int(math.Round(v))
		if iv > maxVal {
			iv = maxVal
		}
		pool[i] = iv
	}
	return func(r *rand.Rand, _ GenContext) any {
		return pool[r.Intn(proxyPoolSize)]
	}
}

// proxyZipfPool pre-computes Zipfian selection indices.
func proxyZipfPool(ipPool []string, rng *rand.Rand, skew float64) ArgGenerator {
	size := len(ipPool)
	indices := make([]int, proxyPoolSize)
	zipf := rand.NewZipf(rng, skew, 1, uint64(size-1))
	for i := range indices {
		indices[i] = int(zipf.Uint64())
	}
	return func(r *rand.Rand, _ GenContext) any {
		return ipPool[indices[r.Intn(proxyPoolSize)]]
	}
}

type weightedStr struct {
	val    string
	weight int
}

type weightedInt struct {
	val    int
	weight int
}

// proxyWeightedStrPool pre-computes a weighted selection pool for strings.
func proxyWeightedStrPool(rng *rand.Rand, choices []weightedStr) ArgGenerator {
	total := 0
	for _, c := range choices {
		total += c.weight
	}
	pool := make([]string, proxyPoolSize)
	for i := range pool {
		n := rng.Intn(total)
		for _, c := range choices {
			n -= c.weight
			if n < 0 {
				pool[i] = c.val
				break
			}
		}
	}
	return func(r *rand.Rand, _ GenContext) any {
		return pool[r.Intn(proxyPoolSize)]
	}
}

// proxyWeightedIntPool pre-computes a weighted selection pool for ints.
func proxyWeightedIntPool(rng *rand.Rand, choices []weightedInt) ArgGenerator {
	total := 0
	for _, c := range choices {
		total += c.weight
	}
	pool := make([]int, proxyPoolSize)
	for i := range pool {
		n := rng.Intn(total)
		for _, c := range choices {
			n -= c.weight
			if n < 0 {
				pool[i] = c.val
				break
			}
		}
	}
	return func(r *rand.Rand, _ GenContext) any {
		return pool[r.Intn(proxyPoolSize)]
	}
}

func ProxyProfile(rng *rand.Rand, ipCfg *IPPoolConfig) *AppProfile {
	if rng == nil {
		rng = rand.New(rand.NewSource(0))
	}

	// --- Pre-generate deterministic pools ---

	// Handling pods: ~895 unique in production
	podPool := make([]string, 200)
	podPrefixes := []string{"svc-search", "svc-data", "svc-master", "svc-ingest", "svc-dashboard", "svc-tracing"}
	for i := range podPool {
		podPool[i] = podPrefixes[rng.Intn(len(podPrefixes))] + "-" + randomHexString(rng, 8) + "-" + randomHexString(rng, 5)
	}

	// Handling nodes: ~126 unique
	nodePool := make([]string, 30)
	nodeAZs := []string{"eu-west-1a", "eu-west-1b", "eu-west-1c"}
	for i := range nodePool {
		a, b, c := rng.Intn(256), rng.Intn(256), rng.Intn(256)
		nodePool[i] = fmt.Sprintf("ip-10-%d-%d-%d.%s.compute.internal", a, b, c, nodeAZs[rng.Intn(len(nodeAZs))])
	}

	// Handling servers: ~836 unique (internal IPs)
	serverPool := make([]string, 800)
	for i := range serverPool {
		serverPool[i] = fmt.Sprintf("100.64.%d.%d", rng.Intn(256), rng.Intn(256))
	}

	// Organization IDs: ~43 unique
	orgPool := make([]string, 43)
	for i := range orgPool {
		orgPool[i] = strconv.Itoa(rng.Intn(3900000000) + 100000000)
	}

	// Handling projects: hex32 IDs, ~301 unique
	projectPool := make([]string, 100)
	for i := range projectPool {
		projectPool[i] = randomHexString(rng, 32)
	}

	// Handling applications: {project}.{type}, ~557 unique
	appPool := make([]string, 150)
	appSuffixes := []string{".search", ".search", ".search", ".search", ".search", ".dashboard", ".tracing", ".ingest", ".agent"}
	for i := range appPool {
		appPool[i] = projectPool[rng.Intn(len(projectPool))] + appSuffixes[rng.Intn(len(appSuffixes))]
	}

	// Handling namespaces: project-{hex32}
	nsPool := make([]string, len(projectPool))
	for i := range nsPool {
		nsPool[i] = "project-" + projectPool[i]
	}

	// Request hosts: {hex32}.{type}.{region}.cloud.example.internal, ~771 unique
	hostPool := make([]string, 200)
	hostTypes := []string{"search", "search", "search", "dashboard", "tracing"}
	regions := []string{"eu-west-1", "us-east-1", "ap-southeast-1"}
	for i := range hostPool {
		proj := projectPool[rng.Intn(len(projectPool))]
		hostPool[i] = proj + "." + hostTypes[rng.Intn(len(hostTypes))] + "." + regions[rng.Intn(len(regions))] + ".cloud.example.internal"
	}

	// Client meta: ~31 unique (synthetic version combos)
	clientMetaPool := []string{
		"es=8.0.0,js=18.0.0,t=8.0.0,hc=18.0.0",
		"es=7.5.0,py=3.10.0,t=7.3.0,ai=3.10.0",
		"es=8.1.0,go=1.21.0,t=8.1.0,hc=1.21.0",
		"es=8.2.0,js=18.1.0,t=8.2.0,hc=18.1.0",
		"es=8.0.0,js=18.0.0,t=8.0.0,hc=18.0.0,h=bp",
		"es=7.9.0,go=1.20.0,t=7.6.0,hc=1.20.0",
		"es=7.5.0,py=3.11.0,t=7.3.0,ai=3.11.0",
		"es=8.3.0,js=20.0.0,t=8.3.0,hc=20.0.0",
		"es=7.8.0,js=18.2.0,t=7.5.0,hc=18.2.0,h=bp",
		"es=8.1.1,go=1.22.0,t=8.1.0,hc=1.22.0,hl=1",
	}

	// Auth user pool (5% non-empty)
	authPool := make([]string, 18)
	authPrefixes := []string{"svc-account-", "internal-", "system-"}
	for i := range authPool {
		authPool[i] = authPrefixes[rng.Intn(len(authPrefixes))] + randomHexString(rng, 8)
	}

	// IP pool
	cidrs := []string{"10.0.0.0/8", "100.64.0.0/10"}
	skew := 1.5
	poolSize := 5000
	if ipCfg != nil {
		if len(ipCfg.CIDRs) > 0 {
			cidrs = ipCfg.CIDRs
		}
		if ipCfg.ZipfSkew > 1.0 {
			skew = ipCfg.ZipfSkew
		}
		if ipCfg.PoolSize > 0 {
			poolSize = ipCfg.PoolSize
		}
	}
	ipList := buildIPPool(rng, cidrs, poolSize)
	clientIPGen := proxyZipfPool(ipList, rng, skew)

	// --- Weighted generators matching real distributions ---

	uuidGen := RandomUUID

	staticPathGen := proxyWeightedStrPool(rng, []weightedStr{
		{"/_bulk", 180}, {"/_security/_authenticate", 124}, {"/", 66},
		{"/_security/user/_has_privileges", 54},
		{"/.sysidx_task_manager/_msearch", 45},
		{"/.sysidx_task_manager/_search", 31},
		{"/_msearch", 22},
		{"/_nodes/_all/_none", 18},
		{"/.sysidx_ingest/_search", 17},
		{"/.sysidx_task_manager_v2/_search", 13},
		{"/.sysidx_task_manager/_bulk", 13},
		{"/_tasks", 11}, {"/.sysidx_config/_search", 9},
		{"/_xpack", 8}, {"/_mget", 8},
		{"/_cluster/health", 8}, {"/_cluster/state/master_node", 8},
		{"/_resolve/index/*", 8}, {"/_cat/shards", 8},
		{"/_nodes/stats/breaker,fs,http,indices,jvm,os,process,thread_pool,transport", 8},
		{"/.sysidx_config/_doc/config:default", 8},
		{"/_search", 7}, {"/.sysidx_task_manager/_mget", 6},
		{"/_pit", 6}, {"/_health", 6},
		{"/_cluster/settings", 5}, {"/intake/v2/events", 4},
		{"/.ds-logs-generic-default/_bulk", 20},
		{"/.ds-metrics-generic-default/_bulk", 15},
		{"/.ds-traces-generic-default/_bulk", 10},
		{"/_ml/anomaly_detectors/_stats", 3},
		{"/_alias", 3}, {"/_mapping", 3},
		{"/_index_template", 2}, {"/_ingest/pipeline", 2},
	})

	// Dynamic path pool: pre-generate ~8000 unique paths from templates with IDs
	dynamicPathTemplates := []string{
		"/.ds-logs-%s-default/_bulk",
		"/.ds-metrics-%s-default/_bulk",
		"/.ds-traces-%s-default/_bulk",
		"/%s/_search",
		"/%s/_bulk",
		"/%s/_doc/%s",
		"/%s/_update/%s",
		"/%s/_mapping",
		"/api/v1/%s/%s",
		"/.kibana_%s/_search",
		"/.sysidx_%s/_search",
		"/.sysidx_%s/_bulk",
		"/%s/_count",
		"/%s/_refresh",
		"/%s/_settings",
	}
	dynamicDatasets := []string{
		"nginx.access", "system.syslog", "kubernetes.pod", "apm.app",
		"elastic_agent", "endpoint.events", "cloud.audit", "fleet_server",
		"synthetics.http", "profiling.events", "logs.generic", "metrics.generic",
		"system.auth", "kubernetes.container", "kubernetes.event", "apm.error",
		"apm.transaction", "elastic_agent.metricbeat", "endpoint.alerts",
		"cloud.vpcflow", "osquery_manager.result", "ti_abusech.malware",
	}
	dynamicIndices := []string{
		".ds-logs-nginx.access-default-2024.01",
		".ds-logs-system.syslog-default-2024.01",
		".ds-metrics-kubernetes.pod-default-2024.01",
		"logs-apm.app-default",
		".fleet-actions-results",
		".kibana_analytics",
		".kibana_security_session",
		".ds-logs-system.auth-default-2024.01",
		".ds-metrics-kubernetes.container-default-2024.01",
		".ds-logs-elastic_agent-default-2024.01",
		".ds-logs-endpoint.alerts-default-2024.01",
		".ds-logs-cloud.vpcflow-default-2024.01",
		".internal.alerts-security.alerts-default-2024.01",
	}
	const dynamicPathPoolSize = 16384
	dynamicPathPool := make([]string, dynamicPathPoolSize)
	for i := range dynamicPathPool {
		tpl := dynamicPathTemplates[rng.Intn(len(dynamicPathTemplates))]
		nPlaceholders := strings.Count(tpl, "%s")
		switch nPlaceholders {
		case 1:
			item := dynamicDatasets[rng.Intn(len(dynamicDatasets))]
			if rng.Intn(2) == 0 {
				item = dynamicIndices[rng.Intn(len(dynamicIndices))]
			}
			dynamicPathPool[i] = fmt.Sprintf(tpl, item)
		default:
			item := dynamicIndices[rng.Intn(len(dynamicIndices))]
			id := randomHexString(rng, 12)
			dynamicPathPool[i] = fmt.Sprintf(tpl, item, id)
		}
	}
	dynamicPathGen := func(r *rand.Rand, _ GenContext) any {
		return dynamicPathPool[r.Intn(dynamicPathPoolSize)]
	}

	// 60% static weighted paths, 40% dynamic paths
	requestPathGen := func(r *rand.Rand, ctx GenContext) any {
		if r.Intn(10) < 6 {
			return staticPathGen(r, ctx)
		}
		return dynamicPathGen(r, ctx)
	}

	methodGen := proxyWeightedStrPool(rng, []weightedStr{
		{"GET", 430}, {"POST", 380}, {"PUT", 180}, {"DELETE", 7}, {"HEAD", 3},
	})

	statusGen := proxyWeightedIntPool(rng, []weightedInt{
		{200, 8540}, {404, 350}, {202, 42}, {429, 37},
		{201, 28}, {409, 15}, {401, 12}, {400, 8},
		{302, 4}, {503, 3}, {304, 1},
	})

	actionGen := proxyWeightedStrPool(rng, []weightedStr{
		{"bulk", 194}, {"search", 180}, {"security", 179}, {"other", 161},
		{"get", 83}, {"nodes", 26}, {"index", 25}, {"cluster", 21},
		{"tasks", 11}, {"xpack", 8}, {"cat", 8},
		{"exists/index", 2}, {"metadata", 2}, {"ml", 1}, {"head", 1},
		{"ingest", 1}, {"refresh", 1},
	})

	routingDecGen := proxyWeightedStrPool(rng, []weightedStr{
		{"primary:read,same_az", 700}, {"primary:write,same_az", 278}, {"fallback:preferred_zone", 17},
	})

	appTypeGen := proxyWeightedStrPool(rng, []weightedStr{
		{"search", 880}, {"", 7}, {"ingest", 6}, {"dashboard", 5}, {"tracing", 4}, {"agent", 1},
	})

	resolutionGen := proxyWeightedStrPool(rng, []weightedStr{
		{"RESOURCE_ID", 780}, {"ALIAS", 220},
	})

	requestSourceGen := proxyWeightedStrPool(rng, []weightedStr{
		{"internal", 770}, {"external", 230},
	})

	statusReasonGen := proxyWeightedStrPool(rng, []weightedStr{
		{"-", 9950}, {"INITIALIZING", 40}, {"RESOURCE_NOT_FOUND", 5},
		{"CLIENT_CLOSED_CONNECTION", 3}, {"NO_AVAILABLE_INSTANCES", 2},
	})

	serverlessTypeGen := proxyWeightedStrPool(rng, []weightedStr{
		{"observability", 520}, {"security", 300}, {"search", 170}, {"assistant", 10},
	})

	zoneGen := proxyWeightedStrPool(rng, []weightedStr{
		{"eu-west-1c", 560}, {"eu-west-1b", 250}, {"eu-west-1a", 180}, {"", 10},
	})

	userAgentGen := proxyWeightedStrPool(rng, []weightedStr{
		{"Dashboard/8.0.0", 560},
		{"search-client-py/7.5.0 (Python/3.10.0; transport/7.3.0)", 150},
		{"Metrics-Agent/8.1.0 (linux; arm64; a1b2c3d4)", 80},
		{"Agent-Server/8.0.0 (linux; arm64; d4c3b2a1)", 24},
		{"load-generator", 16},
		{"OTel-Collector/1.0.0 (linux/amd64)", 10},
		{"ELB-HealthChecker/2.0", 4},
		{"kube-probe/1.30+", 2},
		{"Index-Service/e5f6a7b8 transport-go/8.0.0", 2},
		{"log-shipper/ (linux/arm64)", 2},
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0", 3},
	})

	backendProtoGen := proxyWeightedStrPool(rng, []weightedStr{
		{"HTTP/1.1", 970}, {"HTTP/2.0", 30},
	})

	requestProtoGen := proxyWeightedStrPool(rng, []weightedStr{
		{"HTTP/1.1", 970}, {"HTTP/2.0", 30},
	})

	portGen := proxyWeightedIntPool(rng, []weightedInt{
		{443, 992}, {9400, 5}, {9243, 2}, {9843, 1},
	})

	handlingPortGen := proxyWeightedIntPool(rng, []weightedInt{
		{9200, 970}, {0, 12}, {5601, 5}, {8200, 5}, {4317, 4}, {8443, 2}, {8220, 1}, {9843, 1},
	})

	tlsVersionGen := proxyWeightedIntPool(rng, []weightedInt{
		{772, 997}, {771, 3},
	})

	tlsCipherGen := proxyWeightedIntPool(rng, []weightedInt{
		{4865, 850}, {49199, 100}, {49195, 30}, {49200, 20},
	})

	// Numeric distributions calibrated from real percentiles
	respTimeGen := proxyLogNormalPool(rng, 5, 2.2)
	backendRespTimeGen := proxyLogNormalPool(rng, 5, 2.2)
	proxyTimeGen := proxyLogNormalPool(rng, 107, 0.3)
	reqLenGen := proxyLogNormalPool(rng, 166, 3.5)
	respLenGen := proxyLogNormalPool(rng, 482, 3.0)
	connConcGen := proxyLogNormalPoolClamped(rng, 12, 1.5, 1000)
	reqConcGen := proxyLogNormalPoolClamped(rng, 3, 1.5, 300)

	// Cheap pool-based generators
	podGen := RandomFrom(podPool...)
	nodeGen := RandomFrom(nodePool...)
	nsGen := RandomFrom(nsPool...)
	hostGen := RandomFrom(hostPool...)
	serverGen := RandomFrom(serverPool...)
	orgGen := RandomFrom(orgPool...)
	projGen := RandomFrom(projectPool...)
	appGen := RandomFrom(appPool...)
	authGen := RandomFrom(authPool...)
	metaGen := RandomFrom(clientMetaPool...)

	var msgs []MessageTemplate

	// --- INFO: access logs (85% of traffic, empty body, ~14 core attrs) ---
	// Lean hot-path: only the most distinctive fields. Full field set on WARN/ERROR.
	infoAttrs := []AttrGen{
		{"request_id", uuidGen},
		{"request_method", methodGen},
		{"request_path", requestPathGen},
		{"status_code", statusGen},
		{"action", actionGen},
		{"response_time", respTimeGen},
		{"proxy_internal_time_us", proxyTimeGen},
		{"request_length", reqLenGen},
		{"response_length", respLenGen},
		{"client_ip", clientIPGen},
		{"request_host", hostGen},
		{"handling_pod", podGen},
		{"handling_zone", zoneGen},
	}
	infoRare := []RareAttrGen{
		{"user_agent.original", 0.31, userAgentGen},
		{"serverless.project.type", 0.30, serverlessTypeGen},
		{"tls_version", 0.15, tlsVersionGen},
		{"tls_cipher", 0.15, tlsCipherGen},
		{"request_source", 0.20, requestSourceGen},
		{"request_proto", 0.15, requestProtoGen},
		{"client_meta", 0.10, metaGen},
		{"handling_project", 0.20, projGen},
		{"request_port", 0.15, portGen},
		{"auth_user", 0.03, authGen},
	}
	// Two copies to give the full request template 2x weight vs the health-check template.
	msgs = append(msgs,
		MessageTemplate{Severity: plog.SeverityNumberInfo, Attrs: infoAttrs, RareAttrs: infoRare},
		MessageTemplate{Severity: plog.SeverityNumberInfo, Attrs: infoAttrs, RareAttrs: infoRare},
	)

	// INFO: health check (lighter, ~12 attrs)
	healthAttrs := []AttrGen{
		{"request_id", uuidGen},
		{"connection_id", uuidGen},
		{"request_method", Static("GET")},
		{"request_path", Static("/_health")},
		{"status_code", HTTPStatus(200)},
		{"action", Static("other")},
		{"response_time", RandomInt(0, 1)},
		{"proxy_internal_time_us", proxyTimeGen},
		{"client_ip", clientIPGen},
		{"user_agent.original", Static("LB-HealthChecker/2.0")},
		{"request_port", HTTPStatus(9400)},
		{"status_reason", statusReasonGen},
	}
	msgs = append(msgs, MessageTemplate{Severity: plog.SeverityNumberInfo, Attrs: healthAttrs})

	// --- DEBUG: routing details (~15 attrs) ---
	debugAttrs := []AttrGen{
		{"request_id", uuidGen},
		{"connection_id", uuidGen},
		{"request_method", methodGen},
		{"request_path", requestPathGen},
		{"action", actionGen},
		{"routing_decision", routingDecGen},
		{"backend_response_time", backendRespTimeGen},
		{"handling_pod", podGen},
		{"handling_node", nodeGen},
		{"handling_zone", zoneGen},
		{"handling_namespace", nsGen},
		{"handling_server", serverGen},
		{"handling_port", handlingPortGen},
		{"backend_proto", backendProtoGen},
		{"client_ip", clientIPGen},
	}
	msgs = append(msgs, MessageTemplate{Severity: plog.SeverityNumberDebug, Attrs: debugAttrs})

	// --- WARN: slow/throttled requests (~25 fields) ---
	warnAttrs := []AttrGen{
		{"request_id", uuidGen},
		{"connection_id", uuidGen},
		{"backend_connection_id", uuidGen},
		{"request_method", methodGen},
		{"request_path", requestPathGen},
		{"status_code", proxyWeightedIntPool(rng, []weightedInt{{429, 50}, {409, 20}, {200, 20}, {503, 10}})},
		{"action", actionGen},
		{"application_type", appTypeGen},
		{"response_time", proxyLogNormalPool(rng, 500, 0.8)},
		{"backend_response_time", proxyLogNormalPool(rng, 400, 0.8)},
		{"proxy_internal_time_us", proxyTimeGen},
		{"request_length", reqLenGen},
		{"response_length", respLenGen},
		{"route_connection_concurrency", connConcGen},
		{"route_request_concurrency", reqConcGen},
		{"client_ip", clientIPGen},
		{"request_host", hostGen},
		{"handling_pod", podGen},
		{"handling_node", nodeGen},
		{"handling_zone", zoneGen},
		{"handling_application", appGen},
		{"routing_decision", routingDecGen},
		{"organization_id", orgGen},
		{"user_agent.original", userAgentGen},
		{"status_reason", proxyWeightedStrPool(rng, []weightedStr{{"INITIALIZING", 50}, {"-", 30}, {"NO_AVAILABLE_INSTANCES", 20}})},
	}
	msgs = append(msgs,
		MessageTemplate{Severity: plog.SeverityNumberWarn, Attrs: warnAttrs},
	)

	// --- ERROR: upstream failures (~25 fields) ---
	errAttrs := []AttrGen{
		{"request_id", uuidGen},
		{"connection_id", uuidGen},
		{"backend_connection_id", uuidGen},
		{"request_method", methodGen},
		{"request_path", requestPathGen},
		{"status_code", proxyWeightedIntPool(rng, []weightedInt{{503, 40}, {500, 20}, {404, 20}, {401, 10}, {400, 10}})},
		{"action", actionGen},
		{"application_type", appTypeGen},
		{"response_time", proxyLogNormalPool(rng, 1000, 1.0)},
		{"backend_response_time", proxyLogNormalPool(rng, 800, 1.0)},
		{"proxy_internal_time_us", proxyTimeGen},
		{"request_length", reqLenGen},
		{"response_length", respLenGen},
		{"route_connection_concurrency", connConcGen},
		{"route_request_concurrency", reqConcGen},
		{"client_ip", clientIPGen},
		{"request_host", hostGen},
		{"handling_pod", podGen},
		{"handling_node", nodeGen},
		{"handling_zone", zoneGen},
		{"handling_application", appGen},
		{"routing_decision", routingDecGen},
		{"resolution_type", resolutionGen},
		{"organization_id", orgGen},
		{"user_agent.original", userAgentGen},
		{"status_reason", proxyWeightedStrPool(rng, []weightedStr{{"RESOURCE_NOT_FOUND", 40}, {"-", 30}, {"NO_AVAILABLE_INSTANCES", 20}, {"CLIENT_CLOSED_CONNECTION", 10}})},
	}
	msgs = append(msgs,
		MessageTemplate{Severity: plog.SeverityNumberError, Attrs: errAttrs},
	)

	// --- FATAL: proxy crash (minimal) ---
	fatalAttrs := []AttrGen{
		{"handling_pod", podGen},
		{"handling_node", nodeGen},
		{"handling_zone", zoneGen},
		{"status_reason", Static("NO_AVAILABLE_INSTANCES")},
	}
	msgs = append(msgs,
		MessageTemplate{Severity: plog.SeverityNumberFatal, Attrs: fatalAttrs},
	)

	crossCutting := ErrorMessageAttrs(rng, GoStackTrace(800, 8500, rng))
	longTail := LongTailAttrs(rng)
	return &AppProfile{
		Name:             "proxy",
		ScopeName:        "io.opentelemetry.proxy",
		SeverityWeights:  DefaultSeverityWeights(),
		EmitTraceContext: true,
		Messages:         appendCrossCutting(msgs, crossCutting),
		LongTail:         longTail,
	}
}
