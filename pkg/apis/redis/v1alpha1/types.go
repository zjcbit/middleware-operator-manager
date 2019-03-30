/*
Copyright 2017 The Kubernetes Authors.

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

package v1alpha1

import (
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RedisClusterUpdateStrategyType is a string enumeration type that enumerates
// all possible update strategies for the RedisCluster.
type RedisClusterUpdateStrategyType string

const (
	AssignReceiveStrategyType RedisClusterUpdateStrategyType = "AssignReceive"
	AutoReceiveStrategyType   RedisClusterUpdateStrategyType = "AutoReceive"
)

type RedisClusterConditionType string

const (
	MasterConditionType RedisClusterConditionType = "master"
	SalveConditionType  RedisClusterConditionType = "salve"
)

type RedisClusterPhase string

// These are the valid phases of a RedisCluster.
const (
	// RedisClusterUpgrading means the RedisCluster is Upgrading
	RedisClusterUpgrading RedisClusterPhase = "Upgrading"
	// RedisClusterNone means the RedisCluster crd is first create
	RedisClusterNone RedisClusterPhase = "None"
	// RedisClusterCreating means the RedisCluster is Creating
	RedisClusterCreating RedisClusterPhase = "Creating"
	// RedisClusterRunning means the RedisCluster is Running after RedisCluster create and initialize success
	RedisClusterRunning RedisClusterPhase = "Running"
	// RedisClusterFailed means the RedisCluster is Failed
	RedisClusterFailed RedisClusterPhase = "Failed"
	// RedisClusterScaling means the RedisCluster is Scaling
	RedisClusterScaling RedisClusterPhase = "Scaling"
)

// 为当前类型生成客户端
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type RedisCluster struct {
	metav1.TypeMeta `json:",inline"`
	// Metadata is the standard object metadata.
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec is the specification for the behaviour of the rediscluster
	// More info: http://releases.k8s.io/HEAD/docs/devel/api-conventions.md#spec-and-status.
	// +optional
	Spec RedisClusterSpec `json:"spec,omitempty"`

	// Status is the current information about the rediscluster.
	// +optional
	Status RedisClusterStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type RedisClusterList struct {
	metav1.TypeMeta `json:",inline"`
	// Metadata is the standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata"`

	// Items is the list of RedisCluster objects.
	Items []RedisCluster `json:"items"`
}

// RedisClusterSpec describes the desired functionality of the ComplexPodScale.
type RedisClusterSpec struct {

	// Replicas is the desired number of replicas of the given Template.
	// These are replicas in the sense that they are instantiations of the
	// same Template, but individual replicas also have a consistent identity.
	// If unspecified, defaults to 1.
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	Pause          bool                          `json:"pause,omitempty"`
	Repository     string                        `json:"repository,omitempty"`
	Version        string                        `json:"version,omitempty"`
	UpdateStrategy RedisClusterUpdateStrategy    `json:"updateStrategy,omitempty"`
	Pod            []RedisClusterPodTemplateSpec `json:"pod,omitempty"`
}

type RedisClusterUpdateStrategy struct {
	Type             RedisClusterUpdateStrategyType `json:"type,omitempty"`
	AssignStrategies []SlotsAssignStrategy          `json:"assignStrategies,omitempty"`
}

type SlotsAssignStrategy struct {
	Slots *int32 `json:"slots,omitempty"`
	//nodeid
	FromReplicas string `json:"fromReplicas,omitempty"`
}

type RedisClusterPodTemplateSpec struct {
	Configmap string `json:"configmap,omitempty" protobuf:"bytes,4,opt,name=configmap"`

	MonitorImage string `json:"monitorImage,omitempty" protobuf:"bytes,4,opt,name=monitorImage"`

	InitImage string `json:"initImage,omitempty" protobuf:"bytes,4,opt,name=initImage"`

	MiddlewareImage string `json:"middlewareImage,omitempty" protobuf:"bytes,4,opt,name=middlewareImage"`

	Volumes RedisClusterPodVolume `json:"volumes"`

	// List of environment variables to set in the container.
	// Cannot be updated.
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	Env []v1.EnvVar `json:"env,omitempty" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,7,rep,name=env"`
	// Compute Resources required by this container.
	// Cannot be updated.
	// More info: https://kubernetes.io/docs/concepts/storage/persistent-volumes#resources
	// +optional
	Resources v1.ResourceRequirements `json:"resources,omitempty" protobuf:"bytes,8,opt,name=resources"`
	// If specified, the pod's scheduling constraints
	// +optional
	Affinity *v1.Affinity `json:"affinity,omitempty" protobuf:"bytes,18,opt,name=affinity"`

	// Annotations is an unstructured key value map stored with a resource that may be
	// set by external tools to store and retrieve arbitrary metadata. They are not
	// queryable and should be preserved when modifying objects.
	// More info: http://kubernetes.io/docs/user-guide/annotations
	// +optional
	Annotations map[string]string `json:"annotations,omitempty" protobuf:"bytes,12,rep,name=annotations"`

	// Map of string keys and values that can be used to organize and categorize
	// (scope and select) objects. May match selectors of replication controllers
	// and services.
	// More info: http://kubernetes.io/docs/user-guide/labels
	// +optional
	Labels map[string]string `json:"labels,omitempty" protobuf:"bytes,11,rep,name=labels"`

	// updateStrategy indicates the StatefulSetUpdateStrategy that will be
	// employed to update Pods in the StatefulSet when a revision is made to
	// Template.
	UpdateStrategy appsv1.StatefulSetUpdateStrategy `json:"updateStrategy,omitempty" protobuf:"bytes,7,opt,name=updateStrategy"`
}

type RedisClusterPodVolume struct {
	Type                      string `json:"type,omitempty" protobuf:"bytes,4,opt,name=type"`
	PersistentVolumeClaimName string `json:"persistentVolumeClaimName,omitempty" protobuf:"bytes,4,opt,name=persistentVolumeClaimName"`
}

type RedisClusterStatus struct {
	// replicas is the number of Pods created by the RedisCluster.
	Replicas int32 `json:"replicas" protobuf:"varint,2,opt,name=replicas"`
	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`

	Phase RedisClusterPhase `json:"phase"`

	Conditions []RedisClusterCondition `json:"conditions"`
}

type RedisClusterCondition struct {
	Name string `json:"name,omitempty" protobuf:"bytes,4,opt,name=name"`
	// Type of RedisCluster condition.
	Type       RedisClusterConditionType `json:"type"`
	InstanceIP string                    `json:"instanceIP,omitempty" protobuf:"bytes,4,opt,name=instanceIP"`

	NodeId string `json:"nodeId,omitempty" protobuf:"bytes,4,opt,name=nodeId"`

	MasterNodeId string `json:"masterNodeId,omitempty" protobuf:"bytes,4,opt,name=masterNodeId"`

	DomainName string `json:"domainName,omitempty" protobuf:"bytes,4,opt,name=domainName"`

	Slots    int32  `json:"slots,omitempty" protobuf:"bytes,4,opt,name=slots"`
	Hostname string `json:"hostname,omitempty" protobuf:"bytes,4,opt,name=hostname"`
	HostIP   string `json:"hostIP,omitempty" protobuf:"bytes,4,opt,name=hostIP"`
	// Status of the condition, one of True, False, Unknown.
	Status v1.ConditionStatus `json:"status" protobuf:"bytes,2,opt,name=status,casttype=k8s.io/api/core/v1.ConditionStatus"`
	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,3,opt,name=lastTransitionTime"`
	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`
	// A human readable message indicating details about the transition.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,5,opt,name=message"`
}

// LeaderElectionConfiguration defines the configuration of leader election
// clients for components that can run with leader election enabled.
type LeaderElectionConfiguration struct {
	// leaderElect enables a leader election client to gain leadership
	// before executing the main loop. Enable this when running replicated
	// components for high availability.
	LeaderElect bool
	// leaseDuration is the duration that non-leader candidates will wait
	// after observing a leadership renewal until attempting to acquire
	// leadership of a led but unrenewed leader slot. This is effectively the
	// maximum duration that a leader can be stopped before it is replaced
	// by another candidate. This is only applicable if leader election is
	// enabled.
	LeaseDuration metav1.Duration
	// renewDeadline is the interval between attempts by the acting master to
	// renew a leadership slot before it stops leading. This must be less
	// than or equal to the lease duration. This is only applicable if leader
	// election is enabled.
	RenewDeadline metav1.Duration
	// retryPeriod is the duration the clients should wait between attempting
	// acquisition and renewal of a leadership. This is only applicable if
	// leader election is enabled.
	RetryPeriod metav1.Duration
	// resourceLock indicates the resource object type that will be used to lock
	// during leader election cycles.
	ResourceLock string
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type OperatorManagerConfig struct {
	metav1.TypeMeta

	// Operators is the list of operators to enable or disable
	// '*' means "all enabled by default operators"
	// 'foo' means "enable 'foo'"
	// '-foo' means "disable 'foo'"
	// first item for a particular name wins
	Operators []string

	// ConcurrentRedisClusterSyncs is the number of redisCluster objects that are
	// allowed to sync concurrently. Larger number = more responsive redisClusters,
	// but more CPU (and network) load.
	ConcurrentRedisClusterSyncs int32

	//cluster create or upgrade timeout (min)
	ClusterTimeOut int32

	// How long to wait between starting controller managers
	ControllerStartInterval metav1.Duration

	ResyncPeriod int64
	// leaderElection defines the configuration of leader election client.
	LeaderElection LeaderElectionConfiguration
	// port is the port that the controller-manager's http service runs on.
	Port int32
	// address is the IP address to serve on (set to 0.0.0.0 for all interfaces).
	Address string

	// enableProfiling enables profiling via web interface host:port/debug/pprof/
	EnableProfiling bool
	// contentType is contentType of requests sent to apiserver.
	ContentType string
	// kubeAPIQPS is the QPS to use while talking with kubernetes apiserver.
	KubeAPIQPS float32
	// kubeAPIBurst is the burst to use while talking with kubernetes apiserver.
	KubeAPIBurst int32
}
