package logstmpl

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
)

func TestRenderLogsTemplate_Builtin(t *testing.T) {
	builtins := []string{
		"builtin/simple",
		"builtin/k8s-nginx",
		"builtin/k8s-mysql",
		"builtin/k8s-redis",
		"builtin/k8s-goapp",
	}
	for _, path := range builtins {
		t.Run(path, func(t *testing.T) {
			logs, err := RenderLogsTemplate(path, nil)
			require.NoError(t, err)
			require.NotEqual(t, plog.Logs{}, logs)
			assert.Greater(t, logs.ResourceLogs().Len(), 0, "must have at least one ResourceLogs")
		})
	}
}

func TestGetLogResources_Scale(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(42))

	resources, err := GetLogResources("builtin/simple", startTime, 10, nil, rng)
	require.NoError(t, err)
	require.Len(t, resources, 10)

	podNames := make(map[string]struct{})
	for i, r := range resources {
		v, ok := r.Attributes().Get("k8s.pod.name")
		require.True(t, ok, "resource %d must have k8s.pod.name", i)
		podNames[v.Str()] = struct{}{}
	}
	assert.Len(t, podNames, 10, "scale=10 must produce 10 distinct k8s.pod.name values")
}

func TestGetLogResources_Deterministic(t *testing.T) {
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	path := "builtin/k8s-nginx" // uses RandomHex, UUID - RNG-dependent

	rng1 := rand.New(rand.NewSource(42))
	rng2 := rand.New(rand.NewSource(42))

	res1, err := GetLogResources(path, startTime, 5, nil, rng1)
	require.NoError(t, err)
	res2, err := GetLogResources(path, startTime, 5, nil, rng2)
	require.NoError(t, err)

	require.Len(t, res1, 5)
	require.Len(t, res2, 5)

	for i := 0; i < 5; i++ {
		json1 := resourceToJSON(res1[i])
		json2 := resourceToJSON(res2[i])
		assert.Equal(t, json1, json2, "resource %d must match", i)
	}
}

func resourceToJSON(r pcommon.Resource) string {
	// Build a minimal comparable representation
	m := make(map[string]string)
	r.Attributes().Range(func(k string, v pcommon.Value) bool {
		m[k] = v.AsString()
		return true
	})
	data, _ := json.Marshal(m)
	return string(data)
}

func TestRenderLogsTemplate_ExternalPath(t *testing.T) {
	dir := t.TempDir()
	templateContent := `resourceLogs:
  - resource:
      attributes:
        - key: service.name
          value:
            stringValue: "external-{{.InstanceID}}"
    scopeLogs:
      - scope:
          name: "log-generator"
        logRecords: []
`
	templatePath := filepath.Join(dir, "custom-resource-attributes.yaml")
	require.NoError(t, os.WriteFile(templatePath, []byte(templateContent), 0600))

	path := filepath.Join(dir, "custom")
	model := &resourceTemplateModel{InstanceID: 3, rand: rand.New(rand.NewSource(1))}

	logs, err := RenderLogsTemplate(path+"-resource-attributes", model)
	require.NoError(t, err)
	require.Greater(t, logs.ResourceLogs().Len(), 0)
	r := logs.ResourceLogs().At(0).Resource()
	v, ok := r.Attributes().Get("service.name")
	require.True(t, ok)
	assert.Equal(t, "external-3", v.Str())
}

func TestGetLogResources_ExternalPath(t *testing.T) {
	dir := t.TempDir()
	templateContent := `resourceLogs:
  - resource:
      attributes:
        - key: service.name
          value:
            stringValue: "svc-{{.InstanceID}}"
        - key: k8s.pod.name
          value:
            stringValue: "pod-{{.InstanceID}}"
    scopeLogs:
      - scope:
          name: "log-generator"
        logRecords: []
`
	templatePath := filepath.Join(dir, "mytemplate-resource-attributes.yaml")
	require.NoError(t, os.WriteFile(templatePath, []byte(templateContent), 0600))

	path := filepath.Join(dir, "mytemplate")
	startTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	rng := rand.New(rand.NewSource(7))

	resources, err := GetLogResources(path, startTime, 3, nil, rng)
	require.NoError(t, err)
	require.Len(t, resources, 3)

	svc0, _ := resources[0].Attributes().Get("service.name")
	svc1, _ := resources[1].Attributes().Get("service.name")
	svc2, _ := resources[2].Attributes().Get("service.name")
	assert.Equal(t, "svc-0", svc0.Str())
	assert.Equal(t, "svc-1", svc1.Str())
	assert.Equal(t, "svc-2", svc2.Str())
}
