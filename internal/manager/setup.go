/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package manager

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/controller"
	"karo.jeeatwork.com/internal/store"
)

// SetupOptions contains options for setting up the manager
type SetupOptions struct {
	zap.Options

	MetricsAddr        string
	Scheme             *runtime.Scheme
	SkipNameValidation bool
	MinimumDelay       int32
}

// DefaultSetupOptions returns default setup options
func DefaultSetupOptions() *SetupOptions {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(karov1alpha1.AddToScheme(scheme))

	return &SetupOptions{
		MetricsAddr:        "0", // disable metrics by default
		Scheme:             scheme,
		SkipNameValidation: false,
		MinimumDelay:       0,
	}
}

// SetupManager creates and configures a controller-runtime manager with all controllers
//
//nolint:ireturn // Manager interface is intended to be returned as interface
func SetupManager(opts *SetupOptions) (ctrl.Manager, error) {
	if opts == nil {
		opts = DefaultSetupOptions()
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: opts.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: opts.MetricsAddr,
		},
		Controller: config.Controller{
			SkipNameValidation: &opts.SkipNameValidation,
		},
	})
	if err != nil {
		return nil, err
	}

	// Create shared store
	restartRuleStore := store.NewMemoryRestartRuleStore()

	// Create delayed restart manager
	logger := log.Log.WithName("delayed-restart-manager")
	delayedRestartManager := controller.NewDelayedRestartManager(logger)

	// Setup RestartRule controller
	if err := (&controller.RestartRuleReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		RestartRuleStore: restartRuleStore,
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	// Setup ConfigMap controller
	if err := (&controller.ConfigMapReconciler{
		BaseReconciler: controller.BaseReconciler{
			Client:                mgr.GetClient(),
			RestartRuleStore:      restartRuleStore,
			DelayedRestartManager: delayedRestartManager,
			MinimumDelay:          opts.MinimumDelay,
		},
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	// Setup Secret controller
	if err := (&controller.SecretReconciler{
		BaseReconciler: controller.BaseReconciler{
			Client:                mgr.GetClient(),
			RestartRuleStore:      restartRuleStore,
			DelayedRestartManager: delayedRestartManager,
			MinimumDelay:          opts.MinimumDelay,
		},
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return nil, err
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, err
	}

	return mgr, nil
}
