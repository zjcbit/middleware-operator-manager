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

// The operator manager is responsible for monitoring
// rediscluster cr.., and creating rediscluster and initialize rediscluster
package main

import (
	"fmt"
	"github.com/spf13/pflag"
	"harmonycloud.cn/middleware-operator-manager/cmd/operator-manager/app"
	"harmonycloud.cn/middleware-operator-manager/cmd/operator-manager/app/options"
	"k8s.io/apiserver/pkg/util/flag"
	"k8s.io/apiserver/pkg/util/logs"
	"k8s.io/kubernetes/pkg/version/verflag"
	"os"
)

func main() {
	s := options.NewOMServer()
	s.AddFlags(pflag.CommandLine, app.KnownOperators())

	flag.InitFlags()
	logs.InitLogs()
	defer logs.FlushLogs()

	verflag.PrintAndExitIfRequested()

	if err := app.Run(s); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
