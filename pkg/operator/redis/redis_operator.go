/*
Copyright 2015 The Kubernetes Authors.

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

// Package RedisCluster contains all the logic for handling Kubernetes RedisClusters.
// It implements a set of strategies (rolling, recreate) for deploying an application,
// the means to rollback to previous versions, proportional scaling for mitigating
// risk, cleanup policy, and other useful features of RedisClusters.

package redis

import (
	"fmt"
	"github.com/golang/glog"
	redistype "harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	customclient "harmonycloud.cn/middleware-operator-manager/pkg/clients/clientset/versioned"
	custominfomer "harmonycloud.cn/middleware-operator-manager/pkg/clients/informers/externalversions/redis/v1alpha1"
	redisclusterLister "harmonycloud.cn/middleware-operator-manager/pkg/clients/listers/redis/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubernetes/pkg/controller"
		"time"
	"k8s.io/client-go/rest"
	"strings"
	"bytes"
	"harmonycloud.cn/middleware-operator-manager/util"
	"strconv"
	"harmonycloud.cn/middleware-operator-manager/cmd/operator-manager/app/options"
	"encoding/json"
)

const (
	// maxRetries is the number of times a RedisCluster will be retried before it is dropped out of the queue.
	// With the current rate-limiter in use (5ms*2^(maxRetries-1)) the following numbers represent the times
	// a RedisCluster is going to be requeued:
	//
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries            = 15
	redisServicePort6379  = 6379
	redisServicePort16379 = 16379
	pauseKey = "pause.middleware.harmonycloud.cn"
)

// controllerKind contains the schema.GroupVersionKind for this controller type.
var controllerKind = extensions.SchemeGroupVersion.WithKind("RedisCluster")

// RedisClusterOperator is responsible for synchronizing RedisCluster objects stored
// in the system with actual running replica sets and pods.
type RedisClusterOperator struct {
	//extensionCRClient *extensionsclient.Clientset
	customCRDClient customclient.Interface
	defaultClient   clientset.Interface
	kubeConfig     *rest.Config

	options *options.OperatorManagerServer

	eventRecorder   record.EventRecorder
	// To allow injection of syncRedisCluster for testing.
	syncHandler func(dKey string) error
	// used for unit testing
	enqueueRedisCluster func(redisCluster *redistype.RedisCluster)

	redisClusterInformer cache.SharedIndexInformer

	redisClusterLister redisclusterLister.RedisClusterLister

	// dListerSynced returns true if the redisCluster store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	redisClusterListerSynced cache.InformerSynced

	// redisCluster that need to be synced
	queue workqueue.RateLimitingInterface
}

// NewRedisClusterOperator creates a new RedisClusterOperator.
func NewRedisClusterOperator(redisInformer custominfomer.RedisClusterInformer, kubeClient clientset.Interface, customCRDClient customclient.Interface, kubeConfig *rest.Config, options options.OperatorManagerServer) (*RedisClusterOperator, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	// TODO: remove the wrapper when every clients have moved to use the clientset.
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(kubeClient.CoreV1().RESTClient()).Events("")})

	rco := &RedisClusterOperator{
		options: &options,
		kubeConfig: kubeConfig,
		defaultClient: kubeClient,
		//extensionCRClient: extensionCRClient,
		customCRDClient: customCRDClient,
		eventRecorder:   eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "operator-manager"}),
		queue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "rediscluster"),
	}

	redisInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    rco.addRedisCluster,
		UpdateFunc: rco.updateRedisCluster,
		// This will enter the sync loop and no-op, because the RedisCluster has been deleted from the store.
		DeleteFunc: rco.deleteRedisCluster,
	})

	rco.syncHandler = rco.syncRedisCluster
	rco.enqueueRedisCluster = rco.enqueue

	rco.redisClusterInformer = redisInformer.Informer()
	rco.redisClusterListerSynced = rco.redisClusterInformer.HasSynced
	rco.redisClusterLister = redisInformer.Lister()
	return rco, nil
}

// Run begins watching and syncing.
func (rco *RedisClusterOperator) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer rco.queue.ShutDown()

	glog.Infof("Starting rediscluster operator")
	defer glog.Infof("Shutting down rediscluster operator")

	if !controller.WaitForCacheSync("rediscluster", stopCh, rco.redisClusterListerSynced) {
		return
	}

	for i := 0; i < workers; i++ {
		go wait.Until(rco.worker, time.Second, stopCh)
	}

	<-stopCh
}

func (rco *RedisClusterOperator) addRedisCluster(obj interface{}) {
	rc := obj.(*redistype.RedisCluster)
	glog.V(4).Infof("Adding RedisCluster %s", rc.Name)

	//rco.updateRedisClusterStatus(rc, nil, redistype.RedisClusterNone, "")
	rco.enqueueRedisCluster(rc)
}

func (rco *RedisClusterOperator) updateRedisCluster(old, cur interface{}) {
	oldR := old.(*redistype.RedisCluster)
	curR := cur.(*redistype.RedisCluster)

	/*if curR.ResourceVersion == oldR.ResourceVersion {
		glog.V(4).Infof("%s/%s: skip same ResourceVersion: %s", curR.Namespace, curR.Name, curR.ResourceVersion)
		return
	}*/

	glog.V(4).Infof("Updating RedisCluster %s", oldR.Name)
	rco.enqueueRedisCluster(curR)
}

func (rco *RedisClusterOperator) deleteRedisCluster(obj interface{}) {
	d, ok := obj.(*redistype.RedisCluster)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Couldn't get object from tombstone %#v", obj))
			return
		}
		d, ok = tombstone.Obj.(*redistype.RedisCluster)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Tombstone contained object that is not a RedisCluster %#v", obj))
			return
		}
	}
	glog.V(4).Infof("Deleting RedisCluster %s", d.Name)
	rco.enqueueRedisCluster(d)
}

func (rco *RedisClusterOperator) enqueue(redisCluster *redistype.RedisCluster) {
	key, err := controller.KeyFunc(redisCluster)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Couldn't get key for object %#v: %v", redisCluster, err))
		return
	}

	rco.queue.Add(key)
}

func (rco *RedisClusterOperator) enqueueRateLimited(redisCluster *redistype.RedisCluster) {
	key, err := controller.KeyFunc(redisCluster)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Couldn't get key for object %#v: %v", redisCluster, err))
		return
	}

	rco.queue.AddRateLimited(key)
}

// enqueueAfter will enqueue a RedisCluster after the provided amount of time.
func (rco *RedisClusterOperator) enqueueAfter(RedisCluster *redistype.RedisCluster, after time.Duration) {
	key, err := controller.KeyFunc(RedisCluster)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("Couldn't get key for object %#v: %v", RedisCluster, err))
		return
	}

	rco.queue.AddAfter(key, after)
}

// resolveControllerRef returns the controller referenced by a ControllerRef,
// or nil if the ControllerRef could not be resolved to a matching controller
// of the correct Kind.
func (rco *RedisClusterOperator) resolveControllerRef(namespace string, controllerRef *metav1.OwnerReference) *redistype.RedisCluster {
	// We can't look up by UID, so look up by Name and then verify UID.
	// Don't even try to look up by Name if it's the wrong Kind.
	if controllerRef.Kind != controllerKind.Kind {
		return nil
	}
	/*d, err := rco.redisClusterInformer.GetStore()
	if err != nil {
		return nil
	}
	if d.UID != controllerRef.UID {
		// The controller we found with this Name is not the same one that the
		// ControllerRef points to.
		return nil
	}*/
	return nil
}

