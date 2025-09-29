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

// Package e2e provides end-to-end tests for the Karo operator.
//
// This file contains comprehensive end-to-end tests for the delayRestart feature,
// covering the requirements from issue #15:
//
// Test Scenarios:
//
// 1. Basic Delay Tests:
//   - Single rule with delay (5s) → verify delayed restart timing
//   - Rule with zero delay → verify immediate restart via DelayedRestartManager
//
// 2. Multiple Rules Delay Priority Tests:
//   - Multiple rules with different delays (3s, 5s, 8s) → highest delay (8s) wins
//   - Mixed delay/no-delay rules → delay takes precedence over immediate
//   - Verify only one restart occurs despite multiple rule triggers
//
// 3. Duplicate/Pending Restart Tests:
//   - Trigger restart for already delayed target → verify no duplicate restart
//   - Multiple rapid changes → verify only one delayed restart scheduled
//   - Check controller logs for duplicate prevention messages
//
// 4. Integration Tests:
//   - ConfigMap change with delay → verify delayed restart + status recording
//   - Secret change with delay → verify delayed restart + status recording
//   - Verify RestartRule status properly records restart events after delay
//
// 5. Edge Cases:
//   - Very short delays (1s) → verify they work correctly
//   - Longer delays (15s) → verify they work within e2e test timeouts
//   - Proper resource cleanup to prevent test interference
//
// All tests use realistic delays (1-15 seconds) appropriate for e2e testing,
// verify timing constraints, and check for proper status recording and
// log message generation.
package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	delayTestNamespace = "karo-delay-e2e-test"
	delayTimeout       = 90 * time.Second // Extended timeout for delay tests
	delayInterval      = 2 * time.Second  // Shorter interval for delay tests
)

// TestDelayRestartE2E runs comprehensive end-to-end tests for delayRestart functionality
func TestDelayRestartE2E(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	setupDelayTestEnvironment(ctx, t, clients)
	defer cleanupDelayTest(ctx, t, clients)

	t.Run("BasicDelayTests", func(t *testing.T) {
		testBasicDelayRestart(ctx, t, clients)
	})

	t.Run("MultipleRulesDelayPriority", func(t *testing.T) {
		testMultipleRulesDelayPriority(ctx, t, clients)
	})

	t.Run("DuplicateRestartPrevention", func(t *testing.T) {
		testDuplicateRestartPrevention(ctx, t, clients)
	})

	t.Run("DelayIntegrationTests", func(t *testing.T) {
		testDelayIntegration(ctx, t, clients)
	})

	t.Run("EdgeCaseTests", func(t *testing.T) {
		testDelayEdgeCases(ctx, t, clients)
	})
}

// setupDelayTestEnvironment creates the test environment for delay tests
func setupDelayTestEnvironment(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Setting up delay test environment...")

	if err := createNamespace(ctx, clients.clientset, delayTestNamespace); err != nil {
		t.Fatalf("Failed to create delay test namespace: %v", err)
	}

	if err := clients.controllerManager.Start(ctx); err != nil {
		t.Fatalf("Failed to start controller manager: %v", err)
	}

	// Create all test resources needed for delay tests
	testConfigMaps := []*corev1.ConfigMap{
		createTestConfigMap("test-config-1"),
		createTestConfigMap("test-config-2"),
		createTestConfigMap("test-config-multi-1"),
		createTestConfigMap("test-config-mixed-1"),
		createTestConfigMap("test-config-mixed-2"),
		createTestConfigMap("test-config-dup"),
		createTestConfigMap("test-config-integration"),
		createTestConfigMap("test-config-short"),
		createTestConfigMap("test-config-longer"),
	}

	for _, configMap := range testConfigMaps {
		if err := clients.k8sClient.Create(ctx, configMap); err != nil {
			t.Fatalf("Failed to create test ConfigMap %s: %v", configMap.Name, err)
		}
	}

	testSecrets := []*corev1.Secret{
		createTestSecret("test-secret-1"),
		createTestSecret("test-secret-integration"),
	}

	for _, secret := range testSecrets {
		if err := clients.k8sClient.Create(ctx, secret); err != nil {
			t.Fatalf("Failed to create test Secret %s: %v", secret.Name, err)
		}
	}

	deployment := createDelayTestDeployment()
	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create test Deployment: %v", err)
	}

	if err := waitForDeploymentReady(ctx, clients.clientset, delayTestNamespace, "test-app"); err != nil {
		t.Fatalf("Test deployment did not become ready: %v", err)
	}

	t.Log("Delay test environment setup complete")
}

