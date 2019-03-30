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

package operator

import (
	"github.com/golang/glog"
	clientgoclientset "harmonycloud.cn/middleware-operator-manager/pkg/clients/clientset/versioned"
	clientset "harmonycloud.cn/middleware-operator-manager/pkg/clients/clientset/versioned"
	restclient "k8s.io/client-go/rest"
)

// OperatorClientBuilder allows you to get clients and configs for operators
type OperatorClientBuilder interface {
	Config(name string) (*restclient.Config, error)
	ConfigOrDie(name string) *restclient.Config
	Client(name string) (clientset.Interface, error)
	ClientOrDie(name string) clientset.Interface
	ClientGoClient(name string) (clientgoclientset.Interface, error)
	ClientGoClientOrDie(name string) clientgoclientset.Interface
}

// SimpleOperatorClientBuilder returns a fixed client with different user agents
type SimpleOperatorClientBuilder struct {
	// ClientConfig is a skeleton config to clone and use as the basis for each controller client
	ClientConfig *restclient.Config
}

func (b SimpleOperatorClientBuilder) Config(name string) (*restclient.Config, error) {
	clientConfig := *b.ClientConfig
	return restclient.AddUserAgent(&clientConfig, name), nil
}

func (b SimpleOperatorClientBuilder) ConfigOrDie(name string) *restclient.Config {
	clientConfig, err := b.Config(name)
	if err != nil {
		glog.Fatal(err)
	}
	return clientConfig
}

func (b SimpleOperatorClientBuilder) Client(name string) (clientset.Interface, error) {
	clientConfig, err := b.Config(name)
	if err != nil {
		return nil, err
	}
	return clientset.NewForConfig(clientConfig)
}

func (b SimpleOperatorClientBuilder) ClientOrDie(name string) clientset.Interface {
	client, err := b.Client(name)
	if err != nil {
		glog.Fatal(err)
	}
	return client
}

func (b SimpleOperatorClientBuilder) ClientGoClient(name string) (clientgoclientset.Interface, error) {
	clientConfig, err := b.Config(name)
	if err != nil {
		return nil, err
	}
	return clientgoclientset.NewForConfig(clientConfig)
}

func (b SimpleOperatorClientBuilder) ClientGoClientOrDie(name string) clientgoclientset.Interface {
	client, err := b.ClientGoClient(name)
	if err != nil {
		glog.Fatal(err)
	}
	return client
}
