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
	"harmonycloud.cn/middleware-operator-manager/cmd/operator-manager/app/options"
	redistype "harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	customclient "harmonycloud.cn/middleware-operator-manager/pkg/clients/clientset/versioned"
	custominfomer "harmonycloud.cn/middleware-operator-manager/pkg/clients/informers/externalversions/redis/v1alpha1"
	redisclusterLister "harmonycloud.cn/middleware-operator-manager/pkg/clients/listers/redis/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubernetes/pkg/controller"
	"reflect"
	"runtime/debug"
	"time"
)

const (
	// maxRetries is the number of times a RedisCluster will be retried before it is dropped out of the queue.
	// With the current rate-limiter in use (5ms*2^(maxRetries-1)) the following numbers represent the times
	// a RedisCluster is going to be requeued:
	//
	// 5ms, 10ms, 20ms, 40ms, 80ms, 160ms, 320ms, 640ms, 1.3s, 2.6s, 5.1s, 10.2s, 20.4s, 41s, 82s
	maxRetries            = 15
	redisServicePort6379  = 6379
	redisExporterPort9105 = 9105
	redisServicePort16379 = 16379
	pauseKey              = "pause.middleware.harmonycloud.cn"
	defaultPipeline       = "10"
	finalizersForeGround  = "Foreground"
	redisContainerName    = "redis-cluster"
)

type handleClusterType int

const (
	createCluster handleClusterType = iota
	upgradeCluster
	dropCluster
)

// controllerKind contains the schema.GroupVersionKind for this controller type.
var controllerKind = redistype.SchemeGroupVersion.WithKind("RedisCluster")

// RedisClusterOperator is responsible for synchronizing RedisCluster objects stored
// in the system with actual running replica sets and pods.
type RedisClusterOperator struct {
	//extensionCRClient *extensionsclient.Clientset
	customCRDClient customclient.Interface
	defaultClient   clientset.Interface
	kubeConfig      *rest.Config

	options *options.OperatorManagerServer

	eventRecorder record.EventRecorder
	// To allow injection of syncRedisCluster for testing.
	syncHandler func(dKey string) error
	// used for unit testing
	enqueueRedisCluster func(redisCluster *redistype.RedisCluster)

	redisClusterInformer cache.SharedIndexInformer

	redisClusterLister redisclusterLister.RedisClusterLister

	// dListerSynced returns true if the redisCluster store has been synced at least once.
	// Added as a member to the struct to allow injection for testing.
	redisClusterListerSynced cache.InformerSynced

	// stsLister is able to list/get stateful sets from a shared informer's store
	stsLister appslisters.StatefulSetLister
	// stsListerSynced returns true if the stateful set shared informer has synced at least once
	stsListerSynced cache.InformerSynced

	// redisCluster that need to be synced
	queue workqueue.RateLimitingInterface
}

// NewRedisClusterOperator creates a new RedisClusterOperator.
func NewRedisClusterOperator(redisInformer custominfomer.RedisClusterInformer, stsInformer appsinformers.StatefulSetInformer, kubeClient clientset.Interface, customCRDClient customclient.Interface, kubeConfig *rest.Config, options options.OperatorManagerServer) (*RedisClusterOperator, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	// TODO: remove the wrapper when every clients have moved to use the clientset.
	eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(kubeClient.CoreV1().RESTClient()).Events("")})

	rco := &RedisClusterOperator{
		options:       &options,
		kubeConfig:    kubeConfig,
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

	stsInformer.Informer().AddEventHandler(
		cache.ResourceEventHandlerFuncs{
			AddFunc: rco.addStatefulSet,
			UpdateFunc: func(old, cur interface{}) {
				oldSts := old.(*appsv1.StatefulSet)
				curSts := cur.(*appsv1.StatefulSet)
				if oldSts.Status.Replicas != curSts.Status.Replicas {
					glog.V(4).Infof("Observed updated replica count for StatefulSet: %v, %d->%d", curSts.Name, oldSts.Status.Replicas, curSts.Status.Replicas)
				}
				rco.updateStatefulSet(oldSts, curSts)
			},
			DeleteFunc: rco.deleteStatefulSet,
		},
	)
	rco.stsLister = stsInformer.Lister()
	rco.stsListerSynced = stsInformer.Informer().HasSynced

	return rco, nil
}