// worker runs a worker thread that just dequeues items, processes them, and marks them done.
// It enforces that the syncHandler is never invoked concurrently with the same key.
func (rco *RedisClusterOperator) worker() {
	for rco.processNextWorkItem() {
	}
}

func (rco *RedisClusterOperator) processNextWorkItem() bool {
	key, quit := rco.queue.Get()
	if quit {
		return false
	}
	defer rco.queue.Done(key)

	err := rco.syncHandler(key.(string))
	rco.handleErr(err, key)

	return true
}

func (rco *RedisClusterOperator) handleErr(err error, key interface{}) {
	if err == nil {
		rco.queue.Forget(key)
		return
	}

	if rco.queue.NumRequeues(key) < maxRetries {
		glog.V(2).Infof("Error syncing RedisCluster %v: %v", key, err)
		rco.queue.AddRateLimited(key)
		return
	}

	utilruntime.HandleError(err)
	glog.V(2).Infof("Dropping RedisCluster %q out of the queue: %v", key, err)
	rco.queue.Forget(key)
}

// syncRedisCluster will sync the redisCluster with the given key.
// This function is not meant to be invoked concurrently with the same key.
func (rco *RedisClusterOperator) syncRedisCluster(key string) error {
	startTime := time.Now()
	glog.V(4).Infof("Started syncing redisCluster %q (%v)", key, startTime)
	defer func() {
		glog.V(4).Infof("Finished syncing redisCluster %q (%v)", key, time.Since(startTime))
	}()

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	// 必须要先声明defer，否则不能捕获到panic异常
	defer func(){
		if err := recover(); err != nil{
			// 这里的err其实就是panic传入的内容
			glog.Errorf("recover panic error: %v", err)
		}
	}()
	// Deep-copy otherwise we are mutating our cache.
	// TODO: Deep-copy only when needed.
	//d := redisCluster.DeepCopy()
	err = rco.createOrUpgradeCluster(namespace, name)
	if err != nil {
		return err
	}

	return nil
}

func (rco *RedisClusterOperator) createOrUpgradeCluster(namespace, name string) error {

	redisCluster, err := rco.redisClusterLister.RedisClusters(namespace).Get(name)
	//_, err = rco.redisClusterInformer.GetIndexer().
	if errors.IsNotFound(err) {
		glog.V(2).Infof("RedisCluster %v/%v has been deleted", namespace, name)
		return nil
	}
	if err != nil {
		return err
	}

	rc, _ := json.Marshal(redisCluster)

	//TODO 调试用
	glog.V(4).Infof("** RedisCluster: %v", string(rc))

	//1、取到redisCluster对象，开始判断是否是第一次创建，用redisCluster.namespaces和redisCluster.name绑定statefulset

	//2、如果不存在则为初始化操作，执行创建service、statefulset的操作，

	//3、判断service-->endpoints实例是否都ready,如果ready则开始初始化redis集群的操作,否则阻塞等待

	//3.1 初始化成功后,更新redisCluster对象中的status

	//4、如果存在statefulset则为升级操作或者删除操作，执行修改statefulset的操作，

	//5、判断service-->endpoints新实例是否都ready,如果ready则开始升级redis集群的操作,否则阻塞等待

	//5.1 升级成功后,更新redisCluster对象中的status

	//6、启动一个协程一直探测集群的健康状态,实时更新到redisCluster对象的status中

	existSts, err := rco.defaultClient.AppsV1().StatefulSets(namespace).Get(name, metav1.GetOptions{})

	isInit := false
	if errors.IsNotFound(err) {
		glog.V(2).Infof("RedisCluster %v/%v has been create firstly, will create and init redis cluster", namespace, name)
		isInit = true
	} else if err != nil {
		glog.Errorf("get cluster statefulset: %v/%v is error: %v", namespace, name, err)
		return err
	} else {
		//可能是升级操作也可能是强制同步;升级操作实例和当前sts实例不一样
		//redisCluster里实例数小于当前sts实例数,只检查状态不进行缩容,TODO 缩容需求
		if *redisCluster.Spec.Replicas <= *existSts.Spec.Replicas {
			return rco.checkAndUpdateRedisClusterStatus(redisCluster)
		} else {
			//实例不一样则为扩容升级操作
			isInit = false
		}
	}

	//初始化集群
	if isInit {

		//更新状态为Creating
		newRedisCluster, err := rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterCreating, "")
		if err != nil {
			return err
		}

		//create service
		recService := rco.buildRedisClusterService(namespace, name)
		_, err = rco.defaultClient.CoreV1().Services(namespace).Create(recService)

		if errors.IsAlreadyExists(err) {
			glog.Warningf("redis cluster create service: %v/%v Is Already Exists", namespace, name)
		}else if err != nil {
			return fmt.Errorf("init redis cluster create service: %v/%v is error: %v", namespace, name, err)
		}

		recSts := rco.buildRedisClusterStatefulset(namespace, name, newRedisCluster)
		//create statefulset
		sts, err := rco.defaultClient.AppsV1().StatefulSets(namespace).Create(recSts)

		if err != nil {
			return fmt.Errorf("init redis cluster create statefulset: %v is error: %v", sts.Name, err)
		}

		//检测endpoint pod都ready,则创建和初始化集群
		go rco.createAndInitRedisCluster(sts, newRedisCluster)
	} else {

		//卡槽分配策略手动,但分配详情有误
		if (redisCluster.Spec.UpdateStrategy.Type == redistype.AssignReceiveStrategyType) && (int32(len(redisCluster.Spec.UpdateStrategy.AssignStrategies)) != ((*redisCluster.Spec.Replicas - redisCluster.Status.Replicas) / 2)) {
			err := fmt.Errorf("upgrade redis cluster: %v/%v slots AssignStrategies error", redisCluster.Namespace, redisCluster.Name)
			glog.Error(err.Error())
			rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterRunning, err.Error())
			return err
		}

		//更新状态为Scaling
		newRedisCluster, err := rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterScaling, "")
		if err != nil {
			return err
		}

		//升级集群
		recSts := rco.buildRedisClusterStatefulset(namespace, name, newRedisCluster)
		//update statefulset
		sts, err := rco.defaultClient.AppsV1().StatefulSets(namespace).Update(recSts)

		if err != nil {
			return fmt.Errorf("upgrade redis cluster scale statefulset: %v is error: %v", sts.Name, err)
		}

		//检测endpoint pod都ready,则升级扩容集群
		go rco.upgradeRedisCluster(sts, newRedisCluster)
	}

	return nil
}

//检查集群状态
func (rco *RedisClusterOperator) checkAndUpdateRedisClusterStatus (redisCluster *redistype.RedisCluster) error {

	endpoints, err := rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(redisCluster.GetName(), metav1.GetOptions{})

	if err != nil {
		return fmt.Errorf("get redis cluster endpoint is error: %v", err)
	}

	if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		return fmt.Errorf("redis cluster endpoint: %v is blank", endpoints)
	}

	clusterInstanceIp := endpoints.Subsets[0].Addresses[0].IP
	reference := endpoints.Subsets[0].Addresses[0].TargetRef

	clusterStatusIsOK, err := rco.execClusterInfo(redisCluster, clusterInstanceIp, reference.Name, reference.Namespace)
	if err != nil {
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	if !clusterStatusIsOK {
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, "cluster state fail")
		return nil
	}

	_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")
	return err
}