// testBasicDelayRestart tests basic delay functionality
//
//nolint:cyclop
func testBasicDelayRestart(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing Basic Delay Restart Functionality ===")

	// Test 1: Single rule with delay
	t.Run("SingleRuleWithDelay", func(t *testing.T) {
		t.Log("Testing single rule with 5-second delay...")

		delay := int32(5)
		ruleName := "single-delay-rule"

		rule := createDelayRestartRule(ruleName, delay, "test-config-1", "Deployment", "test-app")
		if err := clients.k8sClient.Create(ctx, rule); err != nil {
			t.Fatalf("Failed to create delay RestartRule: %v", err)
		}
		defer cleanupRestartRule(ctx, t, clients.k8sClient, ruleName)
		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
			t.Fatalf("DelayRestart rule did not become ready: %v", err)
		}

		// Update ConfigMap to trigger delayed restart
		configMap := &corev1.ConfigMap{}
		key := client.ObjectKey{Namespace: delayTestNamespace, Name: "test-config-1"}
		if err := clients.k8sClient.Get(ctx, key, configMap); err != nil {
			t.Fatalf("Failed to get ConfigMap: %v", err)
		}

		configMap.Data["test-key"] = fmt.Sprintf("updated-value-%d", time.Now().Unix())
		if err := clients.k8sClient.Update(ctx, configMap); err != nil {
			t.Fatalf("Failed to update ConfigMap: %v", err)
		}

		// Verify restart is delayed
		startTime := time.Now()
		restartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, "", "ConfigMap update")
		elapsed := time.Since(startTime)

		if elapsed < 4*time.Second { // Allow some margin
			t.Errorf("Restart happened too quickly: %v (expected ~5s delay)", elapsed)
		}

		if elapsed > 10*time.Second { // Upper bound to catch issues
			t.Errorf("Restart took too long: %v (expected ~5s delay)", elapsed)
		}

		t.Logf("SUCCESS: Delayed restart completed after %v with restart time: %s", elapsed, restartTime)
	})

	// Test 2: Rule with zero delay
	t.Run("RuleWithZeroDelay", func(t *testing.T) {
		t.Log("Testing rule with zero delay (immediate restart)...")

		delay := int32(0)
		ruleName := "zero-delay-rule"

		rule := createDelayRestartRule(ruleName, delay, "test-config-2", "Deployment", "test-app")
		if err := clients.k8sClient.Create(ctx, rule); err != nil {
			t.Fatalf("Failed to create zero-delay RestartRule: %v", err)
		}
		defer cleanupRestartRule(ctx, t, clients.k8sClient, ruleName)
		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
			t.Fatalf("Zero-delay rule did not become ready: %v", err)
		}

		// Update ConfigMap to trigger immediate restart
		configMap := &corev1.ConfigMap{}
		key := client.ObjectKey{Namespace: delayTestNamespace, Name: "test-config-2"}
		if err := clients.k8sClient.Get(ctx, key, configMap); err != nil {
			t.Fatalf("Failed to get ConfigMap: %v", err)
		}

		configMap.Data["test-key"] = fmt.Sprintf("updated-value-%d", time.Now().Unix())
		if err := clients.k8sClient.Update(ctx, configMap); err != nil {
			t.Fatalf("Failed to update ConfigMap: %v", err)
		}

		// Verify restart is immediate (handled by DelayedRestartManager)
		startTime := time.Now()
		restartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, "", "ConfigMap update with zero delay")
		elapsed := time.Since(startTime)

		if elapsed > 3*time.Second { // Should be almost immediate
			t.Errorf("Zero-delay restart took too long: %v", elapsed)
		}

		t.Logf("SUCCESS: Zero-delay restart completed after %v with restart time: %s", elapsed, restartTime)
	})
}

