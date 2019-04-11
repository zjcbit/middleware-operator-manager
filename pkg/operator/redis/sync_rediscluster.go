package redis

import (
	"bytes"
	"fmt"
	"github.com/golang/glog"
	redistype "harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	"harmonycloud.cn/middleware-operator-manager/util"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"sort"
	"strconv"
	"strings"
	"time"
)

//redis-cli -c -h 10.16.78.90 -p 6379 cluster info
//clusterInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) execClusterInfo(clusterInstanceIp, podName, namespace string) (bool, bool, error) {

	clusterInfoCmd := []string{"/bin/sh", "-c", fmt.Sprintf("redis-cli -c -h %v -p 6379 cluster info", clusterInstanceIp)}
	stdout, stderr, err := rco.ExecToPodThroughAPI(clusterInfoCmd, "", podName, namespace, nil)
	glog.Infof("clusterInfoCmd stdout: %v -- stderr: %v -- error: %v ", stdout, stderr, err)

	if err != nil || stderr != "" {
		err := fmt.Errorf("exec cluster nodes Command: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", clusterInfoCmd, stdout, stderr, err)
		glog.Errorf(err.Error())
		return false, false, err
	}

	isOK := strings.Contains(stdout, clusterStatusOK)
	isOnlyKnownSelf := strings.Contains(stdout, clusterKnownNodesOnlySelf)
	return isOK, isOnlyKnownSelf, nil
}

