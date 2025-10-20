package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/tom1299/k8stest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

type KaroResources struct {
	k8stest.Resources

	restartRules []*karov1alpha1.RestartRule
}

type RestartRule struct {
	KaroResources
}

func (kr *KaroResources) WithRestartRule(name string) *RestartRule {
	restartRule := karov1alpha1.RestartRule{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RestartRule",
			APIVersion: "v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{},
			Targets: []karov1alpha1.TargetSpec{},
		},
	}
	kr.restartRules = append(kr.restartRules, &restartRule)

	return &RestartRule{*kr}
}

func (rs RestartRule) ForConfigMap(configMapName string) *RestartRule {
	restartRule := rs.restartRules[len(rs.restartRules)-1]
	restartRule.Spec.Changes = append(restartRule.Spec.Changes,
		karov1alpha1.ChangeSpec{
			Kind:    "ConfigMap",
			Name:    configMapName,
			IsRegex: false,
		})

	return &rs
}

func (rs RestartRule) WithTarget(deploymentName string) *RestartRule {
	restartRule := rs.restartRules[len(rs.restartRules)-1]
	restartRule.Spec.Targets = append(restartRule.Spec.Targets,
		karov1alpha1.TargetSpec{
			Kind:      "Deployment",
			Name:      deploymentName,
			Namespace: "default",
		})

	return &rs
}

func (kr *KaroResources) Create(testClients *k8stest.TestClients, ctx context.Context) (*KaroResources, error) {
	schema := testClients.K8sClient.Scheme()
	err := karov1alpha1.AddToScheme(schema)
	if err != nil {
		return nil, err
	}

	_, err = kr.Resources.Create(testClients, context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}

	for _, restartRule := range kr.restartRules {
		if err := testClients.K8sClient.Create(ctx, restartRule); err != nil {
			return nil, fmt.Errorf("failed to create restart rule: %w", err)
		}
	}

	return kr, nil
}

func TestDeploymentRestarts(t *testing.T) {
	t.Parallel()
	testClients := k8stest.SetupTestClients(t)

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

			testResources := KaroResources{}
			testResources.WithDeployment(tt.deploymentName).
				WithConfigMap(tt.configMapName)
			testResources.WithRestartRule(tt.configMapName).
				ForConfigMap(tt.configMapName).
				WithTarget(tt.deploymentName)

			_, err := testResources.Create(testClients, context.Background())

			if err != nil {
				t.Fatalf("failed to create configmap: %v", err)
			}
		})
	}
}
