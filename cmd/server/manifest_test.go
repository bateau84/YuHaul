package main

import (
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// ADR-007 required test 9: the Deployment gives the in-process shutdown budget
// (15 s run drain + 10 s HTTP + 15 s import = 40 s) room before SIGKILL, keeps
// the Recreate strategy that ADR-005's single-owner premise needs, and leaves
// the probes as they were.
func TestDeploymentManifestShutdownContract(t *testing.T) {
	raw, err := os.ReadFile("../../k8s/deployment.yaml")
	require.NoError(t, err)
	manifest := string(raw)

	grace := regexp.MustCompile(`(?m)^      terminationGracePeriodSeconds: (\d+)\s*$`).FindStringSubmatch(manifest)
	require.NotNil(t, grace, "spec.template.spec.terminationGracePeriodSeconds must be set")
	seconds, err := strconv.Atoi(grace[1])
	require.NoError(t, err)
	require.GreaterOrEqual(t, seconds, 60)

	require.Regexp(t, `(?m)^  strategy:\n    type: Recreate\n`, manifest)

	require.Contains(t, manifest, `          livenessProbe:
            httpGet:
              path: /api/overview
              port: http
            initialDelaySeconds: 10
            periodSeconds: 30
          readinessProbe:
            httpGet:
              path: /api/overview
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
`, "the probes are unchanged")
	require.NotContains(t, manifest, "startupProbe", "ADR-007 adds no startup probe")
}
