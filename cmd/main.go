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

package main

import (
	"flag"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"karo.jeeatwork.com/internal/manager"
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	var minimumDelay int
	flag.IntVar(&minimumDelay, "minimum-delay", 0, "Minimum delay for workload restarts in seconds")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	controllerOpts := manager.DefaultSetupOptions()
	controllerOpts.MinimumDelay = int32(minimumDelay)

	mgr, err := manager.SetupManager(controllerOpts)
	if err != nil {
		setupLog.Error(err, "unable to setup manager")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
