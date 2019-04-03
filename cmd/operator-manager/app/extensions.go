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

// Package app implements a server that runs a set of active
// components.  This includes replication controllers, service endpoints and
// nodes.
//
package app

import (
	"fmt"
	"harmonycloud.cn/middleware-operator-manager/pkg/operator/redis"
)

func startRedisClusterController(otx OperatorContext) (bool, error) {

	rco, err := redis.NewRedisClusterOperator(
		otx.RedisInformerFactory.Cr().V1alpha1().RedisClusters(),
		otx.InformerFactory.Apps().V1().StatefulSets(),
		otx.DefaultClientBuilder.ClientOrDie("default-kube-client"),
		otx.OperatorClientBuilder.ClientOrDie("rediscluster-operator"),
		otx.kubeConfig,
		otx.Options,
	)
	if err != nil {
		return true, fmt.Errorf("error creating rediscluster operator: %v", err)
	}
	go rco.Run(int(otx.Options.ConcurrentRedisClusterSyncs), otx.Stop)
	return true, nil
}
