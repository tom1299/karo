package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/tom1299/k8stest"
)

func TestDeploymentRestarts(t *testing.T) {

	ctx := context.Background()
	clients := setupTestClients(t)
	err := clients.controllerManager.Start(ctx)
	if err != nil {
		t.Fatal("failed to start controller manager:", err)
	}

	tests := []struct {
		name            string
		configMapName   string
		deploymentName  string
		restartRuleName string
	}{
		{
			name:            "basic immediate restart",
			configMapName:   "basic-config",
			deploymentName:  "basic-deployment",
			restartRuleName: "basic-restart-rule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			k8sResources := k8stest.New(t, ctx).
				WithDeployment(tt.deploymentName).
				WithConfigMap(tt.configMapName)

			karoResources := From(k8sResources.GetResources())

			karoResources.WithRestartRule(tt.restartRuleName).
				ForConfigMap(tt.configMapName).
				WithTarget(tt.deploymentName)

			if _, err := karoResources.Create(); err != nil {
				t.Errorf("failed to create basic fluentd restart rule test resources: %v", err)
			}

			if err := karoResources.Wait(5 * time.Second); err != nil {
				t.Errorf("failed to wait for resources to be created: %v", err)
			}

			if _, err := karoResources.Delete(); err != nil {
				t.Errorf("failed to delete resources: %v", err)
			}
		})
	}
}