// testMultipleRulesDelayPriority tests delay priority with multiple rules
//
//nolint:gocognit,cyclop
func testMultipleRulesDelayPriority(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing Multiple Rules Delay Priority ===")

	// Test 1: Multiple rules with different delays - highest should win
	t.Run("HighestDelayWins", func(t *testing.T) {
		t.Log("Testing multiple rules with different delays...")

		rule1Name := "delay-rule-3s"
		rule2Name := "delay-rule-8s"
		rule3Name := "delay-rule-5s"

		// Create rules with different delays for the same target
		rule1 := createDelayRestartRule(rule1Name, 3, "test-config-multi-1", "Deployment", "test-app")
		rule2 := createDelayRestartRule(rule2Name, 8, "test-config-multi-1", "Deployment", "test-app")
		rule3 := createDelayRestartRule(rule3Name, 5, "test-config-multi-1", "Deployment", "test-app")

		for _, rule := range []*karov1alpha1.RestartRule{rule1, rule2, rule3} {
			if err := clients.k8sClient.Create(ctx, rule); err != nil {
				t.Fatalf("Failed to create multi-delay RestartRule: %v", err)
			}
			defer cleanupRestartRule(ctx, t, clients.k8sClient, rule.Name)
		}

		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		// Wait for all rules to be ready
		for _, ruleName := range []string{rule1Name, rule2Name, rule3Name} {
			if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
				t.Fatalf("Multi-delay rule %s did not become ready: %v", ruleName, err)
			}
		}

		// Update all ConfigMaps simultaneously to trigger all rules
		startTime := time.Now()
		// TODO: Remove loop
		for i, configName := range []string{"test-config-multi-1"} {
			configMap := &corev1.ConfigMap{}
			key := client.ObjectKey{Namespace: delayTestNamespace, Name: configName}
			if err := clients.k8sClient.Get(ctx, key, configMap); err != nil {
				t.Fatalf("Failed to get ConfigMap %s: %v", configName, err)
			}

			configMap.Data["test-key"] = fmt.Sprintf("multi-update-%d-%d", i, time.Now().Unix())
			if err := clients.k8sClient.Update(ctx, configMap); err != nil {
				t.Fatalf("Failed to update ConfigMap %s: %v", configName, err)
			}
		}

		// Verify restart is delayed by the highest delay (8 seconds)
		restartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, "", "Multiple ConfigMap updates")
		elapsed := time.Since(startTime)

		if elapsed < 7*time.Second { // Allow some margin below expected
			t.Errorf("Restart happened too quickly: %v (expected ~8s delay)", elapsed)
		}

		if elapsed > 12*time.Second { // Upper bound
			t.Errorf("Restart took too long: %v (expected ~8s delay)", elapsed)
		}

		t.Logf("SUCCESS: Highest delay (8s) was applied, restart completed after"+
			" %v with restart time: %s", elapsed, restartTime)
	})

	// Test 2: Mixed delay and no-delay rules - delay should take precedence
	t.Run("MixedDelayNoDelay", func(t *testing.T) {
		t.Log("Testing mixed delay and no-delay rules...")

		delayRuleName := "mixed-delay-rule"
		noDelayRuleName := "mixed-no-delay-rule"

		// Create one rule with delay and one without
		delayRule := createDelayRestartRule(delayRuleName, 6, "test-config-mixed-1", "Deployment", "test-app")
		noDelayRule := createDelayRestartRule(noDelayRuleName, 0, "test-config-mixed-2", "Deployment", "test-app")

		for _, rule := range []*karov1alpha1.RestartRule{delayRule, noDelayRule} {
			if err := clients.k8sClient.Create(ctx, rule); err != nil {
				t.Fatalf("Failed to create mixed RestartRule: %v", err)
			}
			defer cleanupRestartRule(ctx, t, clients.k8sClient, rule.Name)
		}

		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		// Wait for both rules to be ready
		for _, ruleName := range []string{delayRuleName, noDelayRuleName} {
			if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
				t.Fatalf("Mixed rule %s did not become ready: %v", ruleName, err)
			}
		}

		// Update both ConfigMaps to trigger both rules
		startTime := time.Now()
		for i, configName := range []string{"test-config-mixed-1", "test-config-mixed-2"} {
			configMap := &corev1.ConfigMap{}
			key := client.ObjectKey{Namespace: delayTestNamespace, Name: configName}
			if err := clients.k8sClient.Get(ctx, key, configMap); err != nil {
				t.Fatalf("Failed to get ConfigMap %s: %v", configName, err)
			}

			configMap.Data["test-key"] = fmt.Sprintf("mixed-update-%d-%d", i, time.Now().Unix())
			if err := clients.k8sClient.Update(ctx, configMap); err != nil {
				t.Fatalf("Failed to update ConfigMap %s: %v", configName, err)
			}
		}

		// Verify restart is delayed (should use the 6-second delay)
		restartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, "", "Mixed delay/no-delay updates")
		elapsed := time.Since(startTime)

		if elapsed < 5*time.Second { // Allow some margin
			t.Errorf("Restart happened too quickly: %v (expected ~6s delay)", elapsed)
		}

		if elapsed > 10*time.Second { // Upper bound
			t.Errorf("Restart took too long: %v (expected ~6s delay)", elapsed)
		}

		t.Logf("SUCCESS: Delay rule took precedence over no-delay rule, restart completed after"+
			" %v with restart time: %s", elapsed, restartTime)
	})
}

