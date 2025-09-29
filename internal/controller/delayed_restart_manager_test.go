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

package controller

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

const (
	// TestDeploymentName is the name used for test deployments
	TestDeploymentName = "test-deployment"
)

var _ = Describe("DelayedRestartManager", func() {
	var (
		manager DelayedRestartManager
		ctx     context.Context
		cancel  context.CancelFunc
	)

	BeforeEach(func() {
		manager = NewDelayedRestartManager()
		newCtx, newCancel := context.WithCancel(context.Background())
		//nolint:fatcontext
		ctx = newCtx
		cancel = newCancel
	})

	AfterEach(func() {
		manager.Stop()
		cancel()
	})

	Describe("ScheduleRestart", func() {
		It("should execute restart immediately when no delay is configured", func() {
			var executed int32
			targetKey := TestDeploymentName

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec:       karov1alpha1.RestartRuleSpec{
						// No DelayRestart field - should execute immediately
					},
				},
			}

			restartFunc := func() error {
				atomic.StoreInt32(&executed, 1)

				return nil
			}

			manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)

			// Should execute immediately
			Eventually(func() int32 {
				return atomic.LoadInt32(&executed)
			}, time.Second, 10*time.Millisecond).Should(Equal(int32(1)))

			Expect(manager.IsRestartPending(targetKey)).To(BeFalse())
		})

		It("should delay restart when DelayRestart is configured", func() {
			var executed int32
			targetKey := TestDeploymentName
			delay := int32(1) // 1 second delay

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay,
					},
				},
			}

			restartFunc := func() error {
				atomic.StoreInt32(&executed, 1)

				return nil
			}

			startTime := time.Now()
			manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)

			// Should be pending initially
			Expect(manager.IsRestartPending(targetKey)).To(BeTrue())

			// Should not execute immediately
			Consistently(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 500*time.Millisecond, 10*time.Millisecond).Should(Equal(int32(0)))

			// Should execute after delay
			Eventually(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 2*time.Second, 10*time.Millisecond).Should(Equal(int32(1)))

			elapsed := time.Since(startTime)
			Expect(elapsed).To(BeNumerically(">=", time.Second))
			Expect(manager.IsRestartPending(targetKey)).To(BeFalse())
		})

		It("should use maximum delay when multiple rules have different delays", func() {
			var executed int32
			targetKey := TestDeploymentName
			delay1 := int32(1)
			delay2 := int32(2)
			delay3 := int32(3) // This should be the maximum

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay2,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule3"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay3,
					},
				},
			}

			restartFunc := func() error {
				atomic.StoreInt32(&executed, 1)

				return nil
			}

			startTime := time.Now()
			manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)

			// Should be pending initially
			Expect(manager.IsRestartPending(targetKey)).To(BeTrue())

			// Should not execute before maximum delay
			Consistently(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 2500*time.Millisecond, 10*time.Millisecond).Should(Equal(int32(0)))

			// Should execute after maximum delay
			Eventually(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 4*time.Second, 10*time.Millisecond).Should(Equal(int32(1)))

			elapsed := time.Since(startTime)
			Expect(elapsed).To(BeNumerically(">=", 3*time.Second))
		})

		It("should use maximum delay even when some rules have no delay", func() {
			var executed int32
			targetKey := TestDeploymentName
			delay2 := int32(2)

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec:       karov1alpha1.RestartRuleSpec{
						// No DelayRestart field
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay2,
					},
				},
			}

			restartFunc := func() error {
				atomic.StoreInt32(&executed, 1)

				return nil
			}

			startTime := time.Now()
			manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)

			// Should be pending initially
			Expect(manager.IsRestartPending(targetKey)).To(BeTrue())

			// Should not execute immediately despite one rule having no delay
			Consistently(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 1500*time.Millisecond, 10*time.Millisecond).Should(Equal(int32(0)))

			// Should execute after the delay from rule2
			Eventually(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 3*time.Second, 10*time.Millisecond).Should(Equal(int32(1)))

			elapsed := time.Since(startTime)
			Expect(elapsed).To(BeNumerically(">=", 2*time.Second))
		})

		It("should skip duplicate restart requests for the same target", func() {
			var executeCount int32
			targetKey := TestDeploymentName
			delay := int32(1)

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay,
					},
				},
			}

			restartFunc := func() error {
				atomic.AddInt32(&executeCount, 1)

				return nil
			}

			// Schedule first restart
			manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)
			Expect(manager.IsRestartPending(targetKey)).To(BeTrue())

			// Schedule second restart for same target - should be ignored
			manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)

			// Wait for execution
			Eventually(func() int32 {
				return atomic.LoadInt32(&executeCount)
			}, 2*time.Second, 10*time.Millisecond).Should(Equal(int32(1)))

			Expect(manager.IsRestartPending(targetKey)).To(BeFalse())
		})

		It("should handle context cancellation", func() {
			var executed int32
			targetKey := TestDeploymentName
			delay := int32(2)

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay,
					},
				},
			}

			restartFunc := func() error {
				atomic.StoreInt32(&executed, 1)

				return nil
			}

			// Create a context that will be cancelled
			cancelCtx, cancelFunc := context.WithCancel(ctx)

			manager.ScheduleRestart(cancelCtx, targetKey, rules, restartFunc)
			Expect(manager.IsRestartPending(targetKey)).To(BeTrue())

			// Cancel the context after a short delay
			go func() {
				time.Sleep(500 * time.Millisecond)
				cancelFunc()
			}()

			// Should not execute due to cancellation
			Consistently(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 3*time.Second, 10*time.Millisecond).Should(Equal(int32(0)))

			// Eventually should not be pending
			Eventually(func() bool {
				return manager.IsRestartPending(targetKey)
			}, time.Second, 10*time.Millisecond).Should(BeFalse())
		})
	})

	Describe("IsRestartPending", func() {
		It("should return false for unknown target", func() {
			Expect(manager.IsRestartPending("unknown-target")).To(BeFalse())
		})

		It("should return true for pending restart", func() {
			targetKey := TestDeploymentName
			delay := int32(5) // Long delay to keep it pending

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay,
					},
				},
			}

			restartFunc := func() error { return nil }

			manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)
			Expect(manager.IsRestartPending(targetKey)).To(BeTrue())
		})
	})

	Describe("Stop", func() {
		It("should cancel all pending restarts", func() {
			var executed1, executed2 int32
			targetKey1 := "test-deployment-1"
			targetKey2 := "test-deployment-2"
			delay := int32(2)

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay,
					},
				},
			}

			restartFunc1 := func() error {
				atomic.StoreInt32(&executed1, 1)

				return nil
			}
			restartFunc2 := func() error {
				atomic.StoreInt32(&executed2, 1)

				return nil
			}

			// Schedule two restarts
			manager.ScheduleRestart(ctx, targetKey1, rules, restartFunc1)
			manager.ScheduleRestart(ctx, targetKey2, rules, restartFunc2)

			Expect(manager.IsRestartPending(targetKey1)).To(BeTrue())
			Expect(manager.IsRestartPending(targetKey2)).To(BeTrue())

			// Stop the manager
			manager.Stop()

			// Should not execute the restarts
			Consistently(func() int32 {
				return atomic.LoadInt32(&executed1) + atomic.LoadInt32(&executed2)
			}, time.Second, 10*time.Millisecond).Should(Equal(int32(0)))

			// Should no longer be pending
			Expect(manager.IsRestartPending(targetKey1)).To(BeFalse())
			Expect(manager.IsRestartPending(targetKey2)).To(BeFalse())
		})

		It("should ignore new restart requests after stop", func() {
			var executed int32
			targetKey := TestDeploymentName

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec:       karov1alpha1.RestartRuleSpec{
						// No delay for immediate execution
					},
				},
			}

			restartFunc := func() error {
				atomic.StoreInt32(&executed, 1)

				return nil
			}

			// Stop the manager first
			manager.Stop()

			// Try to schedule restart after stop
			manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)

			// Should not execute or be pending
			Consistently(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 500*time.Millisecond, 10*time.Millisecond).Should(Equal(int32(0)))

			Expect(manager.IsRestartPending(targetKey)).To(BeFalse())
		})
	})

	Describe("Concurrent operations", func() {
		It("should handle concurrent schedule requests safely", func() {
			var executed int32
			numGoroutines := 10
			targetKey := TestDeploymentName
			delay := int32(1) // Add a small delay to make the race condition more visible

			rules := []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &delay,
					},
				},
			}

			restartFunc := func() error { //nolint:unparam
				atomic.AddInt32(&executed, 1)

				return nil
			}

			var wg sync.WaitGroup
			// Try to schedule the same restart from multiple goroutines
			for range numGoroutines {
				wg.Add(1)
				go func() {
					defer wg.Done()
					manager.ScheduleRestart(ctx, targetKey, rules, restartFunc)
				}()
			}

			wg.Wait()

			// Should have only one pending restart
			Expect(manager.IsRestartPending(targetKey)).To(BeTrue())

			// Wait for execution to complete
			Eventually(func() int32 {
				return atomic.LoadInt32(&executed)
			}, 3*time.Second, 10*time.Millisecond).Should(Equal(int32(1)))

			// Should no longer be pending
			Eventually(func() bool {
				return manager.IsRestartPending(targetKey)
			}, 100*time.Millisecond, 10*time.Millisecond).Should(BeFalse())
		})
	})
})