// Run begins watching and syncing.
func (rco *RedisClusterOperator) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer rco.queue.ShutDown()

	glog.Infof("Starting rediscluster operator")
	defer glog.Infof("Shutting down rediscluster operator")

	if !controller.WaitForCacheSync("rediscluster", stopCh, rco.redisClusterListerSynced, rco.stsListerSynced) {
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

	if curR.ResourceVersion == oldR.ResourceVersion {
		glog.V(4).Infof("same %s/%s ResourceVersion: %s", curR.Namespace, curR.Name, curR.ResourceVersion)
		//return
	}

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

// addStatefulSet enqueues the rediscluster that manages a StatefulSet when the StatefulSet is created.
func (rco *RedisClusterOperator) addStatefulSet(obj interface{}) {
	sts := obj.(*appsv1.StatefulSet)

	if sts.DeletionTimestamp != nil {
		// On a restart of the controller manager, it's possible for an object to
		// show up in a state that is already pending deletion.
		rco.deleteStatefulSet(sts)
		return
	}

	// Get a list of all matching RedisClusters and sync
	// them to see if anyone wants to adopt it.
	rcs := rco.getRedisClustersForStatefulSet(sts)
	if rcs == nil {
		return
	}
	glog.V(4).Infof("StatefulSet %s added.", sts.Name)
	rco.enqueueRedisCluster(rcs)
}

// deleteStatefulSet enqueues the rediscluster that manages a statefulSet when
// the statefulSet is deleted. obj could be an *apps.statefulSet, or
// a DeletionFinalStateUnknown marker item.
func (rco *RedisClusterOperator) deleteStatefulSet(obj interface{}) {
	sts, ok := obj.(*appsv1.StatefulSet)

	// When a delete is dropped, the relist will notice a pod in the store not
	// in the list, leading to the insertion of a tombstone object which contains
	// the deleted key/value. Note that this value might be stale. If the statefulSet
	// changed labels the new rediscluster will not be woken up till the periodic resync.
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Couldn't get object from tombstone %#v", obj))
			return
		}
		sts, ok = tombstone.Obj.(*appsv1.StatefulSet)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("Tombstone contained object that is not a statefulSet %#v", obj))
			return
		}
	}

	rcs := rco.getRedisClustersForStatefulSet(sts)
	if rcs == nil {
		return
	}
	glog.V(4).Infof("StatefulSet %s deleted.", sts.Name)
	rco.enqueueRedisCluster(rcs)
}

// updateStatefulSet figures out what rediscluster(s) manage a StatefulSet when the StatefulSet
// is updated and wake them up. If the anything of the StatefulSets have changed, we need to
// awaken both the old and new StatefulSets. old and cur must be *apps.StatefulSet
// types.
func (rco *RedisClusterOperator) updateStatefulSet(old, cur interface{}) {
	curSts := cur.(*appsv1.StatefulSet)
	oldSts := old.(*appsv1.StatefulSet)
	if curSts.ResourceVersion == oldSts.ResourceVersion {
		// Periodic resync will send update events for all known Stateful set.
		// Two different versions of the same Stateful Set will always have different RVs.
		return
	}

	// Otherwise, it's an orphan. If anything changed, sync matching controllers
	// to see if anyone wants to adopt it now.
	statusChanged := !reflect.DeepEqual(curSts.Status, oldSts.Status)
	if statusChanged {
		rcs := rco.getRedisClustersForStatefulSet(curSts)
		if rcs == nil {
			return
		}
		glog.V(4).Infof("StatefulSet %s updated.", curSts.Name)
		rco.enqueueRedisCluster(rcs)
	}
}

