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

package e2e

import (
	"context"
	"strings"
	"testing"
)

// TestControllerLogAccess validates that the controller logs can be accessed during e2e tests
func TestControllerLogAccess(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	setupMinimalEnvironment(ctx, t, clients)
	defer cleanupMinimalEnvironment(ctx, t, clients)

	testLogCapture(t, clients)
	testRecentLogs(t, clients)

	t.Log("SUCCESS: Controller log access functionality is working correctly")
}

func setupMinimalEnvironment(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Creating test namespace...")
	if err := createNamespace(ctx, clients.clientset, testNamespace); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	t.Log("Starting controller manager...")
	if err := clients.controllerManager.Start(ctx); err != nil {
		t.Fatalf("Failed to start controller manager: %v", err)
	}
}

func cleanupMinimalEnvironment(ctx context.Context, t *testing.T, clients *testClients) {
	if err := clients.controllerManager.Stop(); err != nil {
		t.Logf("Error stopping controller manager: %v", err)
	}
	cleanupNamespace(ctx, t, clients.clientset)
}

func testLogCapture(t *testing.T, clients *testClients) {
	t.Log("Testing log access...")
	logs := clients.controllerManager.GetLogs()
	if len(logs) == 0 {
		t.Fatal("Expected to capture some logs, but got none")
	}

	t.Logf("Captured %d log lines", len(logs))
	verifyStartupLogs(t, logs)
}

func verifyStartupLogs(t *testing.T, logs []string) {
	foundStartupLog := false
	for _, logLine := range logs {
		if strings.Contains(logLine, "Starting EventSource") ||
			strings.Contains(logLine, "Starting Controller") ||
			strings.Contains(logLine, "Starting workers") {
			foundStartupLog = true

			break
		}
	}

	if !foundStartupLog {
		t.Error("Expected to find controller startup log, but didn't find it")
		logAllLines(t, logs)
	}
}

func logAllLines(t *testing.T, logs []string) {
	t.Log("Available logs:")
	for i, logLine := range logs {
		t.Logf("  [%d]: %s", i+1, logLine)
	}
}

func testRecentLogs(t *testing.T, clients *testClients) {
	recentLogs := clients.controllerManager.GetRecentLogs(5)
	if len(recentLogs) == 0 {
		t.Error("Expected to get some recent logs, but got none")
	}

	if len(recentLogs) > 5 {
		t.Errorf("Expected at most 5 recent logs, but got %d", len(recentLogs))
	}
}
