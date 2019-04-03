/*
Copyright 2014 The Kubernetes Authors.

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

// Package app implements a server that runs a set of active
// components.  This includes replication controllers, service endpoints and
// nodes.
//
package app

import (
	"k8s.io/apiserver/pkg/server/healthz"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"

	"harmonycloud.cn/middleware-operator-manager/util/configz"
	"k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/kubernetes/pkg/version"

	"fmt"
	"github.com/golang/glog"
	"harmonycloud.cn/middleware-operator-manager/cmd/operator-manager/app/options"
	"harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	redisInformerFactory "harmonycloud.cn/middleware-operator-manager/pkg/clients/informers/externalversions"
	"harmonycloud.cn/middleware-operator-manager/pkg/operator"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/pkg/controller"
	"time"
)

const (
	// Jitter used when starting operator managers
	OperatorStartJitter = 1.0
)

// Run runs the OMServer.  This should never exit.
func Run(s *options.OperatorManagerServer) error {
	// To help debugging, immediately log version
	glog.Infof("Version: %+v", version.Get())

	kubeClient, leaderElectionClient, extensionCRClient, kubeconfig, err := createClients(s)

	if err != nil {
		return err
	}

	err = CreateRedisClusterCRD(extensionCRClient)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			glog.Infof("redis cluster crd is already created.")
		} else {
			fmt.Fprint(os.Stderr, err)
			return err
		}
	}

	go startHTTP(s)

	recorder := createRecorder(kubeClient)

	run := func(stop <-chan struct{}) {
		operatorClientBuilder := operator.SimpleOperatorClientBuilder{
			ClientConfig: kubeconfig,
		}

		rootClientBuilder := controller.SimpleControllerClientBuilder{
			ClientConfig: kubeconfig,
		}

		otx, err := CreateOperatorContext(s, kubeconfig, operatorClientBuilder, rootClientBuilder, stop)
		if err != nil {
			glog.Fatalf("error building controller context: %v", err)
		}

		otx.InformerFactory = informers.NewSharedInformerFactory(kubeClient, time.Duration(s.ResyncPeriod)*time.Second)

		if err := StartOperators(otx, NewOperatorInitializers()); err != nil {
			glog.Fatalf("error starting operators: %v", err)
		}

		otx.RedisInformerFactory.Start(otx.Stop)
		otx.InformerFactory.Start(otx.Stop)
		close(otx.InformersStarted)

		select {}
	}

	if !s.LeaderElection.LeaderElect {
		run(nil)
		panic("unreachable")
	}

	id, err := os.Hostname()
	if err != nil {
		return err
	}

	rl, err := resourcelock.New(s.LeaderElection.ResourceLock,
		"kube-system",
		"middleware-operator-manager",
		leaderElectionClient.CoreV1(),
		resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: recorder,
		})
	if err != nil {
		glog.Fatalf("error creating lock: %v", err)
	}

	leaderelection.RunOrDie(leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: s.LeaderElection.LeaseDuration.Duration,
		RenewDeadline: s.LeaderElection.RenewDeadline.Duration,
		RetryPeriod:   s.LeaderElection.RetryPeriod.Duration,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {
				glog.Fatalf("leaderelection lost")
			},
		},
	})
	panic("unreachable")
}

func StartOperators(otx OperatorContext, operators map[string]InitFunc) error {

	for operatorName, initFn := range operators {
		if !otx.IsControllerEnabled(operatorName) {
			glog.Warningf("%q is disabled", operatorName)
			continue
		}

		time.Sleep(wait.Jitter(otx.Options.ControllerStartInterval.Duration, OperatorStartJitter))

		glog.V(1).Infof("Starting %q", operatorName)
		started, err := initFn(otx)
		if err != nil {
			glog.Errorf("Error starting %q", operatorName)
			return err
		}
		if !started {
			glog.Warningf("Skipping %q", operatorName)
			continue
		}
		glog.Infof("Started %q", operatorName)
	}

	return nil
}

// CreateOperatorContext creates a context struct containing references to resources needed by the
// Operators such as clientBuilder
func CreateOperatorContext(s *options.OperatorManagerServer, kubeConfig *restclient.Config, operatorClientBuilder operator.OperatorClientBuilder, rootClientBuilder controller.ControllerClientBuilder, stop <-chan struct{}) (OperatorContext, error) {
	versionedClient := operatorClientBuilder.ClientOrDie("middleware-shared-informers")
	sharedInformers := redisInformerFactory.NewSharedInformerFactory(versionedClient, time.Duration(s.ResyncPeriod)*time.Second)

	/*availableResources, err := GetAvailableResources(rootClientBuilder)
	if err != nil {
		return OperatorContext{}, err
	}*/

	otx := OperatorContext{
		kubeConfig:            kubeConfig,
		OperatorClientBuilder: operatorClientBuilder,
		DefaultClientBuilder:  rootClientBuilder,
		RedisInformerFactory:  sharedInformers,
		Options:               *s,
		//AvailableResources: availableResources,
		Stop:             stop,
		InformersStarted: make(chan struct{}),
	}
	return otx, nil
}

