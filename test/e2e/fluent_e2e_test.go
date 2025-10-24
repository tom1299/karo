package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tom1299/k8stest"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type KaroResources struct {
	*k8stest.Resources

	restartRules []*karov1alpha1.RestartRule
	testClients  *k8stest.TestClients
	ctx          context.Context
	timeout      time.Duration
}

type RestartRule struct {
	*KaroResources
}

// New creates a new KaroResources object with embedded k8stest.Resources
func New(t *testing.T, ctx context.Context) *KaroResources {
	return &KaroResources{
		Resources:   k8stest.New(t, ctx),
		testClients: k8stest.SetupTestClients(t),
		ctx:         ctx,
		timeout:     30 * time.Second,
	}
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

	return &RestartRule{kr}
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

func (rs *RestartRule) And() *KaroResources {
	return rs.KaroResources
}

func (kr *KaroResources) Create() (*KaroResources, error) {
	// Ensure the scheme has the RestartRule type registered
	schema := kr.testClients.K8sClient.Scheme()
	err := karov1alpha1.AddToScheme(schema)
	if err != nil {
		return nil, err
	}

	// Create base k8stest resources (deployments, configmaps, secrets, etc.)
	_, err = kr.Resources.Create()
	if err != nil {
		return nil, err
	}

	// Create RestartRules using the controller-runtime client
	for _, restartRule := range kr.restartRules {
		if err := kr.testClients.K8sClient.Create(kr.ctx, restartRule); err != nil {
			return nil, fmt.Errorf("failed to create restart rule: %w", err)
		}
	}

	return kr, nil
}

// Wait waits for all resources to be ready, including RestartRules
func (kr *KaroResources) Wait() (*KaroResources, error) {
	// Wait for base k8stest resources (deployments, secrets, configmaps)
	_, err := kr.Resources.Wait()
	if err != nil {
		return nil, err
	}

	// Wait for RestartRules to be ready (in Active phase with Ready condition)
	for _, restartRule := range kr.restartRules {
		err := wait.PollUntilContextTimeout(kr.ctx, 100*time.Millisecond, kr.timeout, true,
			func(ctx context.Context) (bool, error) {
				rule := &karov1alpha1.RestartRule{}
				if err := kr.testClients.K8sClient.Get(ctx,
					client.ObjectKey{Namespace: restartRule.Namespace, Name: restartRule.Name}, rule); err != nil {
					return false, err
				}

				// Check if the rule is in Active phase
				return rule.Status.Phase == "Active", nil
			})
		if err != nil {
			return nil, fmt.Errorf("failed to wait for restart rule %s: %w", restartRule.Name, err)
		}
	}

	return kr, nil
}

// Delete deletes all resources, including RestartRules
func (kr *KaroResources) Delete() (*KaroResources, error) {
	// Delete RestartRules first
	for _, restartRule := range kr.restartRules {
		err := kr.testClients.K8sClient.Delete(kr.ctx, restartRule)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to delete restart rule: %w", err)
		}
	}

	// Delete base k8stest resources (deployments, configmaps, secrets)
	_, err := kr.Resources.Delete()
	if err != nil {
		return nil, err
	}

	return kr, nil
}

// WithTimeout sets the timeout for Wait operations
func (kr *KaroResources) WithTimeout(timeout time.Duration) *KaroResources {
	kr.timeout = timeout
	kr.Resources.WithTimeout(timeout)
	return kr
}

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
			testResources := New(t, ctx)

			// Build the test resources using the fluent API
			testResources.WithDeployment(tt.deploymentName).
				WithConfigMap(tt.configMapName)
			testResources.WithRestartRule(tt.restartRuleName).
				ForConfigMap(tt.configMapName).
				WithTarget(tt.deploymentName)

			// Create all resources
			_, err := testResources.Create()
			if err != nil {
				t.Fatalf("failed to create resources: %v", err)
			}

			// Wait for all resources to be ready
			_, err = testResources.Wait()
			if err != nil {
				t.Fatalf("failed to wait for resources: %v", err)
			}

			// Clean up resources
			_, err = testResources.Delete()
			if err != nil {
				t.Fatalf("failed to delete resources: %v", err)
			}
		})
	}
}
