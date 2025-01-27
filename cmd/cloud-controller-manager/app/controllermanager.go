/*
Copyright 2016 The Kubernetes Authors.

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

package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	cloudprovider "k8s.io/cloud-provider"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/cli/globalflag"
	"k8s.io/component-base/configz"
	"k8s.io/component-base/term"
	genericcontrollermanager "k8s.io/controller-manager/app"
	"k8s.io/klog/v2"

	cloudcontrollerconfig "sigs.k8s.io/cloud-provider-azure/cmd/cloud-controller-manager/app/config"
	"sigs.k8s.io/cloud-provider-azure/cmd/cloud-controller-manager/app/dynamic"
	"sigs.k8s.io/cloud-provider-azure/cmd/cloud-controller-manager/app/options"
	"sigs.k8s.io/cloud-provider-azure/pkg/provider"
	"sigs.k8s.io/cloud-provider-azure/pkg/version"
	"sigs.k8s.io/cloud-provider-azure/pkg/version/verflag"
)

const (
	// ControllerStartJitter is the jitter value used when starting controller managers.
	ControllerStartJitter = 1.0
	// ConfigzName is the name used for register cloud-controller manager /configz, same with GroupName.
	ConfigzName = "cloudcontrollermanager.config.k8s.io"
)

// NewCloudControllerManagerCommand creates a *cobra.Command object with default parameters
func NewCloudControllerManagerCommand() *cobra.Command {
	s, err := options.NewCloudControllerManagerOptions()
	if err != nil {
		klog.Fatalf("unable to initialize command options: %v", err)
	}

	cmd := &cobra.Command{
		Use:  "cloud-controller-manager",
		Long: `The Cloud controller manager is a daemon that embeds the cloud specific control loops shipped with Kubernetes.`,
		Run: func(cmd *cobra.Command, args []string) {
			verflag.PrintAndExitIfRequested("Cloud Provider Azure")
			cliflag.PrintFlags(cmd.Flags())

			c, err := s.Config(KnownControllers(), ControllersDisabledByDefault.List())
			if err != nil {
				klog.Errorf("Run: failed to configure cloud controller manager: %v", err)
				os.Exit(1)
			}

			err = StartHTTPServer(c.Complete(), wait.NeverStop)
			if err != nil {
				klog.Errorf("Run: railed to start HTTP server: %v", err)
				os.Exit(1)
			}

			if c.ComponentConfig.Generic.LeaderElection.LeaderElect {
				// Identity used to distinguish between multiple cloud controller manager instances
				id, err := os.Hostname()
				if err != nil {
					klog.Errorf("Run: failed to get host name: %v", err)
					os.Exit(1)
				}
				// add a uniquifier so that two processes on the same host don't accidentally both become active
				id = id + "_" + string(uuid.NewUUID())

				// Lock required for leader election
				rl, err := resourcelock.NewFromKubeconfig(c.ComponentConfig.Generic.LeaderElection.ResourceLock,
					c.ComponentConfig.Generic.LeaderElection.ResourceNamespace,
					c.ComponentConfig.Generic.LeaderElection.ResourceName,
					resourcelock.ResourceLockConfig{
						Identity:      id,
						EventRecorder: c.EventRecorder,
					},
					c.Kubeconfig,
					c.ComponentConfig.Generic.LeaderElection.RenewDeadline.Duration)
				if err != nil {
					klog.Fatalf("error creating lock: %v", err)
				}

				// Try and become the leader and start cloud controller manager loops
				var electionChecker *leaderelection.HealthzAdaptor
				if c.ComponentConfig.Generic.LeaderElection.LeaderElect {
					electionChecker = leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)
				}

				leaderelection.RunOrDie(context.TODO(), leaderelection.LeaderElectionConfig{
					Lock:          rl,
					LeaseDuration: c.ComponentConfig.Generic.LeaderElection.LeaseDuration.Duration,
					RenewDeadline: c.ComponentConfig.Generic.LeaderElection.RenewDeadline.Duration,
					RetryPeriod:   c.ComponentConfig.Generic.LeaderElection.RetryPeriod.Duration,
					Callbacks: leaderelection.LeaderCallbacks{
						OnStartedLeading: RunWrapper(s, c),
						OnStoppedLeading: func() {
							panic("leaderelection lost")
						},
					},
					WatchDog: electionChecker,
					Name:     "cloud-controller-manager",
				})

				panic("unreachable")
			}

			RunWrapper(s, c)(context.TODO())
			panic("unreachable")
		},
	}

	fs := cmd.Flags()
	namedFlagSets := s.Flags(KnownControllers(), ControllersDisabledByDefault.List())
	verflag.AddFlags(namedFlagSets.FlagSet("global"))
	globalflag.AddGlobalFlags(namedFlagSets.FlagSet("global"), cmd.Name())

	for _, f := range namedFlagSets.FlagSets {
		fs.AddFlagSet(f)
	}
	usageFmt := "Usage:\n  %s\n"
	cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	cmd.SetUsageFunc(func(cmd *cobra.Command) error {
		fmt.Fprintf(cmd.OutOrStderr(), usageFmt, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStderr(), namedFlagSets, cols)
		return nil
	})
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n"+usageFmt, cmd.Long, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStdout(), namedFlagSets, cols)
	})

	return cmd
}

// RunWrapper adapts the ccm boot logic to the leader elector call back function
func RunWrapper(s *options.CloudControllerManagerOptions, c *cloudcontrollerconfig.Config) func(ctx context.Context) {
	return func(ctx context.Context) {
		if !c.DynamicReloadingConfig.EnableDynamicReloading {
			klog.V(1).Infof("using static initialization from config file %s", c.ComponentConfig.KubeCloudShared.CloudProvider.CloudConfigFile)
			if err := Run(context.TODO(), c.Complete()); err != nil {
				klog.Errorf("RunWrapper: failed to start cloud controller manager: %v", err)
				os.Exit(1)
			}

			panic("unreachable")
		} else {
			var updateCh chan struct{}
			if c.ComponentConfig.KubeCloudShared.CloudProvider.CloudConfigFile != "" {
				klog.V(1).Infof("RunWrapper: using dynamic initialization from config file %s, starting the file watcher", c.ComponentConfig.KubeCloudShared.CloudProvider.CloudConfigFile)
				updateCh = dynamic.RunFileWatcherOrDie(c.ComponentConfig.KubeCloudShared.CloudProvider.CloudConfigFile)
			} else {
				klog.V(1).Infof("RunWrapper: using dynamic initialization from secret %s/%s, starting the secret watcher", c.DynamicReloadingConfig.CloudConfigSecretNamespace, c.DynamicReloadingConfig.CloudConfigSecretName)
				updateCh = dynamic.RunSecretWatcherOrDie(c)
			}

			errCh := make(chan error, 1)
			cancelFunc := runAsync(s, errCh)
			for {
				select {
				case <-updateCh:
					klog.V(2).Info("RunWrapper: detected the cloud config has been updated, re-constructing the cloud controller manager")

					// stop the previous goroutines
					cancelFunc()

					// start new goroutines
					cancelFunc = runAsync(s, errCh)
				case err := <-errCh:
					klog.Errorf("RunWrapper: failed to start cloud controller manager: %v", err)
					os.Exit(1)
				}
			}
		}
	}
}

func runAsync(s *options.CloudControllerManagerOptions, errCh chan error) context.CancelFunc {
	ctx, cancelFunc := context.WithCancel(context.Background())

	go func() {
		c, err := s.Config(KnownControllers(), ControllersDisabledByDefault.List())
		if err != nil {
			klog.Errorf("RunAsync: failed to configure cloud controller manager: %v", err)
			os.Exit(1)
		}

		if err := Run(ctx, c.Complete()); err != nil {
			klog.Errorf("RunAsync: failed to run cloud controller manager: %v", err)
			errCh <- err
		}

		klog.V(1).Infof("RunAsync: stopping")
	}()

	return cancelFunc
}

// StartHTTPServer starts the controller manager HTTP server
func StartHTTPServer(c *cloudcontrollerconfig.CompletedConfig, stopCh <-chan struct{}) error {
	// Setup any healthz checks we will want to use.
	var checks []healthz.HealthChecker
	var electionChecker *leaderelection.HealthzAdaptor
	if c.ComponentConfig.Generic.LeaderElection.LeaderElect {
		electionChecker = leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)
		checks = append(checks, electionChecker)
	}

	// Start the controller manager HTTP server
	if c.SecureServing != nil {
		unsecuredMux := genericcontrollermanager.NewBaseHandler(&c.ComponentConfig.Generic.Debugging, checks...)
		handler := genericcontrollermanager.BuildHandlerChain(unsecuredMux, &c.Authorization, &c.Authentication)
		// TODO: handle stoppedCh returned by c.SecureServing.Serve
		if _, err := c.SecureServing.Serve(handler, 0, stopCh); err != nil {
			return err
		}

		healthz.InstallReadyzHandler(unsecuredMux, checks...)
	}
	if c.InsecureServing != nil {
		unsecuredMux := genericcontrollermanager.NewBaseHandler(&c.ComponentConfig.Generic.Debugging, checks...)
		insecureSuperuserAuthn := server.AuthenticationInfo{Authenticator: &server.InsecureSuperuser{}}
		handler := genericcontrollermanager.BuildHandlerChain(unsecuredMux, nil, &insecureSuperuserAuthn)
		if err := c.InsecureServing.Serve(handler, 0, stopCh); err != nil {
			return err
		}

		healthz.InstallReadyzHandler(unsecuredMux, checks...)
	}

	return nil
}

// Run runs the ExternalCMServer.  This should never exit.
func Run(ctx context.Context, c *cloudcontrollerconfig.CompletedConfig) error {
	// To help debugging, immediately log version
	klog.Infof("Version: %+v", version.Get())

	var (
		cloud cloudprovider.Interface
		err   error
	)

	if c.ComponentConfig.KubeCloudShared.CloudProvider.CloudConfigFile != "" {
		cloud, err = provider.NewCloudFromConfigFile(c.ComponentConfig.KubeCloudShared.CloudProvider.CloudConfigFile, true)
		if err != nil {
			klog.Fatalf("Cloud provider azure could not be initialized: %v", err)
		}
	} else if c.DynamicReloadingConfig.EnableDynamicReloading && c.DynamicReloadingConfig.CloudConfigSecretName != "" {
		cloud, err = provider.NewCloudFromSecret(c.ClientBuilder, c.DynamicReloadingConfig.CloudConfigSecretName, c.DynamicReloadingConfig.CloudConfigSecretNamespace, c.DynamicReloadingConfig.CloudConfigKey)
		if err != nil {
			klog.Fatalf("Run: Cloud provider azure could not be initialized dynamically from secret %s/%s: %v", c.DynamicReloadingConfig.CloudConfigSecretNamespace, c.DynamicReloadingConfig.CloudConfigSecretName, err)
		}
	}

	if cloud == nil {
		klog.Fatalf("cloud provider is nil, please check if the --cloud-config is set properly")
	}

	if !cloud.HasClusterID() {
		if c.ComponentConfig.KubeCloudShared.AllowUntaggedCloud {
			klog.Warning("detected a cluster without a ClusterID.  A ClusterID will be required in the future.  Please tag your cluster to avoid any future issues")
		} else {
			klog.Fatalf("no ClusterID found.  A ClusterID is required for the cloud provider to function properly.  This check can be bypassed by setting the allow-untagged-cloud option")
		}
	}

	// setup /configz endpoint
	if cz, err := configz.New(ConfigzName); err == nil {
		cz.Set(c.ComponentConfig)
	} else {
		klog.Errorf("unable to register configz: %v", err)
	}

	if err := startControllers(c, ctx.Done(), cloud, newControllerInitializers()); err != nil {
		klog.Fatalf("error running controllers: %v", err)
	}

	return nil
}

// startControllers starts the cloud specific controller loops.
func startControllers(c *cloudcontrollerconfig.CompletedConfig, stopCh <-chan struct{}, cloud cloudprovider.Interface, controllers map[string]initFunc) error {
	// Initialize the cloud provider with a reference to the clientBuilder
	cloud.Initialize(c.ClientBuilder, stopCh)
	// Set the informer on the user cloud object
	if informerUserCloud, ok := cloud.(cloudprovider.InformerUser); ok {
		informerUserCloud.SetInformers(c.SharedInformers)
	}

	for controllerName, initFn := range controllers {
		if !genericcontrollermanager.IsControllerEnabled(controllerName, ControllersDisabledByDefault, c.ComponentConfig.Generic.Controllers) {
			klog.Warningf("%q is disabled", controllerName)
			continue
		}

		klog.V(1).Infof("Starting %q", controllerName)
		_, started, err := initFn(c, cloud, stopCh)
		if err != nil {
			klog.Errorf("Error starting %q: %s", controllerName, err.Error())
			return err
		}
		if !started {
			klog.Warningf("Skipping %q", controllerName)
			continue
		}
		klog.Infof("Started %q", controllerName)

		time.Sleep(wait.Jitter(c.ComponentConfig.Generic.ControllerStartInterval.Duration, ControllerStartJitter))
	}

	// If apiserver is not running we should wait for some time and fail only then. This is particularly
	// important when we start apiserver and controller manager at the same time.
	if err := genericcontrollermanager.WaitForAPIServer(c.VersionedClient, 10*time.Second); err != nil {
		klog.Fatalf("Failed to wait for apiserver being healthy: %v", err)
	}

	klog.V(2).Infof("startControllers: starting shared informers")
	c.SharedInformers.Start(stopCh)

	<-stopCh
	klog.V(1).Infof("startControllers: received stopping signal, exiting")

	return nil
}

// initFunc is used to launch a particular controller.  It may run additional "should I activate checks".
// Any error returned will cause the controller process to `Fatal`
// The bool indicates whether the controller was enabled.
type initFunc func(ctx *cloudcontrollerconfig.CompletedConfig, cloud cloudprovider.Interface, stop <-chan struct{}) (debuggingHandler http.Handler, enabled bool, err error)

// KnownControllers indicate the default controller we are known.
func KnownControllers() []string {
	ret := sets.StringKeySet(newControllerInitializers())
	return ret.List()
}

// ControllersDisabledByDefault is the controller disabled default when starting cloud-controller managers.
var ControllersDisabledByDefault = sets.NewString()

// newControllerInitializers is a private map of named controller groups (you can start more than one in an init func)
// paired to their initFunc.  This allows for structured downstream composition and subdivision.
func newControllerInitializers() map[string]initFunc {
	controllers := map[string]initFunc{}
	controllers["cloud-node"] = startCloudNodeController
	controllers["cloud-node-lifecycle"] = startCloudNodeLifecycleController
	controllers["service"] = startServiceController
	controllers["route"] = startRouteController
	controllers["node-ipam"] = startNodeIpamController
	return controllers
}
