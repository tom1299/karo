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
	resources *k8stest.Resources

	restartRules []*karov1alpha1.RestartRule
}

type RestartRule struct {
	*KaroResources
}

// New creates a new KaroResources object with embedded k8stest.Resources
func New(t *testing.T, ctx context.Context) *KaroResources {
	return &KaroResources{
		resources: k8stest.New(t, ctx),
	}
}

// From New creates a new KaroResources object with embedded k8stest.Resources
func From(resources *k8stest.Resources) *KaroResources {
	return &KaroResources{
		resources: resources,
	}
}

// GetResources returns the underlying k8stest.Resources for chaining
func (kr *KaroResources) GetResources() *k8stest.Resources {
	return kr.resources
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
	schema := kr.resources.TestClients.K8sClient.Scheme()
	err := karov1alpha1.AddToScheme(schema)
	if err != nil {
		return nil, err
	}

	_, err = kr.Delete()
	if err != nil {
		return nil, err
	}

	// Create base k8stest resources
	kr.resources, err = kr.resources.Create()
	if err != nil {
		return nil, err
	}

	// Create RestartRules
	for _, restartRule := range kr.restartRules {
		if err := kr.resources.TestClients.K8sClient.Create(kr.resources.Ctx, restartRule); err != nil {
			return nil, fmt.Errorf("failed to create restart rule: %w", err)
		}
	}

	return kr, nil
}

// Wait waits for all resources to be ready, including RestartRules
func (kr *KaroResources) Wait(timeout ...time.Duration) error {
	applicableTimeout := kr.resources.Timeout
	if len(timeout) > 0 {
		applicableTimeout = timeout[0]
	}

	err := kr.resources.Wait(applicableTimeout)
	if err != nil {
		return err
	}

	// Wait for RestartRules to be ready (in Active phase with Ready condition)
	for _, restartRule := range kr.restartRules {
		err := wait.PollUntilContextTimeout(kr.resources.Ctx, 1*time.Second, applicableTimeout, true,
			func(ctx context.Context) (bool, error) {
				getCtx, cancel := context.WithTimeout(ctx, kr.resources.Timeout)
				defer cancel()

				rule := &karov1alpha1.RestartRule{}
				if err := kr.resources.TestClients.K8sClient.Get(getCtx,
					client.ObjectKey{Namespace: restartRule.Namespace, Name: restartRule.Name}, rule); err != nil {
					return false, err
				}

				return rule.Status.Phase == "Active", nil
			})
		if err != nil {
			return fmt.Errorf("failed to wait for restart rule %s: %w", restartRule.Name, err)
		}
	}

	return nil
}

// Delete deletes all resources, including RestartRules
func (kr *KaroResources) Delete() (*KaroResources, error) {
	// Delete RestartRules first
	for _, restartRule := range kr.restartRules {
		err := kr.resources.TestClients.K8sClient.Delete(kr.resources.Ctx, restartRule)
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to delete restart rule: %w", err)
		}
	}

	// Delete base k8stest resources (deployments, configmaps, secrets)
	var err = kr.resources.Delete()
	if err != nil {
		return nil, err
	}

	return kr, nil
}