//redis-cli -c -h 10.16.78.90 -p 6379 cluster info
func (rco *RedisClusterOperator) execClusterInfo (redisCluster *redistype.RedisCluster, clusterInstanceIp, clusterInstancePodName, clusterInstancePodNs string) (bool, error) {

	clusterInfoCmd := []string{"/bin/sh", "-c", fmt.Sprintf("redis-cli -c -h %v -p 6379 cluster info", clusterInstanceIp)}
	stdout, stderr, err := rco.ExecToPodThroughAPI(clusterInfoCmd, "", clusterInstancePodName, clusterInstancePodNs, nil)
	glog.Infof("clusterInfoCmd stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)

	if err != nil || stderr != "" {
		err := fmt.Errorf("exec cluster nodes Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", clusterInfoCmd, stdout, stderr, err)
		glog.Errorf(err.Error())
		return false, err
	}

	isOK := strings.Contains(stdout, clusterStatusOK)
	return isOK, nil
}


//升级redis集群
func (rco *RedisClusterOperator) upgradeRedisCluster(sts *appsv1.StatefulSet, redisCluster *redistype.RedisCluster) error {

	oldEndpoints, err := rco.defaultClient.CoreV1().Endpoints(sts.Namespace).Get(sts.GetName(), metav1.GetOptions{})
	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, oldEndpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	if len(oldEndpoints.Subsets) == 0 || len(oldEndpoints.Subsets[0].Addresses) == 0 {
		return fmt.Errorf("RedisCluster: %v/%v endpoints address is empty", redisCluster.Namespace, redisCluster.Name)
	}

	//这里将阻塞
	endpoints, err := rco.checkPodInstanceIsReadyByEndpoint(sts)
	if err != nil {
		//更新状态为Failing
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	//更新状态为Upgrading
	rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterUpgrading, "")

	upgradeType := redisCluster.Spec.UpdateStrategy.Type
	//实例全部ready初始化集群
	switch upgradeType {
	case redistype.AutoReceiveStrategyType:
		err := rco.autoAssignSlotToRedisCluster(endpoints, redisCluster, oldEndpoints)
		if err != nil {
			err := fmt.Errorf("redisCluster upgrade autoAssign slot to RedisCluster is error: %v", err)
			glog.Error(err.Error())
			return err
		}
		return nil
	case redistype.AssignReceiveStrategyType:
		err := rco.manualAssignSlotToRedisCluster(endpoints, redisCluster, oldEndpoints)
		if err != nil {
			err := fmt.Errorf("redisCluster upgrade manualAssign slot to RedisCluster is error: %v", err)
			glog.Error(err.Error())
			return err
		}
		return nil
	default:
		err := fmt.Errorf("redisCluster UpdateStrategy Type only [AutoReceive, AssignReceive] error")
		glog.Error(err.Error())
		return err
	}
}

//自动分配
//TODO 如果有一个节点重启了,从该节点看到的nodeInfo里该节点IP是不对的,可能会存在问题
func (rco *RedisClusterOperator) autoAssignSlotToRedisCluster(endpoints *v1.Endpoints, redisCluster *redistype.RedisCluster, oldEndpoints *v1.Endpoints) error {

	//升级之前的集群IP列表和nodeId列表
	/*var existInstanceIps, existNodeIds, existInstanceNames []string
	clusterConditions := redisCluster.Status.Conditions
	for _, info := range clusterConditions {
		existInstanceIps = append(existInstanceIps, info.InstanceIP)
		existNodeIds = append(existNodeIds, info.NodeId)
		existInstanceNames = append(existInstanceNames, info.Name)
	}

	if len(existInstanceIps) == 0 || len(existNodeIds) == 0 || len(existInstanceNames) == 0 {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		glog.Errorf(err.Error())
		return err
	}

	//升级之后endpoint列表
	var newInstanceIps []string
	for _, newAddr := range endpoints.Subsets[0].Addresses {
		newInstanceIps = append(newInstanceIps, newAddr.IP)
	}

	//需要新加入集群的IP列表
	diffIPs := util.SliceStringDiff(newInstanceIps, existInstanceIps)
	diffIpsLen := len(diffIPs)
	if diffIpsLen == 0 {
		//TODO 状态异常,更新redisCluster状态
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		glog.Errorf(err.Error())
		return err
	}*/

	//TODO 新加入的IP,主从规划

	//首先将新节点加入集群
	//1、查询当前集群信息
	/*nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], endpoints.Subsets[0].Addresses)
	if err != nil {
		//TODO 更新redisCluster状态
		return err
	}
	glog.V(2).Infof("In Before upgrade cluster, nodeInfos: %v\n", nodeInfos)

	for _, info := range nodeInfos {
		info.
	}*/
	//diffIps分组master和slave
	//TODO 根据节点分组
/*	var newMasterInstanceIPs, newSlaveInstanceIPs []string
	for i := 0; i < diffIpsLen; i++ {
		if i < (len(diffIPs) / 2) {
			newMasterInstanceIPs = append(newMasterInstanceIPs, diffIPs[i])
		} else {
			newSlaveInstanceIPs = append(newSlaveInstanceIPs, diffIPs[i])
		}
	}*/
	//获取已有redis集群信息以及当前新起来的pod实例信息
	/*addresses := endpoints.Subsets[0].Addresses
	existInstanceIps, existInstanceNames, existNodeIds, newSlaveInstanceIPs, newMasterInstanceIPs, err := rco.currentRedisClusterInfo(redisCluster, addresses)
	if err != nil {
		return err
	}

	//endpoint里pod信息
	reference := addresses[0].TargetRef

	//2、加入新master
	err = rco.addMasterNodeToRedisCluster(newMasterInstanceIPs, existInstanceIps[0], existInstanceNames[0], reference)
	if err != nil {
		return err
	}

	time.Sleep(2 * time.Second)

	//3、加入新master后查询当前集群节点信息,主要是获取新master节点ID
	nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], redisCluster.Namespace, existInstanceNames[0])
	if err != nil {
		return err
	}

	var newMasterNodeIds []string
	for _, info := range nodeInfos {
		//不是master则继续循环
		if !strings.Contains(info.Flags, masterFlagType) {
			continue
		}

		//当前nodeId在status信息里,说明不是新master,则继续循环
		if util.InSlice(info.NodeId, existNodeIds) {
			continue
		}
		//否则为新ID
		newMasterNodeIds = append(newMasterNodeIds, info.NodeId)
	}

	if len(newMasterNodeIds) != len(newMasterInstanceIPs) {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		//TODO 异常更新集群状态
		glog.Errorf(err.Error())
		return err
	}

	//4、给新master加slave
	err = rco.addSlaveToClusterMaster(newSlaveInstanceIPs, newMasterInstanceIPs, newMasterNodeIds, reference)
	if err != nil {
		return err
	}*/

	existInstanceIps, existInstanceNames, _, err := rco.scaleRedisCluster(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		return err
	}

	//延时2秒,查询开始自动分配卡槽和新master
	time.Sleep(2 * time.Second)

	if endpoints.Subsets == nil {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	reference := endpoints.Subsets[0].Addresses[0].TargetRef

	//执行rebalance命令,将16384个卡槽平均分配到新master节点
	err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIps[0], existInstanceNames[0], reference)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}


	//需要阻塞2s去查询集群node信息,否则加入slave可能查到的是master
	time.Sleep(2 * time.Second)
	nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], redisCluster.Namespace, existInstanceNames[0])
	if err != nil {
		return err
	}

	glog.Infof("cluster upgrade success, node info: %v \n will update redisCluster status as Running", nodeInfos)
	//更新状态为Running
	rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")

	return nil
}