//升级redis集群
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) upgradeRedisCluster(redisCluster *redistype.RedisCluster, oldEndpoints *v1.Endpoints) error {

	if len(oldEndpoints.Subsets) == 0 || len(oldEndpoints.Subsets[0].Addresses) == 0 {
		return fmt.Errorf("RedisCluster: %v/%v endpoints address is empty", redisCluster.Namespace, redisCluster.Name)
	}

	//这里将阻塞
	endpoints, err := rco.checkPodInstanceIsReadyByEndpoint(redisCluster)
	if err != nil {
		//更新状态为Failing
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	//更新状态为Upgrading
	newRedisCluster, err := rco.updateRedisClusterStatus(redisCluster, nil, redistype.RedisClusterUpgrading, "")
	if err != nil {
		return err
	}

	upgradeType := newRedisCluster.Spec.UpdateStrategy.Type
	//实例全部ready初始化集群
	switch upgradeType {
	case redistype.AutoReceiveStrategyType:
		err := rco.autoAssignSlotToRedisCluster(endpoints, newRedisCluster, oldEndpoints)
		if err != nil {
			err := fmt.Errorf("redisCluster upgrade autoAssign slot to RedisCluster is error: %v", err)
			glog.Error(err.Error())
			return err
		}
		return nil
	case redistype.AssignReceiveStrategyType:
		err := rco.manualAssignSlotToRedisCluster(endpoints, newRedisCluster, oldEndpoints)
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
//负责调用自动分配逻辑和更新状态
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
//TODO 如果有一个节点重启了,从该节点看到的nodeInfo里该节点IP是不对的,可能会存在问题
func (rco *RedisClusterOperator) autoAssignSlotToRedisCluster(endpoints *v1.Endpoints, redisCluster *redistype.RedisCluster, oldEndpoints *v1.Endpoints) error {

	existInstanceIps, _, err := rco.scaleRedisCluster(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		//更新状态为Running以及更新当前error信息
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	if endpoints.Subsets == nil {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	reference := endpoints.Subsets[0].Addresses[0].TargetRef

	//执行rebalance命令,将16384个卡槽平均分配到新master节点
	err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIps[0], reference.Name, reference.Namespace, redisCluster.Spec.UpdateStrategy.Pipeline)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	_, err = rco.execClusterNodes(existInstanceIps[0], reference.Namespace, reference.Name)
	if err != nil {
		return err
	}

	glog.Infof("cluster upgrade success, will update redisCluster status as Running")
	//更新状态为Running
	rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")

	return nil
}

//手动分配
//负责调用手动分配逻辑和更新状态
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
func (rco *RedisClusterOperator) manualAssignSlotToRedisCluster(endpoints *v1.Endpoints, redisCluster *redistype.RedisCluster, oldEndpoints *v1.Endpoints) error {

	//1、加入新master

	//2、给新master加入slave

	//3、reshare分配卡槽

	existInstanceIps, newMasterNodeIds, err := rco.scaleRedisCluster(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		//更新状态为Running以及更新当前error信息
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	if endpoints.Subsets == nil {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	reference := endpoints.Subsets[0].Addresses[0].TargetRef

	//执行reshare命令,将指定nodeId个卡槽分配到新master节点
	err = rco.reshareRedisClusterSlotsToMasterNode(redisCluster.Spec.UpdateStrategy, existInstanceIps[0], reference.Name, reference.Namespace, newMasterNodeIds)
	if err != nil {
		//更新状态为Running
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		return err
	}

	_, err = rco.execClusterNodes(existInstanceIps[0], redisCluster.Namespace, reference.Name)
	if err != nil {
		return err
	}

	glog.Infof("cluster upgrade success, will update redisCluster status as Running")
	//更新状态为Running
	rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")

	return nil
}

//创建和初始化集群,包括新建master,加slave和更新redisCluster对象status
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) createAndInitRedisCluster(redisCluster *redistype.RedisCluster) error {

	//这里将阻塞
	endpoints, err := rco.checkPodInstanceIsReadyByEndpoint(redisCluster)

	if err != nil {
		//更新状态为Failing
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	if endpoints.Subsets == nil {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	willAssignIPAddresses, err := rco.assignMasterSlaveIPAddress(redisCluster, endpoints, nil)
	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	//实例全部ready初始化集群
	masterInstanceIps, slaveInstanceIps, err := rco.assignMasterSlaveIP(willAssignIPAddresses)
	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	//endpoint里pod信息
	reference := endpoints.Subsets[0].Addresses[0].TargetRef
	//先创建集群
	err = rco.createCluster(masterInstanceIps, reference)
	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	//查询新建集群的节点信息,主要是master的nodeId信息
	nodeInfos, err := rco.execClusterNodes(masterInstanceIps[0], reference.Namespace, reference.Name)

	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	if len(nodeInfos) != len(slaveInstanceIps) {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		glog.Errorf(err.Error())
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	// 根据masterInstanceIp拿nodeId,下标对应
	var masterInstanceNodeIds []string
	for _, masterInstanceIp := range masterInstanceIps {
		for _, info := range nodeInfos {
			if masterInstanceIp+":6379" == info.IpPort {
				masterInstanceNodeIds = append(masterInstanceNodeIds, info.NodeId)
				break
			}
		}
	}

	//给新建集群的master加入slave
	//redis-trib.rb add-node --slave --master-id 1d91acc0 10.168.78.82:6379 10.168.78.83:6379
	err = rco.addSlaveToClusterMaster(slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds, reference.Namespace, reference.Name)
	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	//会先去查询集群状态,检查成功之前会阻塞,直到检查超时,检查目的:redis实例之间node.conf不一致可能查到刚加入的slave为master的情况
	_, err = rco.execClusterNodes(masterInstanceIps[0], redisCluster.Namespace, reference.Name)

	if err != nil {
		//更新状态为Failed
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	glog.Infof("cluster create and init success")
	//更新状态为Running
	rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")

	return nil
}

//查看节点信息
//clusterInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) execClusterNodes(clusterInstanceIp, namespace, podName string) ([]*redisNodeInfo, error) {

	//将阻塞-->>检查集群状态是否ok
	err := rco.waitRedisStatusOK(clusterInstanceIp, podName, namespace)
	if err != nil {
		return nil, err
	}

	clusterNodeInfosCmd := []string{"/bin/sh", "-c", fmt.Sprintf("redis-cli -c -h %v -p 6379 cluster nodes", clusterInstanceIp)}
	stdout, stderr, err := rco.ExecToPodThroughAPI(clusterNodeInfosCmd, "", podName, namespace, nil)
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

//这里将阻塞-->>检查实例是否ready
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) checkPodInstanceIsReadyByEndpoint(redisCluster *redistype.RedisCluster) (*v1.Endpoints, error) {
	var endpoints *v1.Endpoints
	var err error
	f := wait.ConditionFunc(func() (bool, error) {
		endpoints, err = rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get redis cluster endpoint is error: %v", err)
		}

		if len(endpoints.Subsets) == 0 {
			return false, nil
		}

		if int32(len(endpoints.Subsets[0].Addresses)) == *redisCluster.Spec.Replicas {
			return true, nil
		}
		return false, nil
	})

	//2s查询一次endpoint,看pod是否全部ready,if return true,则开始初始化集群;
	// 否则继续查,总共time.Duration(timeout)分钟后创建超时,更新redisCluster状态
	timeout := rco.options.ClusterTimeOut
	err = wait.Poll(2*time.Second, time.Duration(timeout)*time.Minute, f)
	if err != nil || err == wait.ErrWaitTimeout {
		//创建或者升级集群超时
		glog.Errorf("create or upgrade is error: %v .. update redisCluster status..", err)
		return &v1.Endpoints{}, err
	}

	return endpoints, nil
}

//更新redisCluster对象的状态
//redisCluster：redisCluster对象
//endpoints：endpoint信息
//phase：集群状态
//abnormalReason：异常原因
func (rco *RedisClusterOperator) updateRedisClusterStatus(redisCluster *redistype.RedisCluster, endpoints *v1.Endpoints, phase redistype.RedisClusterPhase, abnormalReason string) (*redistype.RedisCluster, error) {

	var tempStatus redistype.RedisClusterStatus
	switch phase {
	//代表该CRD刚创建
	case redistype.RedisClusterNone:
		tempStatus = redistype.RedisClusterStatus{
			Phase:      redistype.RedisClusterNone,
			Reason:     abnormalReason,
			Conditions: redisCluster.Status.Conditions,
		}
		//代表等待redis资源对象创建完毕
	case redistype.RedisClusterCreating:
		/*sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return err
		}*/

		tempStatus = redistype.RedisClusterStatus{
			//Replicas: *sts.Spec.Replicas,
			Replicas: 0,
			Phase:    redistype.RedisClusterCreating,
			Reason:   abnormalReason,
			//Conditions: redisCluster.Status.Conditions,
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

		tempStatus = redistype.RedisClusterStatus{
			Replicas:   *sts.Spec.Replicas,
			Phase:      redistype.RedisClusterRunning,
			Reason:     abnormalReason,
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
		tempStatus.Replicas = *sts.Spec.Replicas
		tempStatus.Phase = redistype.RedisClusterScaling
		tempStatus.Reason = abnormalReason
		tempStatus.Conditions = redisCluster.Status.Conditions

		//代表着升级中
	case redistype.RedisClusterUpgrading:
		sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return redisCluster, err
		}
		tempStatus.Replicas = *sts.Spec.Replicas
		tempStatus.Phase = redistype.RedisClusterUpgrading
		tempStatus.Reason = abnormalReason
		tempStatus.Conditions = redisCluster.Status.Conditions

		//代表着某异常故障
	case redistype.RedisClusterFailed:
		sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return redisCluster, err
		}
		tempStatus.Replicas = *sts.Spec.Replicas
		tempStatus.Phase = redistype.RedisClusterFailed
		tempStatus.Reason = abnormalReason
		tempStatus.Conditions = redisCluster.Status.Conditions
		//代表着某异常故障
	case redistype.RedisClusterDeleting:
		sts, err := rco.defaultClient.AppsV1().StatefulSets(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})

		if err != nil {
			err = fmt.Errorf("update redisCluster:%v/%v status is error: %v", redisCluster.Namespace, redisCluster.Name, err)
			glog.Errorf(err.Error())
			return redisCluster, err
		}
		tempStatus.Replicas = *sts.Spec.Replicas
		tempStatus.Phase = redistype.RedisClusterDeleting
		tempStatus.Reason = abnormalReason
		tempStatus.Conditions = redisCluster.Status.Conditions
	}

	// sort
	sort.SliceStable(tempStatus.Conditions, func(i, j int) bool {
		name1 := tempStatus.Conditions[i].Name
		name2 := tempStatus.Conditions[j].Name
		return name1 < name2
	})

	//判断新旧clusterConditions是否变化(除LastTransitionTime字段),变化才更新状态,避免更新频繁(其实只是LastTransitionTime变化了)
	if !util.DeepEqualRedisClusterStatus(tempStatus, redisCluster.Status) {
		redisCluster.Status = tempStatus

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

	return redisCluster, nil
}

//构造redisCluster的状态Condition信息
//redisCluster：redisCluster对象
//endpoints：endpoint信息
func (rco *RedisClusterOperator) buildRedisClusterStatusConditions(redisCluster *redistype.RedisCluster, endpoints *v1.Endpoints) ([]redistype.RedisClusterCondition, error) {

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
		info := instanceInfo{}
		info.HostName = addr.TargetRef.Name
		info.NodeName = *addr.NodeName
		info.DomainName = fmt.Sprintf("%v.%v.%v.svc.cluster.local", addr.TargetRef.Name, endpoints.Name, addr.TargetRef.Namespace)
		info.InstanceIP = addr.IP
		info.HostIP = nodeInfoMap[info.NodeName]
		instanceInfoMap[addr.IP+":6379"] = info
	}

	var conditions []redistype.RedisClusterCondition

	for _, info := range nodeInfos {

		condition := redistype.RedisClusterCondition{}
		condition.NodeId = info.NodeId
		condition.Instance = info.IpPort
		condition.Status = v1.ConditionTrue
		if masterFlagType == info.Flags {
			condition.Type = redistype.MasterConditionType
		} else if slaveFlagType == info.Flags {
			condition.Type = redistype.SlaveConditionType
		} else {
			condition.Status = v1.ConditionFalse
		}

		condition.Slots = info.Slot

		condition.MasterNodeId = info.Master

		instanceInfo := instanceInfoMap[condition.Instance]
		condition.Name = instanceInfo.HostName
		condition.Hostname = instanceInfo.NodeName
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

//构造Service
//namespace：service所在ns
//name：service name
func (rco *RedisClusterOperator) buildRedisClusterService(redisCluster *redistype.RedisCluster, namespace, name string) *v1.Service {
	flagTrue := true
	//build service
	return &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				redistype.MiddlewareRedisTypeKey: redistype.MiddlewareRedisClustersPrefix + name,
			},
			Labels: map[string]string{
				"app": name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         controllerKind.Group + "/" + controllerKind.Version,
					Kind:               controllerKind.Kind,
					Name:               name,
					UID:                redisCluster.UID,
					BlockOwnerDeletion: &flagTrue,
					Controller:         &flagTrue,
				},
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

//构造Statefulset
//namespace：Statefulset所在ns
//name：Statefulset name
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) buildRedisClusterStatefulset(namespace, name string, redisCluster *redistype.RedisCluster) *appsv1.StatefulSet {
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

	glog.Warningf("-----------redisCluster: %#v--", redisCluster)
	containerEnv = append(containerEnv, redisCluster.Spec.Pod[0].Env...)
	flagTrue := true
	recSts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			//维修状态
			Annotations: map[string]string{
				redistype.MiddlewareRedisTypeKey: redistype.MiddlewareRedisClustersPrefix + name,
				pauseKey: strconv.FormatBool(redisCluster.Spec.Pause),
			},
			Labels: map[string]string{
				"app": name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         controllerKind.Group + "/" + controllerKind.Version,
					Kind:               controllerKind.Kind,
					Name:               redisCluster.Name,
					UID:                redisCluster.UID,
					BlockOwnerDeletion: &flagTrue,
					Controller:         &flagTrue,
				},
			},
		},
		Spec: appsv1.StatefulSetSpec{
			UpdateStrategy:       redisCluster.Spec.Pod[0].UpdateStrategy,
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
					/*NodeSelector: map[string]string{
						"kubernetes.io/hostname": "10.10.103.61-slave",
					},*/
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
//masterInstanceIps：master节点pod ip
//reference：endpoint里address[0]信息,主要获取podName和namespace用于执行redis-trib.rb命令
func (rco *RedisClusterOperator) createCluster(masterInstanceIps []string, reference *v1.ObjectReference) error {
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
//slaveInstanceIps：需要加为slave的pod实例ip列表
//masterInstanceIps：需要加slave的master pod实例ip列表
//masterInstanceNodeIds：需要加slave的master nodeId列表
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) addSlaveToClusterMaster(slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds []string, namespace, podName string) error {

	if len(slaveInstanceIps) != len(masterInstanceIps) || len(masterInstanceIps) != len(masterInstanceNodeIds) {
		return fmt.Errorf("add Slave To ClusterMaster check len error, slaveInstanceIps: %v, masterInstanceIps: %v, masterInstanceNodeIds: %v", slaveInstanceIps, masterInstanceIps, masterInstanceNodeIds)
	}

	//给新建集群的master加入slave
	//redis-trib.rb add-node --slave --master-id 1d91acc0 10.168.78.82:6379 10.168.78.83:6379
	for i := 0; i < len(slaveInstanceIps); i++ {

		//将阻塞-->>检查集群状态是否ok
		err := rco.waitRedisStatusOK(masterInstanceIps[0], podName, namespace)
		if err != nil {
			return err
		}

		addSlaveCommand := fmt.Sprintf("redis-trib.rb add-node --slave --master-id %v %v:6379 %v:6379", masterInstanceNodeIds[i], slaveInstanceIps[i], masterInstanceIps[i])
		commandSlave := []string{"/bin/sh", "-c", addSlaveCommand}
		glog.Infof("add slave cmd: %v", addSlaveCommand)
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandSlave, "", podName, namespace, nil)
		if err != nil || stderr != "" {
			err := fmt.Errorf("redisCluster: %v/%v -- add new slave to cluster is error -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, stdout, stderr, err)
			glog.Errorf(err.Error())
			return err
		}
		glog.V(4).Infof("redisCluster: %v/%v add new slave to cluster: -- \nstdout: %v", namespace, podName, stdout)
	}

	return nil
}

//添加master节点
//newMasterInstanceIPs:新master pod实例ip
//existInstanceIp：redis集群中已存在的实例ip
//podName: pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在ns
func (rco *RedisClusterOperator) addMasterNodeToRedisCluster(newMasterInstanceIPs []string, existInstanceIp, podName, namespace string) error {

	for i := 0; i < len(newMasterInstanceIPs); i++ {

		//将阻塞-->>检查集群状态是否ok
		err := rco.waitRedisStatusOK(existInstanceIp, podName, namespace)
		if err != nil {
			return err
		}

		addMasterCmd := fmt.Sprintf(" redis-trib.rb add-node %v:6379 %v:6379 ", newMasterInstanceIPs[i], existInstanceIp)
		glog.Infof("redisCluster: %v/%v -- add new master to cluster cmd: %v", namespace, podName, addMasterCmd)
		commandMaster := []string{"/bin/sh", "-c", addMasterCmd}
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandMaster, "", podName, namespace, nil)

		if err != nil || stderr != "" {
			err := fmt.Errorf("redisCluster: %v/%v -- add new master to cluster is error -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, stdout, stderr, err)
			glog.Errorf(err.Error())
			return err
		}

		glog.V(4).Infof("redisCluster: %v/%v add new master to cluster: -- \nstdout: %v", namespace, podName, stdout)
	}

	return nil
}

// rebalance自动分配卡槽
// redis-trib.rb  rebalance --use-empty-masters 1.1.1.1:6379
//existInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) rebalanceRedisClusterSlotsToMasterNode(existInstanceIp, podName, namespace, crdPipeline string) error {

	//将阻塞-->>检查集群状态是否ok
	err := rco.waitRedisStatusOK(existInstanceIp, podName, namespace)
	if err != nil {
		return err
	}

	pipeline := defaultPipeline
	if crdPipeline != "" {
		pipeline = crdPipeline
	}

	rebalanceCmd := fmt.Sprintf(" redis-trib.rb  rebalance --use-empty-masters --pipeline %v %v:6379 ", pipeline, existInstanceIp)
	commandRebalance := []string{"/bin/sh", "-c", rebalanceCmd}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandRebalance, "", podName, namespace, nil)

	if err != nil || stderr != "" {
		err := fmt.Errorf("redisCluster: %v/%v -- rebalanceCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, commandRebalance, stdout, stderr, err)
		glog.Errorf(err.Error())
		return err
	}

	glog.V(4).Infof("redisCluster: %v/%v rebalanceCmd: %v \n -- \nstdout: %v", namespace, podName, commandRebalance, stdout)

	return nil
}

//reshare手动调整卡槽
//redis-trib.rb reshard --from c49a3b06ad93638037d56855ff702787ad16e3ea --to 174ad1122349c33c475dcbd54489ea847ad8474f --slots 100 --yes 10.168.78.119:6379
//assignStrategies：reshare卡槽分配策略
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) reshareRedisClusterSlotsToMasterNode(assignStrategies redistype.RedisClusterUpdateStrategy, existInstanceIp, podName, namespace string, newMasterNodeIds []string) error {

	strategies := assignStrategies.AssignStrategies
	crdPipeline := assignStrategies.Pipeline
	pipeline := defaultPipeline
	if crdPipeline != "" {
		pipeline = crdPipeline
	}

	for i, nodeId := range newMasterNodeIds {

		//将阻塞-->>检查集群状态是否ok
		err := rco.waitRedisStatusOK(existInstanceIp, podName, namespace)
		if err != nil {
			return err
		}

		reshareCmd := fmt.Sprintf(" redis-trib.rb reshard --from %v --to %v --slots %v --pipeline %v --yes %v:6379 ", strategies[i].FromReplicas, nodeId, *strategies[i].Slots, pipeline, existInstanceIp)
		commandReshare := []string{"/bin/sh", "-c", reshareCmd}
		stdout, stderr, err := rco.ExecToPodThroughAPI(commandReshare, "", podName, namespace, nil)

		if err != nil || stderr != "" {
			err := fmt.Errorf("redisCluster: %v/%v -- reshareCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v \n", namespace, podName, commandReshare, stdout, stderr, err)
			glog.Errorf(err.Error())
			return err
		}
		glog.V(4).Infof("redisCluster: %v/%v reshareCmd: %v \n -- \nstdout: %v", namespace, podName, commandReshare, stdout)
	}

	return nil
}

//执行redis-trib check 1.1.1.1:6379检查集群状态是否ok,这里将阻塞,最多rco.options.ClusterTimeOut分钟
//等待检查redis集群状态ok
//existInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) waitRedisStatusOK(existInstanceIp, podName, namespace string) error {
	f := wait.ConditionFunc(func() (bool, error) {

		err := rco.execCheckRedisCluster(existInstanceIp, podName, namespace)

		if err != nil {
			return false, fmt.Errorf("check redis cluster is error: %v", err)
		}

		return true, nil
	})

	timeout := rco.options.ClusterTimeOut
	//2s check一次,看集群各节点node状态是否同步,if return true,开始执行后续逻辑;
	// 否则继续检查,总共time.Duration(timeout)分钟后超时,更新redisCluster状态
	err := wait.Poll(2*time.Second, time.Duration(timeout)*time.Minute, f)
	if err != nil || err == wait.ErrWaitTimeout {
		//检查集群状态一直不成功-->>超时
		err := fmt.Errorf("check redisCluster: %v/%v status error: %v", namespace, podName, err)
		glog.Errorf(err.Error())
		return err
	}
	return nil
}

// master、slave IP分配,直到和故障之前的分配方式一样
//addresses：需要分配master、slave IP的address信息
//expectedConnector：期望的master、slave IP绑定关系
func (rco *RedisClusterOperator) waitExpectMasterSlaveIPAssign(addresses []v1.EndpointAddress, expectedConnector map[string]string) (masterInstanceIPs []string, slaveInstanceIPs []string, err error) {
	f := wait.ConditionFunc(func() (bool, error) {

		masterInstanceIPs, slaveInstanceIPs, err = rco.assignMasterSlaveIP(addresses)

		if err != nil {
			return false, fmt.Errorf("assign master slave IP is error: %v", err)
		}

		for i, masterIp := range masterInstanceIPs {
			if slaveIp, ok := expectedConnector[masterIp]; ok {
				if slaveIp != slaveInstanceIPs[i] {
					//不符合期望,继续分配
					return false, nil
				}
			}
		}

		//符合期望执行后续逻辑
		return true, nil
	})

	timeout := rco.options.ClusterTimeOut
	//1s check一次,看IP分配是否符合期望,if return true,开始执行后续逻辑;
	// 否则继续检查,总共time.Duration(timeout)分钟后超时
	err = wait.Poll(1*time.Second, time.Duration(timeout)*time.Minute, f)
	if err != nil || err == wait.ErrWaitTimeout {
		//IP分配一直不符合期望-->>超时
		err := fmt.Errorf("assign master slave IP error: %v", err)
		glog.Errorf(err.Error())
		return nil, nil, err
	}
	return masterInstanceIPs, slaveInstanceIPs, nil
}

//检查redis集群状态
//existInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) execCheckRedisCluster(existInstanceIp, podName, namespace string) error {
	checkCmd := fmt.Sprintf(" redis-trib.rb check %v:6379 ", existInstanceIp)
	commandCheck := []string{"/bin/sh", "-c", checkCmd}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandCheck, "", podName, namespace, nil)

	if err != nil || stderr != "" || strings.Contains(stdout, "[ERR]") {
		err := fmt.Errorf("redisClusterInstance: %v/%v -- checkCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, commandCheck, stdout, stderr, err)
		glog.Errorf(err.Error())
		return err
	}
	glog.V(4).Infof("redisClusterInstance: %v/%v checkCmd: %v \n -- \nstdout: %v", namespace, podName, commandCheck, stdout)
	return nil
}

//获取当前redisCluster实例信息,和新加的节点master和slave信息
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
func (rco *RedisClusterOperator) currentRedisClusterInfo(redisCluster *redistype.RedisCluster, endpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) ([]string, error) {
	//升级之前的集群IP列表和nodeId列表
	var existInstanceIps []string
	oldAddresses := oldEndpoints.Subsets[0].Addresses
	addresses := endpoints.Subsets[0].Addresses
	if len(addresses) == 0 || len(oldAddresses) == 0 {
		return nil, fmt.Errorf("endpoints.Subsets.addresses is empty, maybe statefulset %v/%v all replicas not ready", redisCluster.Namespace, redisCluster.Name)
	}

	for _, addr := range oldAddresses {
		//existInstanceNames = append(existInstanceNames, addr.TargetRef.Name)
		existInstanceIps = append(existInstanceIps, addr.IP)
	}

	return existInstanceIps, nil
}

//升级集群时加master和slave
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
func (rco *RedisClusterOperator) scaleRedisCluster(redisCluster *redistype.RedisCluster, endpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) ([]string, []string, error) {

	if endpoints.Subsets == nil {
		return nil, nil, fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	//获取已有redis集群信息
	addresses := endpoints.Subsets[0].Addresses
	existInstanceIps, err := rco.currentRedisClusterInfo(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		return nil, nil, err
	}

	//TODO 升级时IP分配需要参考升级前的,防止分配结果不符合master、slave IP分配要求
	willAssignIPAddresses, err := rco.assignMasterSlaveIPAddress(redisCluster, endpoints, oldEndpoints)
	if err != nil {
		return nil, nil, err
	}
	//获取当前新起来的pod实例信息
	newMasterInstanceIPs, newSlaveInstanceIPs, err := rco.assignMasterSlaveIP(willAssignIPAddresses)
	if err != nil {
		return nil, nil, err
	}

	//endpoint里pod信息
	reference := addresses[0].TargetRef

	//2、加入新master
	err = rco.addMasterNodeToRedisCluster(newMasterInstanceIPs, existInstanceIps[0], reference.Name, reference.Namespace)
	if err != nil {
		return nil, nil, err
	}

	//3、加入新master后查询当前集群节点信息,主要是获取新master节点ID
	nodeInfos, err := rco.execClusterNodes(existInstanceIps[0], reference.Namespace, reference.Name)
	if err != nil {
		return nil, nil, err
	}

	// 根据newMasterInstanceIPs拿nodeId,下标对应
	var newMasterNodeIds []string
	for _, masterInstanceIp := range newMasterInstanceIPs {
		for _, info := range nodeInfos {
			if strings.Contains(info.IpPort, masterInstanceIp) {
				newMasterNodeIds = append(newMasterNodeIds, info.NodeId)
				break
			}
		}
	}

	if len(newMasterNodeIds) != len(newMasterInstanceIPs) {
		err := fmt.Errorf("current redis cluster: %v/%v status is error", redisCluster.Namespace, redisCluster.Name)
		//更新状态为Running
		//rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
		glog.Errorf(err.Error())
		return nil, nil, err
	}

	//4、给新master加slave
	err = rco.addSlaveToClusterMaster(newSlaveInstanceIPs, newMasterInstanceIPs, newMasterNodeIds, reference.Namespace, reference.Name)
	if err != nil {
		return nil, nil, err
	}

	return existInstanceIps, newMasterNodeIds, nil
}

//级联删除statefulset、service、cr
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) dropRedisCluster(redisCluster *redistype.RedisCluster) error {
	foreGround := metav1.DeletePropagationForeground
	options := &metav1.DeleteOptions{
		PropagationPolicy: &foreGround,
	}
	//删除cr级联删除statefulset、service
	err := rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Delete(redisCluster.Name, options)

	if err != nil {
		glog.Errorf("Drop RedisCluster: %v/%v error: %v", redisCluster.Namespace, redisCluster.Name, err)
		return err
	}

	return err
}

//分配master和slave ip,符合:1、两个主节点尽可能不在同一节点上;2、master和对应slave尽可能不在同一宿主机上
//addresses：需要划分master、slave IP的address集合
func (rco *RedisClusterOperator) assignMasterSlaveIP(addresses []v1.EndpointAddress) ([]string, []string, error) {

	//key: nodeName value: ips
	nodeIPs := make(map[string][]v1.EndpointAddress)
	for _, addr := range addresses {
		nodeIPs[*addr.NodeName] = append(nodeIPs[*addr.NodeName], addr)
	}

	//将nodeIPs map的key排序,保证多次遍历map时输出顺序一致
	sortedKeys := make([]string, 0)
	for k := range nodeIPs {
		sortedKeys = append(sortedKeys, k)
	}

	// sort 'string' key in increasing order
	sort.Strings(sortedKeys)

	// slave replicas count
	replicas := 1
	// all master and slave count
	nodesCount := len(addresses)
	// master count
	mastersCount := nodesCount / (replicas + 1)

	// Select master instances
	var interleaved []v1.EndpointAddress
	isLoop := true
	for isLoop {
		// take one ip from each node until we run out of addr
		// across every node.
		// loop map by sortedKeys Guarantee same order loop repeatedly
		// ref：https://blog.csdn.net/slvher/article/details/44779081
		for _, key := range sortedKeys {
			if len(nodeIPs[key]) == 0 {
				if len(interleaved) == nodesCount {
					isLoop = false
					continue
				}
			} else {
				interleaved = append(interleaved, nodeIPs[key][0])
				nodeIPs[key] = nodeIPs[key][1:]
			}
		}
	}

	masters := interleaved[0:mastersCount]

	// master ips
	var masterInstanceIPs []string
	for _, addr := range masters {
		masterInstanceIPs = append(masterInstanceIPs, addr.IP)
	}

	// Remaining nodes Count
	nodesCount -= len(masters)

	// Remaining
	interleaved = interleaved[mastersCount:]

	// Rotating the list sometimes helps to get better initial anti-affinity before the optimizer runs.
	interleaved = append(interleaved[1:], interleaved[:1]...)

	// slave ips
	var slaveInstanceIPs []string

	// print assign info when the method end
	//defer glog.V(4).Infof("masterInstanceIPs: %v\nslaveInstanceIPs: %v", masterInstanceIPs, slaveInstanceIPs)
	//这里要用闭包方式打印日志,否则slaveInstanceIPs是空slice
	// ref:https://www.kancloud.cn/liupengjie/go/576456
	var slaves []v1.EndpointAddress
	defer func() {
		// 判断一组master、slave是否在同一节点上
		for i := 0; i < len(masters); i++ {
			if *(masters[i].NodeName) == *(slaves[i].NodeName) {
				glog.Warningf("A group [master->slave] nodeName equal; masterInstanceIP: %v slaveInstanceIPs: %v \n", masterInstanceIPs[i], slaveInstanceIPs[i])
			}
		}
		glog.V(4).Infof("\nmasterInstanceIPs: %v\nslaveInstanceIPs: %v", masterInstanceIPs, slaveInstanceIPs)
	}()

	for _, master := range masters {
		assignedReplicas := 0
		for assignedReplicas < replicas {

			// 0 indicate assign finished
			if nodesCount == 0 {
				return masterInstanceIPs, slaveInstanceIPs, nil
			}

			// Return the first node not matching our current master
			var instanceIP string
			removeIndex := 0

			// find slave and master node don't match
			for i, addr := range interleaved {
				if *master.NodeName != *addr.NodeName {
					instanceIP = addr.IP
					removeIndex = i
					break
				}
			}

			// If we found a IP, use it as a best-first match.
			// Otherwise, we didn't find a IP on a different node, so we
			// go ahead and use a same-node replica.
			if instanceIP != "" {
				slaveInstanceIPs = append(slaveInstanceIPs, instanceIP)
			} else {
				slaveInstanceIPs = append(slaveInstanceIPs, interleaved[0].IP)
				removeIndex = 0

			}

			// 用于判断一组master、slave是否在同一节点上
			slaves = append(slaves, interleaved[removeIndex])

			//remove assigned addr
			// if interleaved = ["0", "1", "2"]
			// removeIndex = 0 -- >> interleaved[:0], interleaved[1:]...  -- >> ["1", "2"]
			// removeIndex = 1 -- >> interleaved[:1], interleaved[2:]...  -- >> ["0", "2"]
			// removeIndex = 2 -- >> interleaved[:2], interleaved[3:]...  -- >> ["0", "1"]
			interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)

			// nodesCount dec
			nodesCount -= 1

			// assignedReplicas inc
			assignedReplicas += 1
		}
	}

	return masterInstanceIPs, slaveInstanceIPs, nil
}

//需要分配master和slave ip的address
//endpoints：扩实例之后的endpoint信息
//redisCluster：redisCluster对象
//oldEndpoints：扩实例之前的endpoint信息
func (rco *RedisClusterOperator) assignMasterSlaveIPAddress(redisCluster *redistype.RedisCluster, endpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) ([]v1.EndpointAddress, error) {
	subset := endpoints.Subsets
	if len(subset) == 0 || len(subset[0].Addresses) == 0 {
		return nil, fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	//old endpoints为nil时表示为创建时分配;否则为升级时分配
	var addresses []v1.EndpointAddress
	if oldEndpoints == nil {
		addresses = subset[0].Addresses
	} else {
		oldSubset := oldEndpoints.Subsets
		if len(oldSubset) == 0 || len(oldSubset[0].Addresses) == 0 {
			glog.Warningf("RedisCluster: %v/%v, oldEndpoints.Subsets is nil", redisCluster.Namespace, redisCluster.Name)
			addresses = subset[0].Addresses
		} else {
			for _, new := range subset[0].Addresses {
				for i, old := range oldSubset[0].Addresses {
					if new.IP == old.IP {
						break
					}
					if len(oldSubset[0].Addresses)-1 == i {
						// 说明new是升级时新实例的addr
						addresses = append(addresses, new)
					}
				}
			}
		}
	}

	return addresses, nil
}

/*
TODO 异常场景下的处理，包括：
1、升级时redis集群异常中断；
2、正在创建升级时redisCluster挂掉后恢复；
3、pod在指定时间内没起来，创建或者升级超时，后来起来了，怎么继续创建或升级
*/
