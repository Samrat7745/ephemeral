// Copyright (c) 2021-2025 - for information on the respective copyright owner
// see the NOTICE file and/or the repository https://github.com/carbynestack/ephemeral.
//
// SPDX-License-Identifier: Apache-2.0
//
// The original file created by the Operator SDK has been modified to add Carbyne Stack Ephemeral network controller
// logic.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	goruntime "runtime"

	"github.com/carbynestack/ephemeral/pkg/network-controller/apis"
	"github.com/carbynestack/ephemeral/pkg/network-controller/controller"
	. "github.com/carbynestack/ephemeral/pkg/types"
	"github.com/carbynestack/ephemeral/pkg/utils"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// Change below variables to serve metrics on different host or port.
var (
	metricsHost = "0.0.0.0"
	metricsPort = 8383
)
var log = ctrl.Log.WithName("cmd")

const (
	defaultConfigLocation = "/etc/config/config.json"
	leaderElectionID      = "network-controller-lock"
	watchNamespaceEnvVar  = "WATCH_NAMESPACE"
)

func printVersion() {
	log.Info(fmt.Sprintf("Go Version: %s", goruntime.Version()))
	log.Info(fmt.Sprintf("Go OS/Arch: %s/%s", goruntime.GOOS, goruntime.GOARCH))
}

func main() {
	// Logger flags (--zap-devel, --zap-log-level, ...) are registered on the standard flag set.
	zapOpts := zap.Options{}
	zapOpts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOpts)))

	printVersion()

	networkControllerConfig, err := ParseConfig(defaultConfigLocation)
	if err != nil {
		log.Error(err, "Failed to parse config")
		os.Exit(1)
	}
	log.Info(fmt.Sprintf("Starting with the config:\n%+v", networkControllerConfig))

	namespace, found := os.LookupEnv(watchNamespaceEnvVar)
	if !found {
		log.Error(fmt.Errorf("%s must be set", watchNamespaceEnvVar), "Failed to get watch namespace")
		os.Exit(1)
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}
	// Setup Scheme for all resources
	if err := apis.AddToScheme(scheme); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	opts := ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: fmt.Sprintf("%s:%d", metricsHost, metricsPort)},
		// Become the leader before proceeding (coordination.k8s.io Lease).
		LeaderElection:          true,
		LeaderElectionID:        leaderElectionID,
		LeaderElectionNamespace: namespace,
	}
	if namespace != "" {
		opts.Cache = cache.Options{DefaultNamespaces: map[string]cache.Config{namespace: {}}}
	}

	// Create a new manager to provide shared dependencies and start components
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opts)
	if err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Registering Components.")

	// Setup all Controllers
	if err := controller.AddToManager(mgr, networkControllerConfig); err != nil {
		log.Error(err, "")
		os.Exit(1)
	}

	log.Info("Starting the Cmd.")

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "Manager exited non-zero")
		os.Exit(1)
	}
}

// ParseConfig parses the configuration file of the discovery service.
func ParseConfig(path string) (*NetworkControllerConfig, error) {
	bytes, err := utils.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var conf NetworkControllerConfig
	err = json.Unmarshal(bytes, &conf)
	if err != nil {
		return nil, err
	}
	return &conf, nil
}