func (rco *RedisClusterOperator) manualAssignSlotToRedisCluster(endpoints *v1.Endpoints, redisCluster *redistype.RedisCluster, oldEndpoints *v1.Endpoints) error {

	//1、加入新master

	//2、给新master加入slave

	//3、reshare分配卡槽

	existInstanceIps, existInstanceNames, newMasterNodeIds, err := rco.scaleRedisCluster(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		return err
	}

	//延时2秒,查询开始自动分配卡槽和新master
	time.Sleep(2 * time.Second)

	if endpoints.Subsets == nil {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	reference := endpoints.Subsets[0].Addresses[0].TargetRef

	//执行reshare命令,将指定nodeId个卡槽分配到新master节点
	err = rco.reshareRedisClusterSlotsToMasterNode(redisCluster.Spec.UpdateStrategy.AssignStrategies, existInstanceIps[0], existInstanceNames[0], newMasterNodeIds, reference)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	//需要阻塞2s去查询集群node信息,否则加入slave可能查到的是master
	time.Sleep(2 * time.Second)
	nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], redisCluster.Namespace, existInstanceNames[0])
	if err != nil {
		return err
	}

	glog.Infof("cluster upgrade success, node info: %v \n will update redisCluster status as Running", nodeInfos)
	//更新状态为Running
	rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")

	return nil
}