/*// TODO: In general, any controller checking this needs to be dynamic so
//  users don't have to restart their controller manager if they change the apiserver.
// Until we get there, the structure here needs to be exposed for the construction of a proper ControllerContext.
func GetAvailableResources(clientBuilder controller.ControllerClientBuilder) (map[schema.GroupVersionResource]bool, error) {
	var discoveryClient discovery.DiscoveryInterface

	var healthzContent string
	// If apiserver is not running we should wait for some time and fail only then. This is particularly
	// important when we start apiserver and controller manager at the same time.
	err := wait.PollImmediate(time.Second, 10*time.Second, func() (bool, error) {
		client, err := clientBuilder.Client("controller-discovery")
		if err != nil {
			glog.Errorf("Failed to get api versions from server: %v", err)
			return false, nil
		}

		healthStatus := 0
		resp := client.Discovery().RESTClient().Get().AbsPath("/healthz").Do().StatusCode(&healthStatus)
		if healthStatus != http.StatusOK {
			glog.Errorf("Server isn't healthy yet.  Waiting a little while.")
			return false, nil
		}
		content, _ := resp.Raw()
		healthzContent = string(content)

		discoveryClient = client.Discovery()
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get api versions from server: %v: %v", healthzContent, err)
	}

	resourceMap, err := discoveryClient.ServerResources()
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to get all supported resources from server: %v", err))
	}
	if len(resourceMap) == 0 {
		return nil, fmt.Errorf("unable to get any supported resources from server")
	}

	allResources := map[schema.GroupVersionResource]bool{}
	for _, apiResourceList := range resourceMap {
		version, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if err != nil {
			return nil, err
		}
		for _, apiResource := range apiResourceList.APIResources {
			allResources[version.WithResource(apiResource.Name)] = true
		}
	}

	return allResources, nil
}*/

func startHTTP(s *options.OperatorManagerServer) {
	mux := http.NewServeMux()
	healthz.InstallHandler(mux)
	if s.EnableProfiling {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	configz.InstallHandler(mux)

	server := &http.Server{
		Addr:    net.JoinHostPort(s.Address, strconv.Itoa(int(s.Port))),
		Handler: mux,
	}
	glog.Fatal(server.ListenAndServe())
}

func createRecorder(kubeClient *clientset.Clientset) record.EventRecorder {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(kubeClient.CoreV1().RESTClient()).Events("")})
	return eventBroadcaster.NewRecorder(legacyscheme.Scheme, v1.EventSource{Component: "operator-manager"})
}

