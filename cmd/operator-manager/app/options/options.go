package options

import (
	"fmt"
	"github.com/spf13/pflag"
	"harmonycloud.cn/middleware-operator-manager/config"
	"harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"strings"
	"time"
)

type OperatorManagerServer struct {
	v1alpha1.OperatorManagerConfig
	Master     string
	Kubeconfig string
}

const operatorManagerPort = 10880

func NewOMServer() *OperatorManagerServer {
	s := OperatorManagerServer{
		OperatorManagerConfig: v1alpha1.OperatorManagerConfig{
			ControllerStartInterval:     metav1.Duration{Duration: 0 * time.Second},
			Operators:                   []string{"*"},
			LeaderElection:              config.DefaultLeaderElectionConfiguration(),
			ConcurrentRedisClusterSyncs: 1,
			Port:            operatorManagerPort,
			Address:         "0.0.0.0",
			ResyncPeriod:    60,
			ClusterTimeOut:  6,
			//controller-manager的类型为"application/vnd.kubernetes.protobuf"但在这里有问题,导致同步事件AddFunc、UpdateFunc、delFunc出问题，
			// 不能加,后续细细研究,用于构建kubeConfig
			//ContentType:     "application/vnd.kubernetes.protobuf",
			KubeAPIQPS:      20.0,
			KubeAPIBurst:    30,
			EnableProfiling: true,
		},
	}
	return &s
}

func (s *OperatorManagerServer) AddFlags(fs *pflag.FlagSet, allOperators []string) {
	fs.StringSliceVar(&s.Operators, "operators", s.Operators, fmt.Sprintf(""+
		"A list of operators to enable.  '*' enables all on-by-default operators, 'foo' enables the operator "+
		"named 'foo', '-foo' disables the operator named 'foo'.\nAll operators: %s", strings.Join(allOperators, ", ")))
	fs.StringVar(&s.Master, "master", s.Master, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	fs.StringVar(&s.Kubeconfig, "kubeconfig", s.Kubeconfig, "Path to kubeconfig file with authorization and master location information.")
	fs.Int64Var(&s.ResyncPeriod, "resync-period", 60, "resync frequency in seconds")
	fs.Int32Var(&s.ClusterTimeOut, "clusterTimeOut", 6, "cluster create or upgrade timeout in minutes")
	fs.Int32Var(&s.Port, "port", 10880, "resync frequency in seconds")
	fs.DurationVar(&s.ControllerStartInterval.Duration, "operator-start-interval", s.ControllerStartInterval.Duration, "Interval between starting operator managers.")
	config.BindFlags(&s.LeaderElection, fs)

	utilfeature.DefaultFeatureGate.AddFlag(fs)
}