func (rco *RedisClusterOperator) createAndInitRedisCluster(sts *appsv1.StatefulSet, redisCluster *redistype.RedisCluster) error {

	//这里将阻塞
	endpoints, err := rco.checkPodInstanceIsReadyByEndpoint(sts)

	if err != nil {
		//更新状态为Failing
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	if endpoints.Subsets == nil {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	//实例全部ready初始化集群

	/*
	TODO
	nodeName数大于等于实例数，则随便分配主从;
	如果nodeName数小于实例数：
	1、nodeName数大于等于实例数/2，则用主从交叉部署；
	2、nodeName数小于实例数/2,...
	*/
/*	var masterInstanceIps, savleInstance []string

	for _, addr := range endpoints.Subsets[0].Addresses {
		masterInstanceIps = append(masterInstanceIps, addr.IP)

	}*/
	//TODO 主从节点选择
	var masterInstanceIps, slaveInstanceIps []string
	count := len(endpoints.Subsets[0].Addresses)
	for i, addr := range endpoints.Subsets[0].Addresses {
		if i < (count / 2) {
			masterInstanceIps = append(masterInstanceIps, addr.IP)
		} else {
			slaveInstanceIps = append(slaveInstanceIps, addr.IP)
		}
	}

	//endpoint里pod信息
	reference := endpoints.Subsets[0].Addresses[0].TargetRef
	//先创建集群
	err = rco.createCluster(masterInstanceIps, reference)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	//查询新建集群的节点信息,主要是master的nodeId信息
	nodeInfos, err := rco.execClusterNodes(masterInstanceIps[0], redisCluster.Namespace, reference.Name)

	if err != nil {
		return err
	}

	if len(nodeInfos) != len(slaveInstanceIps) {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		glog.Errorf(err.Error())
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	var masterInstanceNodeIds []string
	for _, info := range nodeInfos {
		masterInstanceNodeIds = append(masterInstanceNodeIds, info.NodeId)
	}
	
	//给新建集群的master加入slave
	//redis-trib.rb add-node --slave --master-id 1d91acc0 10.168.78.82:6379 10.168.78.83:6379
	err = rco.addSlaveToClusterMaster(slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds, reference)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	//需要阻塞2s去查询集群node信息,否则加入slave可能查到的是master
	time.Sleep(2 * time.Second)
	nodeInfos, err = rco.execClusterNodes(masterInstanceIps[0], redisCluster.Namespace, endpoints.Subsets[0].Addresses[0].TargetRef.Name)

	if err != nil {
		return err
	}

	glog.Infof("cluster create and init success, node info: %v \n will update redisCluster status as Running", nodeInfos)
	//更新状态为Running
	rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")

	return nil
}

//查看节点信息
func (rco *RedisClusterOperator) execClusterNodes (clusterInstanceIp, podNamespace, podName string) ([]*redisNodeInfo, error) {

	clusterNodeInfosCmd := []string{"/bin/sh", "-c", fmt.Sprintf("redis-cli -c -h %v -p 6379 cluster nodes", clusterInstanceIp)}
	stdout, stderr, err := rco.ExecToPodThroughAPI(clusterNodeInfosCmd, "", podName, podNamespace, nil)
	glog.Infof("clusterNodeInfosCmd stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)

	if err != nil || stderr != "" {
		err := fmt.Errorf("exec cluster nodes Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v \n", clusterNodeInfosCmd, stdout, stderr, err)
		glog.Errorf(err.Error())
		return []*redisNodeInfo{}, err
	}

	outNodeInfos := strings.Split(stdout, "\n")

	var nodeInfos []*redisNodeInfo

	//转换node信息
	for _, lineInfo := range outNodeInfos {
		if len(lineInfo) == 0 {
			continue
		}

		info := strings.Fields(lineInfo)

		if len(info) < 8 {
			continue
		}

		//glog.Infof("nodeInfo: %v ", info)
		nodeInfo := &redisNodeInfo{}
		nodeInfo.NodeId = info[0]
		nodeInfo.IpPort = info[1]

		//处理myself,slave或者myself,master
		if strings.Contains(info[2], "myself") {
			nodeInfo.Flags = strings.Split(info[2], ",")[1]
		} else {
			nodeInfo.Flags = info[2]
		}
		
		nodeInfo.Master = info[3]
		nodeInfo.PingSent = info[4]
		nodeInfo.PongRecv = info[5]
		nodeInfo.ConfigEpoch = info[6]
		nodeInfo.LinkState = info[7]

		//slave没有slot,也可能master没分配slot
		if len(info) >= 9 {
			nodeInfo.Slot = strings.Join(info[8:], " ")
		}

		nodeInfos = append(nodeInfos, nodeInfo)
	}

	return nodeInfos, nil
}


//检查实例是否ready
func (rco *RedisClusterOperator) checkPodInstanceIsReadyByEndpoint (sts *appsv1.StatefulSet) (*v1.Endpoints, error) {
	var endpoints *v1.Endpoints
	var err error
	f := wait.ConditionFunc(func() (bool, error) {
		endpoints, err = rco.defaultClient.CoreV1().Endpoints(sts.Namespace).Get(sts.GetName(), metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get redis cluster endpoint is error: %v", err)
		}

		if len(endpoints.Subsets) == 0 {
			return false, nil
		}

		if int32(len(endpoints.Subsets[0].Addresses)) == *sts.Spec.Replicas {
			return true, nil
		}
		return false, nil
	})

	//5s查询一次endpoint,看pod是否全部ready,if return true,则开始初始化集群;
	// 否则继续查,总共6分钟后创建超时,更新redisCluster状态为创建集群超时
	timeout := rco.options.ClusterTimeOut
	err = wait.Poll(5 * time.Second, time.Duration(timeout) * time.Minute, f)
	if err != nil || err == wait.ErrWaitTimeout {
		//创建或者升级集群超时
		glog.Errorf("create or upgrade is error: %v .. update redisCluster status..", err)
		return &v1.Endpoints{}, err
	}

	return endpoints, nil
}


//更新redisCluster对象的状态
//从创建RedisCluster对象开始起协程处理,该对象被删除后,协程退出
//入参：chan deepcopy后的redistype.RedisCluster
func (rco *RedisClusterOperator) updateRedisClusterStatus (redisCluster *redistype.RedisCluster, endpoints *v1.Endpoints, phase redistype.RedisClusterPhase, abnormalReason string) (*redistype.RedisCluster, error) {

	switch phase {
		//代表该CRD刚创建
	case redistype.RedisClusterNone:
		redisCluster.Status = redistype.RedisClusterStatus {
			Phase: redistype.RedisClusterNone,
			Reason: abnormalReason,
		}
		//代表等待redis资源对象创建完毕
	case redistype.RedisClusterCreating:
		/*sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return err
		}*/

		redisCluster.Status = redistype.RedisClusterStatus {
			//Replicas: *sts.Spec.Replicas,
			Replicas: 0,
			Phase: redistype.RedisClusterCreating,
			Reason: abnormalReason,
		}

		//代表已进行初始化操作
	case redistype.RedisClusterRunning:

		var err error
		if endpoints == nil {
			endpoints, err = rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(redisCluster.GetName(), metav1.GetOptions{})
			if err != nil {
				return redisCluster, fmt.Errorf("update redisCluster status -- get redis cluster endpoint is error: %v", err)
			}
		}

		if endpoints.Subsets == nil {
			return redisCluster, fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
		}

		sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return redisCluster, err
		}

		clusterConditions, err := rco.buildRedisClusterStatusConditions(redisCluster, endpoints)
		if err != nil {
			err = fmt.Errorf("redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return redisCluster, err
		}

		redisCluster.Status = redistype.RedisClusterStatus {
			Replicas: *sts.Spec.Replicas,
			Phase: redistype.RedisClusterRunning,
			Reason: abnormalReason,
			Conditions: clusterConditions,
		}

		//代表着实例不一致(用户修改实例，operator发现实例不一致，更新statefulset，更新状态)
	case redistype.RedisClusterScaling:
		sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return redisCluster, err
		}
		redisCluster.Status.Replicas = *sts.Spec.Replicas
		redisCluster.Status.Phase = redistype.RedisClusterScaling
		redisCluster.Status.Reason = abnormalReason

		//代表着升级中
	case redistype.RedisClusterUpgrading:
		sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return redisCluster, err
		}
		redisCluster.Status.Replicas = *sts.Spec.Replicas
		redisCluster.Status.Phase = redistype.RedisClusterUpgrading
		redisCluster.Status.Reason = abnormalReason

		//代表着某异常故障
	case redistype.RedisClusterFailed:
		sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return redisCluster, err
		}
		redisCluster.Status.Replicas = *sts.Spec.Replicas
		redisCluster.Status.Phase = redistype.RedisClusterFailed
		redisCluster.Status.Reason = abnormalReason
	}

	//TODO 1.10以下版本的k8s是否对子资源支持？
	//updatedRedisCluster, err := rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).UpdateStatus(redisCluster)
	newRedisCluster, err := rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Update(redisCluster)
	if err != nil {
		err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
		glog.Errorf(err.Error())
		return newRedisCluster, err
	}

	return newRedisCluster, nil
}

func (rco *RedisClusterOperator) buildRedisClusterStatusConditions (redisCluster *redistype.RedisCluster, endpoints *v1.Endpoints) ([]redistype.RedisClusterCondition, error){

	if endpoints.Subsets == nil {
		return []redistype.RedisClusterCondition{}, fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	address := endpoints.Subsets[0].Addresses
	nodeInfos, err := rco.execClusterNodes(address[0].IP, address[0].TargetRef.Namespace, address[0].TargetRef.Name)

	if err != nil {
		//rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return []redistype.RedisClusterCondition{}, err
	}

	//查k8s node信息
	nodeList, err := rco.defaultClient.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		//rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return []redistype.RedisClusterCondition{}, err
	}

	//key nodeName value nodeIP
	nodeInfoMap := make(map[string]string, len(nodeList.Items))
	for _, node := range nodeList.Items {
		for _, addr := range node.Status.Addresses {
			if addr.Type == v1.NodeInternalIP {
				nodeInfoMap[node.Name] = addr.Address
			}
		}
	}

	//pod信息,key podIP , value type:instanceInfo
	instanceInfoMap := make(map[string]instanceInfo, len(address))
	for _, addr := range address {
		info := instanceInfo {}
		info.HostName = addr.TargetRef.Name
		info.NodeName = *addr.NodeName
		info.DomainName = fmt.Sprintf("%v.%v.%v.svc.cluster.local", addr.TargetRef.Name, endpoints.Name, addr.TargetRef.Namespace)
		info.InstanceIP = addr.IP
		info.HostIP = nodeInfoMap[info.NodeName]
		instanceInfoMap[addr.IP] = info
	}

	var conditions []redistype.RedisClusterCondition

	for _, info := range nodeInfos {

		condition := redistype.RedisClusterCondition {}
		condition.NodeId = info.NodeId
		condition.InstanceIP = strings.Split(info.IpPort, ":")[0]
		condition.Status = v1.ConditionTrue
		if masterFlagType == info.Flags {
			condition.Type = redistype.MasterConditionType
		} else if slaveFlagType == info.Flags {
			condition.Type = redistype.SlaveConditionType
		} else {
			condition.Status = v1.ConditionFalse
		}

		//glog.Infof("nodeInfo: %v ", info)
		condition.Slots = info.Slot

		condition.MasterNodeId = info.Master

		instanceInfo := instanceInfoMap[condition.InstanceIP]
		condition.Name = instanceInfo.HostName
		condition.Hostname = instanceInfo.HostName
		condition.DomainName = instanceInfo.DomainName
		condition.HostIP = instanceInfo.HostIP
		condition.Reason = ""
		now := metav1.NewTime(time.Now())
		condition.LastTransitionTime = now
		condition.Message = ""

		conditions = append(conditions, condition)
	}

	return conditions, nil
}

func (rco *RedisClusterOperator) buildRedisClusterService (namespace, name string) *v1.Service {
	/*
		# service提供域名，映射到pod IP
	kind: Service
	apiVersion: v1
	metadata:
	  labels:
	    # RedisCluster Metadata.name
	    app: {RedisCluster Metadata.name}
	  name: {RedisCluster Metadata.name}
	  namespace: {RedisCluster Metadata.namespace}
	spec:
	  clusterIP: None
	  ports:
	  - name: {RedisCluster Metadata.name}-6379
	    port: 6379
	    targetPort: 6379
	  selector:
	    app: {RedisCluster Metadata.name}
	*/
		//build service
		return &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels: map[string]string{
					"app": name,
				},
			},
			Spec: v1.ServiceSpec{
				ClusterIP: v1.ClusterIPNone,
				Ports: []v1.ServicePort{
					{
						Name: name,
						Port: redisServicePort6379,
						TargetPort: intstr.IntOrString{
							Type:   intstr.Int,
							IntVal: int32(redisServicePort6379),
						},
					},
				},

				Selector: map[string]string{
					"app": name,
				},
			},
		}
}


