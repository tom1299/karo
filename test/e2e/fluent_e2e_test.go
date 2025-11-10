package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/tom1299/k8stest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

func TestDeploymentRestarts(t *testing.T) {

	ctx := context.Background()
	clients := setupTestClients(t)

	namespace := "default"
	setupDelayTestEnvironment(ctx, t, clients, namespace)

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
					Level:   LogLevelDebug,
					Message: "RestartRule deleted from store",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

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
			_, err := k8sResources.WithDeployment(tt.deploymentName).WithConfigMap(tt.configMapName).Create()
			if err != nil {
				t.Fatalf("Failed to create test resources: %v", err)
			}

			err = k8sResources.Wait(10 * time.Second)
			if err != nil {
				t.Fatalf("Test resources did not become ready: %v", err)
			}

			restartRule := &karov1alpha1.RestartRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.restartRuleName,
					Namespace: namespace,
				},
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind:       "ConfigMap",
							Name:       tt.configMapName,
							ChangeType: []string{"Update"},
						},
					},
					Targets: []karov1alpha1.TargetSpec{
						{
							Kind: "Deployment",
							Name: tt.deploymentName,
						},
					},
				},
			}
			if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
				t.Fatalf("Failed to create RestartRule: %v", err)
			}

			t.Log("Waiting for RestartRule to become Active...")
			if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule.Name); err != nil {
				t.Fatalf("RestartRule did not become ready: %v", err)
			}

			initialAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, tt.deploymentName)

			t.Log("Updating ConfigMap to trigger delayed restart...")
			if err := updateDelayTestConfigMapContent(ctx, clients.k8sClient, namespace, tt.configMapName); err != nil {
				t.Fatalf("Failed to update ConfigMap: %v", err)
			}

			t.Log("Waiting for restart to occur...")
			err = wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(
				ctx context.Context) (bool, error) {
				annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, tt.deploymentName)

				return annotation != initialAnnotation, nil
			})
			if err != nil {
				t.Fatalf("Restart did not happen after delay period: %v", err)
			}

			t.Log("SUCCESS: Deployment was restarted after 5 second delay")
			err = k8sResources.Delete()
			if err != nil {
				t.Error("Failed to clean up test resources:", err)
			}

			err = clients.k8sClient.Delete(ctx, restartRule)
			if err != nil {
				t.Errorf("Failed to delete RestartRule: %v", err)
			}
		})
	}
}