func createClients(s *options.OperatorManagerServer) (*clientset.Clientset, *clientset.Clientset, *extensionsclient.Clientset, *restclient.Config, error) {
	kubeconfig, err := clientcmd.BuildConfigFromFlags(s.Master, s.Kubeconfig)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	//controller-manager的类型为"application/vnd.kubernetes.protobuf"但在这里有问题,导致同步事件AddFunc、UpdateFunc、delFunc出问题，
	// 不能加,后续细细研究,
	//kubeconfig.ContentConfig.ContentType = s.ContentType
	// Override kubeconfig qps/burst settings from flags
	kubeconfig.QPS = s.KubeAPIQPS
	kubeconfig.Burst = int(s.KubeAPIBurst)
	kubeClient, err := clientset.NewForConfig(restclient.AddUserAgent(kubeconfig, "operator-manager"))
	if err != nil {
		glog.Fatalf("Invalid API configuration: %v", err)
	}
	leaderElectionClient := clientset.NewForConfigOrDie(restclient.AddUserAgent(kubeconfig, "leader-election"))

	extensionCRClient, err := extensionsclient.NewForConfig(kubeconfig)
	if err != nil {
		glog.Fatalf("create extensionCRClient is error: %v", err)
	}

	return kubeClient, leaderElectionClient, extensionCRClient, kubeconfig, nil
}

type OperatorContext struct {
	// ClientBuilder will provide a client for this operator to use
	OperatorClientBuilder operator.OperatorClientBuilder

	kubeConfig *restclient.Config

	// ClientBuilder will provide a default client for this operator to use
	DefaultClientBuilder controller.ControllerClientBuilder

	// RedisInformerFactory gives access to informers for the operator.
	RedisInformerFactory redisInformerFactory.SharedInformerFactory

	// InformerFactory gives access to informers for the operator.
	InformerFactory informers.SharedInformerFactory

	// Options provides access to init options for a given operator
	Options options.OperatorManagerServer

	// AvailableResources is a map listing currently available resources
	//AvailableResources map[schema.GroupVersionResource]bool

	// Stop is the stop channel
	Stop <-chan struct{}

	// InformersStarted is closed after all of the controllers have been initialized and are running.  After this point it is safe,
	// for an individual controller to start the shared informers. Before it is closed, they should not.
	InformersStarted chan struct{}
}

func (c OperatorContext) IsControllerEnabled(name string) bool {
	return IsOperatorEnabled(name, c.Options.Operators...)
}

func IsOperatorEnabled(name string, controllers ...string) bool {
	hasStar := false
	for _, ctrl := range controllers {
		if ctrl == name {
			return true
		}
		if ctrl == "-"+name {
			return false
		}
		if ctrl == "*" {
			hasStar = true
		}
	}
	// if we get here, there was no explicit choice
	if !hasStar {
		// nothing on by default
		return false
	}

	return true
}

func KnownOperators() []string {
	ret := sets.StringKeySet(NewOperatorInitializers())
	return ret.List()
}

// InitFunc is used to launch a particular Operator.  It may run additional "should I activate checks".
// Any error returned will cause the Operator process to `Fatal`
// The bool indicates whether the Operator was enabled.
type InitFunc func(OperatorContext) (bool, error)

// NewOperatorInitializers is a public map of named operator groups (you can start more than one in an init func)
// paired to their InitFunc.  This allows for structured downstream composition and subdivision.
func NewOperatorInitializers() map[string]InitFunc {
	controllers := map[string]InitFunc{}
	controllers["rediscluster"] = startRedisClusterController

	return controllers
}

func CreateRedisClusterCRD(extensionCRClient *extensionsclient.Clientset) error {
	//TODO add CustomResourceValidation due to guarantee redis operator work normally,k8s1.12
	crd := &v1beta1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "redisclusters." + v1alpha1.SchemeGroupVersion.Group,
		},
		Spec: v1beta1.CustomResourceDefinitionSpec{
			Group:   v1alpha1.SchemeGroupVersion.Group,
			Version: v1alpha1.SchemeGroupVersion.Version,
			Scope:   v1beta1.NamespaceScoped,
			Names: v1beta1.CustomResourceDefinitionNames{
				Kind:       "RedisCluster",
				ListKind:   "RedisClusterList",
				Plural:     "redisclusters",
				Singular:   "rediscluster",
				ShortNames: []string{"rec"},
			},
			/*Validation: &v1beta1.CustomResourceValidation {

			},*/
		},
	}
	_, err := extensionCRClient.ApiextensionsV1beta1().CustomResourceDefinitions().Create(crd)
	return err
}