func (rco *RedisClusterOperator) buildRedisClusterStatefulset (namespace, name string, redisCluster *redistype.RedisCluster) *appsv1.StatefulSet {
	/*
		apiVersion: apps/v1
	kind: StatefulSet
	metadata:
	labels:
	app: {RedisCluster.metadata.name}
	name: {RedisCluster.metadata.name}
	namespace: {RedisCluster.metadata.namespace}
	spec:
	# 实例数{可变}
	replicas: {RedisCluster.spec.replicas}
	revisionHistoryLimit: 10
	selector:
	matchLabels:
	  app: {RedisCluster.metadata.name}
	serviceName: {RedisCluster.metadata.name}
	template:
	metadata:
	  creationTimestamp: null
	  labels:
		app: {RedisCluster.metadata.name}
	spec:
	  containers:
	  - command:
		- /bin/redis-server
		- /nfs/redis/$(DOMAINNAME)/$(NAMESPACE)/$(POD_NAME)/config/redis.conf
		image: {RedisCluster.spec.repository:RedisCluster.spec.version}
		imagePullPolicy: Always
		env:
		- name: DOMAINNAME
		  value: {RedisCluster.spec.pod.env.DOMAINNAME如cluster.local}
		- name: POD_NAME
		  valueFrom:
			fieldRef:
			  apiVersion: v1
			  fieldPath: metadata.name
		- name: NAMESPACE
		  valueFrom:
			fieldRef:
			  apiVersion: v1
			  fieldPath: metadata.namespace
		name: {RedisCluster.metadata.name}
		ports:
		- containerPort: 6379
		  protocol: TCP
		- containerPort: 16379
		  protocol: TCP
		# 健康检查，执行redis-cli -h {hostname} ping
		livenessProbe:
		  exec:
			command:
			- sh
			- /data/health-check.sh
		  initialDelaySeconds: 20
		  periodSeconds: 10
		  successThreshold: 1
		  failureThreshold: 3
		  timeoutSeconds: 10
		readinessProbe:
		  failureThreshold: 3
		  exec:
			command:
			- sh
			- /data/health-check.sh
		  initialDelaySeconds: 20
		  periodSeconds: 10
		  successThreshold: 1
		  timeoutSeconds: 10
		resources: {RedisCluster.spec.resource}
		volumeMounts:
		# 挂载data、log、config目录到共享存储，{集群域名、namespace可变}
		- mountPath: /nfs/redis/{RedisCluster.spec.pod.env.DOMAINNAME如cluster.local}/{RedisCluster.metadata.namespace}
		  name: redis-data
	  dnsPolicy: ClusterFirst
	  initContainers:
	  - command:
		- sh
		- /init.sh
		env:
		- name: DOMAINNAME
		  value: {RedisCluster.spec.pod.env.DOMAINNAME如cluster.local}
		- name: MAXMEMORY
		  value: {2/3*RedisCluster.spec.resource.limits.memory}gb
		- name: PODIP
		  valueFrom:
			fieldRef:
			  fieldPath: status.podIP
		- name: NAMESPACE
		  valueFrom:
			fieldRef:
			  apiVersion: v1
			  fieldPath: metadata.namespace
		# init container镜像，执行目录的创建，redis.conf的占位符填充等初始化操作
		image: {RedisCluster.spec.repository/RedisCluster.spec.pod.initImage}
		imagePullPolicy: Always
		name: init
		resources: {}
		volumeMounts:
		- mountPath: /config/redis.conf
		  name: redis-config
		  subPath: redis.conf
		# 挂载data、log、config目录到共享存储，{集群域名、namespace可变}
		- mountPath: /nfs/redis/{RedisCluster.spec.pod.env.DOMAINNAME如cluster.local}/{RedisCluster.metadata.namespace}
		  name: redis-data
	  restartPolicy: Always
	  # 节点选择，{可变}
	  nodeSelector:
		kubernetes.io/hostname: ly1f-yanfa-20180607-docker-vm-3.novalocal
	  volumes:
	  - configMap:
		  defaultMode: 420
		  name: {RedisCluster.metadata.name}-config
		name: redis-config
	  - name: redis-data
		persistentVolumeClaim:
		  claimName: {RedisCluster.spec.pod.volumes.persistentVolumeClaimName}
	*/
	//create statefulset
	revisionHistoryLimit := int32(10)
	defaultMode := int32(420)
	containerEnv := []v1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &v1.EnvVarSource{
				FieldRef: &v1.ObjectFieldSelector{
					APIVersion: "v1",
					FieldPath:  "metadata.name",
				},
			},
		},
		{
			Name:  "NAMESPACE",
			Value: namespace,
		},
		{
			Name: "PODIP",
			ValueFrom: &v1.EnvVarSource{
				FieldRef: &v1.ObjectFieldSelector{
					FieldPath: "status.podIP",
				},
			},
		},
	}

	//取环境变量里的DOMAINNAME
	var k8sClusterDomainName string
	for _, env := range redisCluster.Spec.Pod[0].Env {
		if env.Name == "DOMAINNAME" {
			k8sClusterDomainName = env.Value
			break
		}
	}

	containerEnv = append(containerEnv, redisCluster.Spec.Pod[0].Env...)
	recSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			//维修状态
			Annotations: map[string]string{
				pauseKey: strconv.FormatBool(redisCluster.Spec.Pause),
			},
			Labels: map[string]string{
				"app": name,
			},
		},
		Spec: appsv1.StatefulSetSpec{
			UpdateStrategy: redisCluster.Spec.Pod[0].UpdateStrategy,
			Replicas:             redisCluster.Spec.Replicas,
			RevisionHistoryLimit: &revisionHistoryLimit,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			//serviceName和redisCluster一样
			ServiceName: redisCluster.Name,
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							//Command: []string{"/bin/redis-server", "/nfs/redis/$(DOMAINNAME)/$(NAMESPACE)/$(POD_NAME)/config/redis.conf"},
							Command:         []string{"/bin/redis-server", fmt.Sprintf("/nfs/redis/%v/%v/$(POD_NAME)/config/redis.conf", k8sClusterDomainName, namespace)},
							Image:           redisCluster.Spec.Repository + redisCluster.Spec.Pod[0].MiddlewareImage,
							ImagePullPolicy: v1.PullAlways,
							Env:             containerEnv,
							Name:            redisCluster.Name,
							Ports: []v1.ContainerPort{
								{
									Protocol:      v1.ProtocolTCP,
									ContainerPort: redisServicePort16379,
								},
								{
									Protocol:      v1.ProtocolTCP,
									ContainerPort: redisServicePort6379,
								},
							},
							LivenessProbe: &v1.Probe{
								Handler: v1.Handler{
									Exec: &v1.ExecAction{
										Command: []string{"/bin/sh", "-c", "redis-cli -h ${POD_NAME} ping"},
									},
								},
								InitialDelaySeconds: int32(5),
								TimeoutSeconds:      int32(5),
								PeriodSeconds:       int32(5),
								SuccessThreshold:    int32(1),
								FailureThreshold:    int32(3),
							},
							ReadinessProbe: &v1.Probe{
								Handler: v1.Handler{
									Exec: &v1.ExecAction{
										Command: []string{"/bin/sh", "-c", "redis-cli -h ${POD_NAME} ping"},
									},
								},
								InitialDelaySeconds: int32(5),
								TimeoutSeconds:      int32(5),
								PeriodSeconds:       int32(5),
								SuccessThreshold:    int32(1),
								FailureThreshold:    int32(3),
							},
							Resources: redisCluster.Spec.Pod[0].Resources,
							VolumeMounts: []v1.VolumeMount{
								{
									Name: "redis-data",
									//MountPath: "/nfs/redis/$DOMAINNAME如cluster.local}/{RedisCluster.metadata.namespace}",
									MountPath: fmt.Sprintf("/nfs/redis/%v/%v", k8sClusterDomainName, namespace),
								},
							},
						},
					},
					DNSPolicy: v1.DNSClusterFirst,
					InitContainers: []v1.Container{
						{
							Command: []string{"/bin/sh", "-c", "sh /init.sh"},
							//Image: "{RedisCluster.spec.repository/RedisCluster.spec.pod.initImage}",
							Image:           redisCluster.Spec.Repository + redisCluster.Spec.Pod[0].InitImage,
							ImagePullPolicy: v1.PullAlways,
							Env:             containerEnv,
							Name:            redisCluster.Name + "-init",
							Ports: []v1.ContainerPort{
								{
									Protocol:      v1.ProtocolTCP,
									ContainerPort: redisServicePort16379,
								},
								{
									Protocol:      v1.ProtocolTCP,
									ContainerPort: redisServicePort6379,
								},
							},
							VolumeMounts: []v1.VolumeMount{
								{
									Name: "redis-data",
									//MountPath: "/nfs/redis/{RedisCluster.spec.pod.env.DOMAINNAME如cluster.local}/{RedisCluster.metadata.namespace}",
									MountPath: fmt.Sprintf("/nfs/redis/%v/%v", k8sClusterDomainName, namespace),
								},
								{
									Name:      "redis-config",
									MountPath: "/config/redis.conf",
									SubPath:   "redis.conf",
								},
							},
						},
					},
					RestartPolicy: v1.RestartPolicyAlways,
					//TODO modify
					NodeSelector:  map[string]string{
						"kubernetes.io/hostname" : "ly1f-yanfa-20180607-docker-vm-3.novalocal",
					},
					Volumes: []v1.Volume{
						{
							Name: "redis-config",
							VolumeSource: v1.VolumeSource{
								ConfigMap: &v1.ConfigMapVolumeSource{
									DefaultMode: &defaultMode,
									LocalObjectReference: v1.LocalObjectReference{
										Name: redisCluster.Name + "-config",
									},
								},
							},
						},
						{
							Name: "redis-data",
							VolumeSource: v1.VolumeSource{
								PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
									ClaimName: redisCluster.Spec.Pod[0].Volumes.PersistentVolumeClaimName,
								},
							},
						},
					},
				},
			},
		},
	}
	return recSts
}

