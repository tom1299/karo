package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/tom1299/k8stest"
	"k8s.io/apimachinery/pkg/util/wait"
)

// TODO: Reduce complexity
// TODO: Use testify
func TestDeploymentRestarts(t *testing.T) {

	ctx := context.Background()
	clients := setupTestClients(t)

	namespace := "default"
	SetupTestContext(ctx, t, clients)

	tests := []struct {
		name               string
		configMapName      string
		deploymentName     string
		restartRuleName    string
		expectedLogEntries []ExpectedLogEntry
	}{
		{
			name:            "basic immediate restart",
			configMapName:   "basic-config",
			deploymentName:  "basic-deployment",
			restartRuleName: "basic-restart-rule",
			expectedLogEntries: []ExpectedLogEntry{
				{
					Level:   LogLevelDebug,
					Message: "RestartRule added/updated in store",
				},
				{
					Level:   LogLevelInfo,
					Message: "Successfully restarted deployment immediately",
				},
				{
					Level:   LogLevelDebug,
					Message: "RestartRule deleted from store",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// TODO: Refactor creation of k8sResources into a helper function
			k8sResources := k8stest.Resources{
				Deployments:  nil,
				StatefulSets: nil,
				ConfigMaps:   nil,
				Secrets:      nil,
				Options:      nil,
				TestClients: &k8stest.TestClients{
					ClientSet: clients.clientset,
					K8sClient: clients.k8sClient,
				},
				Ctx:     ctx,
				Timeout: 30 * time.Second,
			}

			deployment := k8sResources.WithDeployment(tt.deploymentName).
				WithConfigMap(tt.configMapName)

			karoResources, err := From(deployment.GetResources()).
				WithRestartRule(tt.restartRuleName).
				ForConfigMap(tt.configMapName).WithTarget(tt.deploymentName).Create()

			if err != nil {
				t.Fatalf("Test resources could not be created: %v", err)
			}

			err = karoResources.Wait(10 * time.Second)
			if err != nil {
				t.Fatalf("Test resources not ready in time: %v", err)
			}

			initialAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, tt.deploymentName)

			t.Log("Updating ConfigMap to trigger delayed restart...")
			if err := updateDelayTestConfigMapContent(ctx, clients.k8sClient, namespace, tt.configMapName); err != nil {
				t.Fatalf("Failed to update ConfigMap: %v", err)
			}

			// TODO: Refactor this into helper function
			t.Log("Waiting for restart to occur...")
			err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(
				ctx context.Context) (bool, error) {
				annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, tt.deploymentName)

				return annotation != initialAnnotation, nil
			})
			if err != nil {
				t.Fatalf("Restart did not happen: %v", err)
			}

			if !karoResources.RestartRuleStatus(tt.restartRuleName).ShouldContainRestartFor(tt.deploymentName, true) {
				t.Errorf("Expected restart for deployment %s not recorded in RestartRule status", tt.deploymentName)
			}

			if !karoResources.DeploymentHasBeenRolled(tt.deploymentName, 1) {
				t.Errorf("Deployment %s was not rolled as expected", tt.deploymentName)
			}

			_, err = karoResources.Delete()
			if err != nil {
				t.Error("Failed to clean up test resources:", err)
			}

			karoLogs := GetKaroLogs(clients.controllerManager)

			if !karoLogs.ContainLogEntriesInSequence(tt.expectedLogEntries) || !karoLogs.ContainNoErrorsOrStacktrace() {
				t.Errorf("Controller logs did not contain expected entries or contained errors/stacktraces")
			}
		})
	}
}