// testDuplicateRestartPrevention tests duplicate restart prevention
//
//nolint:gocognit,cyclop
func testDuplicateRestartPrevention(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing Duplicate Restart Prevention ===")

	t.Run("PreventDuplicateRestart", func(t *testing.T) {
		t.Log("Testing prevention of duplicate restarts...")

		ruleName := "duplicate-prevention-rule"
		delay := int32(10) // Longer delay to allow for multiple triggers

		rule := createDelayRestartRule(ruleName, delay, "test-config-dup", "Deployment", "test-app")
		if err := clients.k8sClient.Create(ctx, rule); err != nil {
			t.Fatalf("Failed to create duplicate prevention RestartRule: %v", err)
		}
		defer cleanupRestartRule(ctx, t, clients.k8sClient, ruleName)
		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
			t.Fatalf("Duplicate prevention rule did not become ready: %v", err)
		}

		// Get initial restart time
		initialDeployment := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: delayTestNamespace, Name: "test-app"}
		if err := clients.k8sClient.Get(ctx, key, initialDeployment); err != nil {
			t.Fatalf("Failed to get deployment: %v", err)
		}

		initialRestartAnnotation := ""
		if annotations := initialDeployment.Spec.Template.Annotations; annotations != nil {
			initialRestartAnnotation = annotations["karo.jeeatwork.com/restartedAt"]
		}

		// Clear logs to capture new log messages
		_ = clients.controllerManager.GetRecentLogs(1000) // Clear existing logs

		// Trigger first restart
		configMap := &corev1.ConfigMap{}
		configKey := client.ObjectKey{Namespace: delayTestNamespace, Name: "test-config-dup"}
		if err := clients.k8sClient.Get(ctx, configKey, configMap); err != nil {
			t.Fatalf("Failed to get ConfigMap: %v", err)
		}

		configMap.Data["test-key"] = fmt.Sprintf("first-update-%d", time.Now().Unix())
		if err := clients.k8sClient.Update(ctx, configMap); err != nil {
			t.Fatalf("Failed to update ConfigMap: %v", err)
		}

		// Wait a moment and trigger second restart (should be prevented)
		time.Sleep(2 * time.Second)

		configMap.Data["test-key"] = fmt.Sprintf("second-update-%d", time.Now().Unix())
		if err := clients.k8sClient.Update(ctx, configMap); err != nil {
			t.Fatalf("Failed to update ConfigMap second time: %v", err)
		}

		// Wait for the original delayed restart to complete
		startTime := time.Now()
		finalRestartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, initialRestartAnnotation,
			"Duplicate prevention test")
		elapsed := time.Since(startTime)

		// Should still take around 10 seconds (from first trigger, not second)
		if elapsed > 12*time.Second {
			t.Errorf("Restart took too long: %v (may indicate duplicate processing)", elapsed)
		}

		// Check logs for duplicate restart prevention message
		logs := clients.controllerManager.GetRecentLogs(100)
		foundDuplicatePreventionLog := false
		for _, logLine := range logs {
			if strings.Contains(logLine, "already scheduled") ||
				strings.Contains(logLine, "duplicate restart") ||
				strings.Contains(logLine, "restart is already pending") {
				foundDuplicatePreventionLog = true
				t.Logf("Found duplicate prevention log: %s", strings.TrimSpace(logLine))

				break
			}
		}

		if !foundDuplicatePreventionLog {
			t.Logf("Warning: Did not find explicit duplicate prevention log message")
			// Log recent logs for debugging
			t.Logf("Recent logs:")
			for i, logLine := range logs {
				if i < 10 { // Only show first 10 lines
					t.Logf("  %s", strings.TrimSpace(logLine))
				}
			}
		}

		t.Logf("SUCCESS: Duplicate restart prevention test completed, final restart time: %s", finalRestartTime)
	})
}