//新建redis集群创建master
func (rco *RedisClusterOperator) createCluster (masterInstanceIps []string, reference *v1.ObjectReference) error {
	var buf bytes.Buffer
	buf.WriteString("echo yes | redis-trib.rb create ")
	for i := 0; i < len(masterInstanceIps); i++ {
		buf.WriteString(masterInstanceIps[i])
		buf.WriteString(":6379")
		buf.WriteString(" ")
	}

	//createMasterCommand := fmt.Sprintf("echo yes | redis-trib.rb create %v:6379 %v:6379 %v:6379", masterInstance[0], masterInstance[1], masterInstance[2])
	createMasterCommand := buf.String()
	glog.Infof("create createMasterCommand is: %v ", createMasterCommand)

	commandMaster := []string{"/bin/sh", "-c", createMasterCommand}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandMaster, "", reference.Name, reference.Namespace, nil)

	if err != nil || stderr != "" {
		err := fmt.Errorf("exec create Master Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", commandMaster, stdout, stderr, err)
		glog.Errorf(err.Error())
		return err
	}

	glog.Infof("create createMaster stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)
	return nil
}

//给redis集群加入新slave
func (rco *RedisClusterOperator) addSlaveToClusterMaster (slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds []string, reference *v1.ObjectReference) error {

	if len(slaveInstanceIps) != len(masterInstanceIps) || len(masterInstanceIps) != len(masterInstanceNodeIds) {
		return fmt.Errorf("add Slave To ClusterMaster check len error, slaveInstanceIps: %v, masterInstanceIps: %v, masterInstanceNodeIds: %v", slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds)
	}

	//给新建集群的master加入slave
	//redis-trib.rb add-node --slave --master-id 1d91acc0 10.168.78.82:6379 10.168.78.83:6379
	for i := 0; i < len(slaveInstanceIps); i++ {
		addSlaveCommand := fmt.Sprintf("redis-trib.rb add-node --slave --master-id %v %v:6379 %v:6379", masterInstanceNodeIds[i], slaveInstanceIps[i], masterInstanceIps[i])
		commandSlave := []string{"/bin/sh", "-c", addSlaveCommand}
		glog.Infof("add slave cmd: %v", addSlaveCommand)
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandSlave, "", reference.Name, reference.Namespace, nil)
		if err != nil || stderr != "" {
			err := fmt.Errorf("redisCluster: %v/%v -- add new slave to cluster is error -- stdout: %v\n -- stderr: %v\n -- error: %v", reference.Namespace, reference.Name, stdout, stderr, err)
			glog.Errorf(err.Error())
			return err
		}
		glog.V(4).Infof("redisCluster: %v/%v add new slave to cluster: -- \nstdout: %v", reference.Namespace, reference.Name, stdout)
	}

	return nil
}

//添加master节点
func (rco *RedisClusterOperator) addMasterNodeToRedisCluster (newMasterInstanceIPs []string , existInstanceIp, existInstanceOneName string, reference *v1.ObjectReference) error {

	for i := 0; i < len(newMasterInstanceIPs); i++ {
		addMasterCmd := fmt.Sprintf(" redis-trib.rb add-node %v:6379 %v:6379 ", newMasterInstanceIPs[i] ,existInstanceIp)
		glog.Infof("redisCluster: %v/%v -- add new master to cluster cmd: %v", reference.Namespace, reference.Name, addMasterCmd)
		commandMaster := []string{"/bin/sh", "-c", addMasterCmd}
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandMaster, "", existInstanceOneName, reference.Namespace, nil)

		if err != nil || stderr != "" {
			err := fmt.Errorf("redisCluster: %v/%v -- add new master to cluster is error -- stdout: %v\n -- stderr: %v\n -- error: %v", reference.Namespace, reference.Name, stdout, stderr, err)
			glog.Errorf(err.Error())
			return err
		}

		glog.V(4).Infof("redisCluster: %v/%v add new master to cluster: -- \nstdout: %v", reference.Namespace, reference.Name, stdout)
	}

	return nil
}

func (rco *RedisClusterOperator) rebalanceRedisClusterSlotsToMasterNode (existInstanceIp, existInstanceName string, reference *v1.ObjectReference) error {
	rebalanceCmd := fmt.Sprintf(" redis-trib.rb  rebalance --use-empty-masters %v:6379 ", existInstanceIp)
	commandRebalance := []string{"/bin/sh", "-c", rebalanceCmd}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandRebalance, "", existInstanceName, reference.Namespace, nil)

	if err != nil || stderr != "" {
		err := fmt.Errorf("redisCluster: %v/%v -- rebalanceCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", reference.Namespace, reference.Name, commandRebalance, stdout, stderr, err)
		glog.Errorf(err.Error())
		return err
	}

	glog.V(4).Infof("redisCluster: %v/%v rebalanceCmd: %v \n -- \nstdout: %v", reference.Namespace, reference.Name, commandRebalance, stdout)

	return nil
}