// getRedisClustersForStatefulSet returns RedisClusters that potentially
// match a StatefulSet annotation.
func (rco *RedisClusterOperator) getRedisClustersForStatefulSet(sts *appsv1.StatefulSet) *redistype.RedisCluster {
	rediscluster, err := rco.redisClusterLister.GetRedisClusterForStatefulSet(sts)
	if err != nil {
		return nil
	}
	return rediscluster
}

// getStatefulSetForRedisCluster It returns the StatefulSets that this RedisCluster should manage.
func (rco *RedisClusterOperator) getStatefulSetForRedisCluster(rc *redistype.RedisCluster) (*appsv1.StatefulSet, error) {
	// List all StatefulSets to find those we own but that no longer match our annotation.
	stsList, err := rco.stsLister.StatefulSets(rc.Namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}

	for _, sts := range stsList {
		// If a statefulSet with a redis.middleware.hc.cn=redisclusters-{redisClusterName} annotation, it should be redis work queue sync
		if len(sts.Annotations) == 0 || sts.Annotations[redistype.MiddlewareRedisTypeKey] != (redistype.MiddlewareRedisClustersPrefix+rc.Name) {
			continue
		}

		if sts.Namespace == rc.Namespace && sts.Name == rc.Name {
			return sts, nil
		}
	}

	glog.V(4).Infof("could not find statefulSet for RedisCluster %s in namespace %s with annotation: %v", rc.Name, rc.Namespace, redistype.MiddlewareRedisTypeKey)

	return nil, nil
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

	// Done marks item as done processing, and if it has been marked as dirty again
	// while it was being processed, it will be re-added to the queue for
	// re-processing.
	/*defer rco.queue.Done(key)

	err := rco.syncHandler(key.(string))
	rco.handleErr(err, key)*/

	go rco.syncHandler(key.(string))

	return true
}