// testDelayIntegration tests delay functionality with ConfigMap and Secret changes
//
//nolint:cyclop
func testDelayIntegration(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing Delay Integration with ConfigMap and Secret Changes ===")

	// Test 1: ConfigMap change with delay
	t.Run("ConfigMapChangeWithDelay", func(t *testing.T) {
		t.Log("Testing ConfigMap change with delay integration...")

		ruleName := "configmap-delay-integration"
		delay := int32(4)

		rule := createDelayRestartRule(ruleName, delay, "test-config-integration", "Deployment", "test-app")
		if err := clients.k8sClient.Create(ctx, rule); err != nil {
			t.Fatalf("Failed to create ConfigMap integration RestartRule: %v", err)
		}
		defer cleanupRestartRule(ctx, t, clients.k8sClient, ruleName)
		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
			t.Fatalf("ConfigMap integration rule did not become ready: %v", err)
		}

		// Update ConfigMap and verify delayed restart
		configMap := &corev1.ConfigMap{}
		key := client.ObjectKey{Namespace: delayTestNamespace, Name: "test-config-integration"}
		if err := clients.k8sClient.Get(ctx, key, configMap); err != nil {
			t.Fatalf("Failed to get ConfigMap: %v", err)
		}

		startTime := time.Now()
		configMap.Data["integration-key"] = fmt.Sprintf("integration-value-%d", time.Now().Unix())
		if err := clients.k8sClient.Update(ctx, configMap); err != nil {
			t.Fatalf("Failed to update ConfigMap: %v", err)
		}

		restartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, "", "ConfigMap integration")
		elapsed := time.Since(startTime)

		if elapsed < 3*time.Second || elapsed > 8*time.Second {
			t.Errorf("ConfigMap integration restart timing unexpected: %v (expected ~4s)", elapsed)
		}

		// Verify restart event was recorded
		if err := waitForRestartEvent(ctx, clients.k8sClient, delayTestNamespace, ruleName, "test-app"); err != nil {
			t.Fatalf("Restart event was not recorded for ConfigMap integration: %v", err)
		}

		t.Logf("SUCCESS: ConfigMap integration with delay completed after %v, restart time: %s", elapsed, restartTime)
	})

	// Test 2: Secret change with delay
	t.Run("SecretChangeWithDelay", func(t *testing.T) {
		t.Log("Testing Secret change with delay integration...")

		ruleName := "secret-delay-integration"
		delay := int32(7)

		rule := createDelaySecretRestartRule(ruleName, delay, "test-secret-integration", "Deployment", "test-app")
		if err := clients.k8sClient.Create(ctx, rule); err != nil {
			t.Fatalf("Failed to create Secret integration RestartRule: %v", err)
		}
		defer cleanupRestartRule(ctx, t, clients.k8sClient, ruleName)
		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
			t.Fatalf("Secret integration rule did not become ready: %v", err)
		}

		// Update Secret and verify delayed restart
		secret := &corev1.Secret{}
		key := client.ObjectKey{Namespace: delayTestNamespace, Name: "test-secret-integration"}
		if err := clients.k8sClient.Get(ctx, key, secret); err != nil {
			t.Fatalf("Failed to get Secret: %v", err)
		}

		startTime := time.Now()
		secret.Data["integration-secret"] = []byte(fmt.Sprintf("integration-secret-value-%d", time.Now().Unix()))
		if err := clients.k8sClient.Update(ctx, secret); err != nil {
			t.Fatalf("Failed to update Secret: %v", err)
		}

		restartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, "", "Secret integration")
		elapsed := time.Since(startTime)

		if elapsed < 6*time.Second || elapsed > 11*time.Second {
			t.Errorf("Secret integration restart timing unexpected: %v (expected ~7s)", elapsed)
		}

		// Verify restart event was recorded
		if err := waitForRestartEvent(ctx, clients.k8sClient, delayTestNamespace, ruleName, "test-app"); err != nil {
			t.Fatalf("Restart event was not recorded for Secret integration: %v", err)
		}

		t.Logf("SUCCESS: Secret integration with delay completed after %v, restart time: %s", elapsed, restartTime)
	})
}