//reshare手动调整卡槽
//redis-trib.rb reshard --from c49a3b06ad93638037d56855ff702787ad16e3ea --to 174ad1122349c33c475dcbd54489ea847ad8474f --slots 100 --yes 10.168.78.119:6379
func (rco *RedisClusterOperator) reshareRedisClusterSlotsToMasterNode (assignStrategies []redistype.SlotsAssignStrategy, existInstanceIp, existInstanceName string, newMasterNodeIds []string, reference *v1.ObjectReference) error {

	for i, nodeId := range newMasterNodeIds {

		f := wait.ConditionFunc(func() (bool, error) {

			err := rco.execCheckRedisCluster(existInstanceIp, existInstanceName, reference)

			if err != nil {
				return false, fmt.Errorf("check redis cluster is error: %v", err)
			}

			return true, nil
		})

		//5s check一次,看集群各节点node状态是否同步,if return true,则开始分配卡槽;
		// 否则继续检查,总共6分钟后超时,更新redisCluster状态为创建集群超时
		timeout := rco.options.ClusterTimeOut
		err := wait.Poll(5 * time.Second, time.Duration(timeout) * time.Minute, f)
		if err != nil || err == wait.ErrWaitTimeout {
			//升级集群超时
			err := fmt.Errorf("check redisCluster: %v/%v status error: %v, upgrade failed", reference.Namespace, reference.Name, err)
			glog.Errorf(err.Error())
			return err
		}

		reshareCmd := fmt.Sprintf(" redis-trib.rb reshard --from %v --to %v --slots %v --yes %v:6379 ", assignStrategies[i].FromReplicas, nodeId, *assignStrategies[i].Slots, existInstanceIp)
		commandReshare := []string{"/bin/sh", "-c", reshareCmd}
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandReshare, "", existInstanceName, reference.Namespace, nil)

		if err != nil || stderr != "" {
			err := fmt.Errorf("redisCluster: %v/%v -- reshareCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v \n", reference.Namespace, reference.Name, commandReshare, stdout, stderr, err)
			glog.Errorf(err.Error())
			return err
		}
		glog.V(4).Infof("redisCluster: %v/%v reshareCmd: %v \n -- \nstdout: %v", reference.Namespace, reference.Name, commandReshare, stdout)
	}

	return nil
}

func (rco *RedisClusterOperator) execCheckRedisCluster (existInstanceIp, existInstanceName string, reference *v1.ObjectReference) error {
	checkCmd := fmt.Sprintf(" redis-trib.rb check %v:6379 ", existInstanceIp)
	commandCheck := []string{"/bin/sh", "-c", checkCmd}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandCheck, "", existInstanceName, reference.Namespace, nil)

	if err != nil || stderr != "" || strings.Contains(stdout, "[ERR]") {
		err := fmt.Errorf("redisCluster: %v/%v -- checkCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", reference.Namespace, reference.Name, commandCheck, stdout, stderr, err)
		glog.Errorf(err.Error())
		return err
	}
	glog.V(4).Infof("redisCluster: %v/%v checkCmd: %v \n -- \nstdout: %v", reference.Namespace, reference.Name, commandCheck, stdout)
	return nil
}

func (rco *RedisClusterOperator) currentRedisClusterInfo (redisCluster *redistype.RedisCluster, addresses []v1.EndpointAddress, oldEndpoints *v1.Endpoints) ([]string, []string, []string, []string, []string, error) {
	//升级之前的集群IP列表和nodeId列表
	var existInstanceIps, existNodeIds, existInstanceNames []string
/*	clusterConditions := redisCluster.Status.Conditions
	for _, info := range clusterConditions {
		existInstanceIps = append(existInstanceIps, info.InstanceIP)
		existNodeIds = append(existNodeIds, info.NodeId)
		existInstanceNames = append(existInstanceNames, info.Name)
	}*/
	oldAddresses := oldEndpoints.Subsets[0].Addresses
	if len(addresses) == 0 || len(oldAddresses) == 0 {
		return nil, nil, nil, nil, nil, fmt.Errorf("endpoints.Subsets.addresses is empty, maybe statefulset %v/%v all replicas not ready", redisCluster.Namespace, redisCluster.Name)
	}

	for _, addr := range oldAddresses {
		existInstanceNames = append(existInstanceNames, addr.TargetRef.Name)
		existInstanceIps = append(existInstanceIps, addr.IP)
	}

	nodeInfos, err := rco.execClusterNodes(oldAddresses[0].IP, oldAddresses[0].TargetRef.Namespace, oldAddresses[0].TargetRef.Name)

	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	for _, info := range nodeInfos {
		existNodeIds = append(existNodeIds, info.NodeId)
	}

	if len(existInstanceIps) == 0 || len(existNodeIds) == 0 || len(existInstanceNames) == 0 {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		glog.Errorf(err.Error())
		return nil, nil, nil, nil, nil, err
	}

	//升级之后endpoint列表
	var newInstanceIps []string
	for _, newAddr := range addresses {
		newInstanceIps = append(newInstanceIps, newAddr.IP)
	}

	//需要新加入集群的IP列表
	diffIPs := util.SliceStringDiff(newInstanceIps, existInstanceIps)
	diffIpsLen := len(diffIPs)
	if diffIpsLen == 0 {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		glog.Errorf(err.Error())
		return nil, nil, nil, nil, nil, err
	}

	//TODO 新加入的IP,主从规划

	//首先将新节点加入集群
	//1、查询当前集群信息
	/*nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], endpoints.Subsets[0].Addresses)
	if err != nil {
		//TODO 更新redisCluster状态
		return err
	}
	glog.V(2).Infof("In Before upgrade cluster, nodeInfos: %v\n", nodeInfos)

	for _, info := range nodeInfos {
		info.
	}*/
	//diffIps分组master和slave
	//TODO 根据节点分组
	var newMasterInstanceIPs, newSlaveInstanceIPs []string
	for i := 0; i < diffIpsLen; i++ {
		if i < (len(diffIPs) / 2) {
			newMasterInstanceIPs = append(newMasterInstanceIPs, diffIPs[i])
		} else {
			newSlaveInstanceIPs = append(newSlaveInstanceIPs, diffIPs[i])
		}
	}

	return existInstanceIps, existInstanceNames, existNodeIds, newSlaveInstanceIPs, newMasterInstanceIPs, nil
}

//升级集群时加master和slave
func (rco *RedisClusterOperator) scaleRedisCluster (redisCluster *redistype.RedisCluster, endpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) ([]string, []string, []string, error) {

	if endpoints.Subsets == nil {
		return nil, nil, nil, fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	//获取已有redis集群信息以及当前新起来的pod实例信息
	addresses := endpoints.Subsets[0].Addresses
	existInstanceIps, existInstanceNames, existNodeIds, newSlaveInstanceIPs, newMasterInstanceIPs, err := rco.currentRedisClusterInfo(redisCluster, addresses, oldEndpoints)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return nil, nil, nil, err
	}

	//endpoint里pod信息
	reference := addresses[0].TargetRef

	//2、加入新master
	err = rco.addMasterNodeToRedisCluster(newMasterInstanceIPs, existInstanceIps[0], existInstanceNames[0], reference)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return nil, nil, nil, err
	}

	time.Sleep(2 * time.Second)

	//3、加入新master后查询当前集群节点信息,主要是获取新master节点ID
	nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], redisCluster.Namespace, existInstanceNames[0])
	if err != nil {
		return nil, nil, nil, err
	}

	var newMasterNodeIds []string
	for _, info := range nodeInfos {
		//不是master则继续循环
		if !strings.Contains(info.Flags, masterFlagType) {
			continue
		}

		//当前nodeId在status信息里,说明不是新master,则继续循环
		if util.InSlice(info.NodeId, existNodeIds) {
			continue
		}
		//否则为新ID
		newMasterNodeIds = append(newMasterNodeIds, info.NodeId)
	}

	if len(newMasterNodeIds) != len(newMasterInstanceIPs) {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		glog.Errorf(err.Error())
		return nil, nil, nil, err
	}

	//4、给新master加slave
	err = rco.addSlaveToClusterMaster(newSlaveInstanceIPs, newMasterInstanceIPs, newMasterNodeIds, reference)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return nil, nil, nil, err
	}

	return existInstanceIps, existInstanceNames, newMasterNodeIds, nil
}