func (rco *RedisClusterOperator) handleErr(err error, key interface{}) {
	if err == nil {
		//Forget表示key已完成重试。 无论是失败还是成功，这只清除`rateLimiter`，之后必须调用Done(key)。
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
func (rco *RedisClusterOperator) syncRedisCluster(key string) (err error) {
	defer rco.queue.Done(key)

	startTime := time.Now()
	glog.V(4).Infof("Started syncing redisCluster %q (%v)", key, startTime)
	defer func() {
		glog.V(4).Infof("Finished syncing redisCluster %q (%v)", key, time.Since(startTime))
		rco.handleErr(err, key)
	}()

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		return err
	}

	// 必须要先声明defer，否则不能捕获到panic异常
	defer func() {
		if err := recover(); err != nil {
			// 这里的err其实就是panic传入的内容
			glog.Errorf("recover panic error: %v", err)
			//打印堆栈
			debug.PrintStack()
		}
	}()

	err = rco.sync(namespace, name)

	return err
}

func (rco *RedisClusterOperator) sync(namespace, name string) error {

	rc, err := rco.redisClusterLister.RedisClusters(namespace).Get(name)

	// Deep-copy otherwise we are mutating our cache.
	// TODO: Deep-copy only when needed.
	redisCluster := rc.DeepCopy()

	glog.V(4).Infof("Started syncing redisCluster: %v/%v ResourceVersion: %v", namespace, name, redisCluster.ResourceVersion)

	//_, err = rco.redisClusterInformer.GetIndexer().
	if errors.IsNotFound(err) {
		glog.V(2).Infof("RedisCluster %v/%v has been deleted", namespace, name)
		return nil
	}
	if err != nil {
		return err
	}

	//1、取到redisCluster对象，开始判断是否是第一次创建，用redisCluster.namespaces和redisCluster.name绑定statefulset

	//2、如果不存在则为初始化操作，执行创建service、statefulset的操作，

	//3、判断service-->endpoints实例是否都ready,如果ready则开始初始化redis集群的操作,否则阻塞等待

	//3.1 初始化成功后,更新redisCluster对象中的status

	//4、如果存在statefulset则为升级操作或者删除操作，执行修改statefulset的操作，

	//5、判断service-->endpoints新实例是否都ready,如果ready则开始升级redis集群的操作,否则阻塞等待

	//5.1 升级成功后,更新redisCluster对象中的status

	//6、启动一个协程一直探测集群的健康状态,实时更新到redisCluster对象的status中
	var handleFlag handleClusterType
	existSts, err := rco.getStatefulSetForRedisCluster(redisCluster)

	if err != nil {
		glog.Errorf("get cluster statefulset: %v/%v is error: %v", namespace, name, err)
		return err
	} else if existSts == nil {
		glog.V(2).Infof("RedisCluster %v/%v has been create firstly, will create and init redis cluster", namespace, name)
		handleFlag = createCluster
	} else {
		if *redisCluster.Spec.Replicas <= *existSts.Spec.Replicas {
			//可能是升级操作也可能是强制同步;升级操作：实例和当前sts实例不一样

			//redisCluster里实例数小于当前sts实例数,只检查状态不进行缩容,TODO 缩容需求
			return rco.checkAndUpdateRedisClusterStatus(redisCluster)
			//cr对象中实例数大于existSts实例数且如果不是删除,就是升级集群
		} else if existSts.DeletionTimestamp == nil {
			//redisCluster实例数大于当前sts实例数,则为扩容
			handleFlag = upgradeCluster
		} else {
			// 正在进行删除集群(cr、statefulset、svc)的操作,什么都不做
			return nil
		}
	}

	switch handleFlag {
	//初始化集群
	case createCluster:
		//更新状态为Creating
		newRedisCluster, err := rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterCreating, "")
		if err != nil {
			return err
		}

		//create service
		recService := rco.buildRedisClusterService(newRedisCluster, namespace, name)
		_, err = rco.defaultClient.CoreV1().Services(namespace).Create(recService)

		if errors.IsAlreadyExists(err) {
			glog.Warningf("redis cluster create service: %v/%v Is Already Exists", namespace, name)
		} else if err != nil {
			return fmt.Errorf("init redis cluster create service: %v/%v is error: %v", namespace, name, err)
		}

		recSts := rco.buildRedisClusterStatefulset(namespace, name, newRedisCluster)
		//create statefulset
		sts, err := rco.defaultClient.AppsV1().StatefulSets(namespace).Create(recSts)

		if err != nil {
			return fmt.Errorf("init redis cluster create statefulset: %v is error: %v", sts.Name, err)
		}

		//检测endpoint pod都ready,则创建和初始化集群
		err = rco.createAndInitRedisCluster(newRedisCluster)
	//升级集群
	case upgradeCluster:
		//策略类型错误
		updateType := redisCluster.Spec.UpdateStrategy.Type
		if updateType != redistype.AssignReceiveStrategyType && updateType != redistype.AutoReceiveStrategyType {
			err := fmt.Errorf("upgrade redis cluster: %v/%v UpdateStrategy Type error: %v", redisCluster.Namespace, redisCluster.Name, updateType)
			glog.Error(err.Error())
			rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterRunning, err.Error())
			return nil
		}

		//卡槽分配策略手动,但分配详情有误
		strategiesLen := int32(len(redisCluster.Spec.UpdateStrategy.AssignStrategies))
		scaleLen := *redisCluster.Spec.Replicas - *existSts.Spec.Replicas
		if (updateType == redistype.AssignReceiveStrategyType) && strategiesLen != (scaleLen/2) {
			err := fmt.Errorf("upgrade redis cluster: %v/%v slots AssignStrategies error", redisCluster.Namespace, redisCluster.Name)
			glog.Error(err.Error())
			rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterRunning, err.Error())
			return nil
		}

		//更新状态为Scaling
		newRedisCluster, err := rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterScaling, "")
		if err != nil {
			return err
		}

		oldEndpoints, err := rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})
		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, oldEndpoints, redistype.RedisClusterFailed, err.Error())
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
		err = rco.upgradeRedisCluster(newRedisCluster, oldEndpoints)
	//删除集群
	case dropCluster:
		//更新状态为Deleting
		newRedisCluster, err := rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterDeleting, "")
		if err != nil {
			return err
		}
		err = rco.dropRedisCluster(newRedisCluster)
	default:
		glog.Error("RedisCluster crd Error.")
	}

	return err
}
