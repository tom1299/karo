package e2e

import (
	"context"
	"testing"

	"github.com/tom1299/k8stest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDeploymentRestarts(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

			ctx := context.Background()

			k8sResources := k8stest.New(t, ctx).
				WithDeployment(tt.deploymentName).
				WithConfigMap(tt.configMapName)

			karoResources := From(k8sResources.GetResources())

			karoResources.WithRestartRule(tt.restartRuleName).
				ForConfigMap(tt.configMapName).
				WithTarget(tt.deploymentName)

			_, err := karoResources.Create()
			if err != nil {
				t.Fatalf("failed to create resources: %v", err)
			}

			_, err = karoResources.resources.TestClients.ClientSet.AppsV1().Deployments("default").
				Get(ctx, tt.deploymentName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get deployment: %v", err)
			}

			_, err = karoResources.resources.TestClients.ClientSet.CoreV1().ConfigMaps("default").
				Get(ctx, tt.configMapName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get configmap: %v", err)
			}

			restartRule := &karov1alpha1.RestartRule{}
			err = karoResources.resources.TestClients.K8sClient.Get(ctx,
				client.ObjectKey{Namespace: "default", Name: tt.restartRuleName}, restartRule)
			if err != nil {
				t.Fatalf("failed to get restartrule: %v", err)
			}

			_, err = karoResources.Delete()
			if err != nil {
				t.Fatalf("failed to delete resources: %v", err)
			}
		})
	}
}