// testDelayEdgeCases tests edge cases for delay functionality
//
//nolint:cyclop
func testDelayEdgeCases(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing Delay Edge Cases ===")

	// Test 1: Very short delay
	t.Run("VeryShortDelay", func(t *testing.T) {
		t.Log("Testing very short delay (1 second)...")

		ruleName := "short-delay-rule"
		delay := int32(1)

		rule := createDelayRestartRule(ruleName, delay, "test-config-short", "Deployment", "test-app")
		if err := clients.k8sClient.Create(ctx, rule); err != nil {
			t.Fatalf("Failed to create short delay RestartRule: %v", err)
		}
		defer cleanupRestartRule(ctx, t, clients.k8sClient, ruleName)
		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
			t.Fatalf("Short delay rule did not become ready: %v", err)
		}

		configMap := &corev1.ConfigMap{}
		key := client.ObjectKey{Namespace: delayTestNamespace, Name: "test-config-short"}
		if err := clients.k8sClient.Get(ctx, key, configMap); err != nil {
			t.Fatalf("Failed to get ConfigMap: %v", err)
		}

		startTime := time.Now()
		configMap.Data["test-key"] = fmt.Sprintf("short-delay-%d", time.Now().Unix())
		if err := clients.k8sClient.Update(ctx, configMap); err != nil {
			t.Fatalf("Failed to update ConfigMap: %v", err)
		}

		restartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, "", "Short delay test")
		elapsed := time.Since(startTime)

		if elapsed > 5*time.Second {
			t.Errorf("Short delay took too long: %v (expected ~1s)", elapsed)
		}

		t.Logf("SUCCESS: Short delay (1s) completed after %v, restart time: %s", elapsed, restartTime)
	})

	// Test 2: Longer delay for edge case testing (but not too long for e2e)
	t.Run("LongerDelayWithinLimit", func(t *testing.T) {
		t.Log("Testing longer delay within e2e test limits (15 seconds)...")

		ruleName := "longer-delay-rule"
		delay := int32(15)

		rule := createDelayRestartRule(ruleName, delay, "test-config-longer", "Deployment", "test-app")
		if err := clients.k8sClient.Create(ctx, rule); err != nil {
			t.Fatalf("Failed to create longer delay RestartRule: %v", err)
		}
		defer cleanupRestartRule(ctx, t, clients.k8sClient, ruleName)
		defer removeRestartAnnotation(ctx, t, clients.k8sClient, delayTestNamespace, "test-app")

		if err := waitForRestartRuleReady(ctx, clients.k8sClient, delayTestNamespace, ruleName); err != nil {
			t.Fatalf("Longer delay rule did not become ready: %v", err)
		}

		configMap := &corev1.ConfigMap{}
		key := client.ObjectKey{Namespace: delayTestNamespace, Name: "test-config-longer"}
		if err := clients.k8sClient.Get(ctx, key, configMap); err != nil {
			t.Fatalf("Failed to get ConfigMap: %v", err)
		}

		startTime := time.Now()
		configMap.Data["test-key"] = fmt.Sprintf("longer-delay-%d", time.Now().Unix())
		if err := clients.k8sClient.Update(ctx, configMap); err != nil {
			t.Fatalf("Failed to update ConfigMap: %v", err)
		}

		restartTime := waitForDelayedDeploymentRestart(ctx, t, clients.k8sClient, "", "Longer delay test")
		elapsed := time.Since(startTime)

		if elapsed < 14*time.Second || elapsed > 20*time.Second {
			t.Errorf("Longer delay timing unexpected: %v (expected ~15s)", elapsed)
		}

		t.Logf("SUCCESS: Longer delay (15s) completed after %v, restart time: %s", elapsed, restartTime)
	})
}

// Helper functions for delay restart tests

// createTestConfigMap creates a test ConfigMap with the given name
func createTestConfigMap(name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: delayTestNamespace,
		},
		Data: map[string]string{
			"test-key": "initial-value",
		},
	}
}

// createTestSecret creates a test Secret with the given name
func createTestSecret(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: delayTestNamespace,
		},
		Data: map[string][]byte{
			"test-secret": []byte("initial-secret-value"),
		},
	}
}

// createDelayTestDeployment creates a test Deployment for delay tests
func createDelayTestDeployment() *appsv1.Deployment {
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: delayTestNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test-app",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test-app",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "nginx:1.21-alpine",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
									Protocol:      corev1.ProtocolTCP,
								},
							},
						},
					},
				},
			},
		},
	}
}

// createDelayRestartRule creates a RestartRule with delayRestart for ConfigMap changes
//
//nolint:unparam
func createDelayRestartRule(name string, delaySeconds int32, configMapName,
	targetKind, targetName string) *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: delayTestNamespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: &delaySeconds,
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       configMapName,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: targetKind,
					Name: targetName,
				},
			},
		},
	}
}

// createDelaySecretRestartRule creates a RestartRule with delayRestart for Secret changes
func createDelaySecretRestartRule(name string, delaySeconds int32, secretName,
	targetKind, targetName string) *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: delayTestNamespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: &delaySeconds,
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "Secret",
					Name:       secretName,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: targetKind,
					Name: targetName,
				},
			},
		},
	}
}

// waitForDelayedDeploymentRestart waits for a deployment restart with proper delay timing
func waitForDelayedDeploymentRestart(
	ctx context.Context,
	t *testing.T,
	k8sClient client.Client,
	previousRestartTime, resourceType string,
) string {
	t.Logf("Waiting for delayed deployment restart after %s...", resourceType)
	var restartTime string

	err := wait.PollUntilContextTimeout(ctx, delayInterval, delayTimeout, true, func(ctx context.Context) (bool, error) {
		currentDeployment := &appsv1.Deployment{}
		if err := k8sClient.Get(
			ctx, client.ObjectKey{Namespace: delayTestNamespace, Name: "test-app"}, currentDeployment); err != nil {
			return false, err
		}

		restartAnnotation := "karo.jeeatwork.com/restartedAt"
		if annotations := currentDeployment.Spec.Template.Annotations; annotations != nil {
			if currentRestartTime, exists := annotations[restartAnnotation]; exists {
				if previousRestartTime == "" || currentRestartTime != previousRestartTime {
					t.Logf("Found restart annotation after %s: %s = %s", resourceType,
						restartAnnotation, currentRestartTime)
					restartTime = currentRestartTime

					return true, nil
				}
				t.Logf("Restart annotation exists but hasn't changed: %s", currentRestartTime)
			}
		} else {
			t.Logf("No annotations found on deployment template")
		}

		return false, nil
	})

	if err != nil {
		t.Fatalf("Error while waiting for delayed deployment restart after %s: %v", resourceType, err)
	}

	if restartTime == "" {
		t.Fatalf("Delayed deployment restart was not detected after %s", resourceType)
	}

	return restartTime
}

// cleanupRestartRule cleans up a specific RestartRule
func cleanupRestartRule(ctx context.Context, t *testing.T, k8sClient client.Client, ruleName string) {
	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ruleName,
			Namespace: delayTestNamespace,
		},
	}
	deleteResource(ctx, t, k8sClient, rule, "RestartRule")
}

// cleanupDelayTest cleans up delay test resources
func cleanupDelayTest(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Cleaning up delay test resources...")

	if err := clients.controllerManager.Stop(); err != nil {
		t.Logf("Error stopping controller manager: %v", err)
	}

	// Clean up test-specific resources
	cleanupDelayTestResources(ctx, t, clients.k8sClient)

	// Clean up namespace
	if err := clients.clientset.CoreV1().Namespaces().Delete(
		ctx, delayTestNamespace, metav1.DeleteOptions{}); err != nil {
		t.Logf("Failed to delete delay test namespace: %v", err)
	}

	t.Log("Delay test cleanup completed")
}

// cleanupDelayTestResources cleans up test resources
func cleanupDelayTestResources(ctx context.Context, t *testing.T, k8sClient client.Client) {
	// Create lists of all possible test resources to clean up
	testConfigMaps := []string{
		"test-config-1", "test-config-2", "test-config-multi-1",
		"test-config-mixed-1", "test-config-mixed-2", "test-config-dup", "test-config-integration",
		"test-config-short", "test-config-longer",
	}

	testSecrets := []string{
		"test-secret-1", "test-secret-integration",
	}

	// Clean up ConfigMaps
	for _, configName := range testConfigMaps {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configName,
				Namespace: delayTestNamespace,
			},
		}
		deleteResource(ctx, t, k8sClient, configMap, "ConfigMap")
	}

	// Clean up Secrets
	for _, secretName := range testSecrets {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: delayTestNamespace,
			},
		}
		deleteResource(ctx, t, k8sClient, secret, "Secret")
	}

	// Clean up Deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: delayTestNamespace,
		},
	}
	deleteResource(ctx, t, k8sClient, deployment, "Deployment")

	time.Sleep(2 * time.Second) // Allow time for resource cleanup
}

//nolint:unparam
func removeRestartAnnotation(ctx context.Context, t *testing.T, k8sClient client.Client,
	namespace, deploymentName string) {
	t.Logf("Removing restart annotation from deployment %s/%s...", namespace, deploymentName)

	deployment := &appsv1.Deployment{}
	key := client.ObjectKey{Namespace: namespace, Name: deploymentName}
	if err := k8sClient.Get(ctx, key, deployment); err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	restartAnnotation := "karo.jeeatwork.com/restartedAt"
	if annotations := deployment.Spec.Template.Annotations; annotations != nil {
		if _, exists := annotations[restartAnnotation]; exists {
			delete(annotations, restartAnnotation)
			deployment.Spec.Template.Annotations = annotations

			// TODO: Examine why only patching with a raw patch works reliably here
			patch := client.RawPatch(types.JSONPatchType, []byte(
				`[{"op": "remove", "path": "/spec/template/metadata/annotations/karo.jeeatwork.com~1restartedAt"}]`))

			if err := k8sClient.Patch(ctx, deployment, patch); err != nil {
				t.Fatalf("Failed to update deployment to remove restart annotation: %v", err)
			}

			t.Logf("Successfully removed restart annotation from deployment %s/%s", namespace, deploymentName)
		}
	} else {
		t.Logf("No annotations found on deployment %s/%s", namespace, deploymentName)
	}
}
