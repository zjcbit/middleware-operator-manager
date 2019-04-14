package redis

import (
	"fmt"
	"github.com/golang/glog"
	redistype "harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	"harmonycloud.cn/middleware-operator-manager/util"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"regexp"
	"strconv"
	"strings"
)

var reg = regexp.MustCompile(`([\d.]+):6379 \((\w+)...\) -> (\d+) keys \| (\d+) slots \| (\d+) slaves`)

//检查集群状态
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) checkAndUpdateRedisClusterStatus(redisCluster *redistype.RedisCluster) error {

	if redisCluster.Status.Phase != redistype.RedisClusterScaling &&
		redisCluster.Status.Phase != redistype.RedisClusterUpgrading &&
		redisCluster.Status.Phase != redistype.RedisClusterFailed &&
		redisCluster.Status.Phase != redistype.RedisClusterCreating {

		endpoints, err := rco.defaultClient.CoreV1().Endpoints(redisCluster.Namespace).Get(redisCluster.GetName(), metav1.GetOptions{})

		if err != nil {
			return fmt.Errorf("get redis cluster endpoint is error: %v", err)
		}

		if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
			return fmt.Errorf("redis cluster endpoint: %v is blank", endpoints)
		}

		sortEndpointsByPodName(endpoints)

		clusterInstanceIp := endpoints.Subsets[0].Addresses[0].IP
		reference := endpoints.Subsets[0].Addresses[0].TargetRef

		clusterStatusIsOK, _, err := rco.execClusterInfo(clusterInstanceIp, reference.Name, reference.Namespace)
		if err != nil {
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
			return err
		}

		if !clusterStatusIsOK {
			abnormalReason := clusterStateFailed
			if redisCluster.Status.Reason != "" {
				abnormalReason = redisCluster.Status.Reason
			}
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, abnormalReason)
			return nil
		}

		_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")
		return err
	}

	// 异常场景,尝试修复
	//这里将阻塞
	endpoints, err := rco.checkPodInstanceIsReadyByEndpoint(redisCluster)

	sortEndpointsByPodName(endpoints)

	if err != nil {
		//更新状态为Failing
		rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", redisCluster.Namespace, redisCluster.Name)
	}

	var phase redistype.RedisClusterPhase
	if redisCluster.Status.Phase == redistype.RedisClusterFailed {
		if len(redisCluster.Status.Conditions) == 0 {
			phase = redistype.RedisClusterCreating
		} else {
			phase = redistype.RedisClusterUpgrading
		}
	} else {
		phase = redisCluster.Status.Phase
	}

	addresses := endpoints.Subsets[0].Addresses
	//异常场景处理
	switch phase {
	case redistype.RedisClusterCreating, redistype.RedisClusterFailed:
		isClusteredAddress, isOnlyKnownSelfAddress, err := rco.getIsClusteredAndOnlyKnownSelfAddress(addresses)
		if err != nil {
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, clusterStateFailed)
			return err
		}

		//表示新集群,所有实例都没有初始化,开始初始化集群
		if int32(len(isOnlyKnownSelfAddress)) == *redisCluster.Spec.Replicas || len(isClusteredAddress) == 0 {
			return rco.createAndInitRedisCluster(redisCluster)
		}

		existInstanceIp := isClusteredAddress[0].IP
		//查询当前集群的节点信息
		nodeInfos, err := rco.execClusterNodes(existInstanceIp, isClusteredAddress[0].TargetRef.Namespace, isClusteredAddress[0].TargetRef.Name)
		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
			return err
		}

		willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps, err := rco.buildWillAddClusterMasterSlaveIPs(nodeInfos, endpoints, nil)

		reference := addresses[0].TargetRef
		//加master
		if len(willAddClusterMasterIps) != 0 {
			err = rco.addMasterNodeToRedisCluster(willAddClusterMasterIps, existInstanceIp, reference.Name, reference.Namespace)
			if err != nil {
				//更新状态为Failed
				rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
				return err
			}
		}

		//查询新建集群的节点信息,主要是master的nodeId信息
		nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name)

		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
			return err
		}

		if len(willAddClusterSlaveIps) != 0 {
			// 根据slaveParentIps拿nodeId,下标对应
			var masterInstanceNodeIds []string
			for _, masterInstanceIp := range slaveParentIps {
				for _, info := range nodeInfos {
					if masterInstanceIp+":6379" == info.IpPort {
						masterInstanceNodeIds = append(masterInstanceNodeIds, info.NodeId)
						break
					}
				}
			}

			err = rco.addSlaveToClusterMaster(willAddClusterSlaveIps, slaveParentIps, masterInstanceNodeIds, reference.Namespace, reference.Name)
			if err != nil {
				//更新状态为Failed
				rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
				return err
			}
		}

		err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIp, reference.Name, reference.Namespace, "")
		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
			return err
		}

		glog.Infof("cluster fix create and init success")
		//更新状态为Running
		_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")

		return err

	case redistype.RedisClusterScaling:
		//复制出一个Endpoints
		oldEndpoints := endpoints.DeepCopy()
		//没有scale前的endpoint里address应该是Conditions长度
		subLen := len(addresses) - len(redisCluster.Status.Conditions)
		oldEndpoints.Subsets[0].Addresses = addresses[:subLen]
		return rco.upgradeRedisCluster(redisCluster, oldEndpoints)
	case redistype.RedisClusterUpgrading:

		isClusteredAddress, isOnlyKnownSelfAddress, err := rco.getIsClusteredAndOnlyKnownSelfAddress(addresses)
		if err != nil {
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterFailed, clusterStateFailed)
			return err
		}

		//复制出一个Endpoints
		oldEndpoints := endpoints.DeepCopy()
		//新增的实例数
		subLen := len(addresses) - len(redisCluster.Status.Conditions)
		oldEndpoints.Subsets[0].Addresses = addresses[:subLen]

		//没有形成集群的实例数如果等于新加实例数,则说明还没有进行升级操作
		if subLen == len(isOnlyKnownSelfAddress) {
			return rco.upgradeRedisCluster(redisCluster, oldEndpoints)
		}

		existInstanceIp := isClusteredAddress[0].IP
		//查询当前集群的节点信息
		nodeInfos, err := rco.execClusterNodes(existInstanceIp, isClusteredAddress[0].TargetRef.Namespace, isClusteredAddress[0].TargetRef.Name)
		if err != nil {
			//更新状态为Running
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
			return err
		}

		willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps, err := rco.buildWillAddClusterMasterSlaveIPs(nodeInfos, endpoints, oldEndpoints)

		reference := addresses[0].TargetRef
		//加master
		if len(willAddClusterMasterIps) != 0 {
			err = rco.addMasterNodeToRedisCluster(willAddClusterMasterIps, existInstanceIp, reference.Name, reference.Namespace)
			if err != nil {
				//更新状态为Running
				rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
				return err
			}
		}

		//查询新建集群的节点信息,主要是master的nodeId信息
		nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name)

		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
			return err
		}

		//加slave
		if len(willAddClusterSlaveIps) != 0 {
			// 根据slaveParentIps拿nodeId,下标对应
			var masterInstanceNodeIds []string
			for _, masterInstanceIp := range slaveParentIps {
				for _, info := range nodeInfos {
					if masterInstanceIp+":6379" == info.IpPort {
						masterInstanceNodeIds = append(masterInstanceNodeIds, info.NodeId)
						break
					}
				}
			}

			err = rco.addSlaveToClusterMaster(willAddClusterSlaveIps, slaveParentIps, masterInstanceNodeIds, reference.Namespace, reference.Name)
			if err != nil {
				//更新状态为Running
				rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
				return err
			}
		}

		upgradeType := redisCluster.Spec.UpdateStrategy.Type
		switch upgradeType {
		case redistype.AutoReceiveStrategyType:
			err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIp, reference.Name, reference.Namespace, redisCluster.Spec.UpdateStrategy.Pipeline)
			if err != nil {
				//更新状态为Running
				rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
				return err
			}
		case redistype.AssignReceiveStrategyType:
			infos, err := rco.execRedisTribInfo(existInstanceIp, reference.Name, reference.Namespace)
			if err != nil {
				//更新状态为Running
				rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
				return err
			}

			//查看卡槽分配情况
			var willAssginSlotsIP []string
			for _, info := range infos {
				if info.Slots == 0 {
					willAssginSlotsIP = append(willAssginSlotsIP, info.Ip)
				}
			}

			if len(willAssginSlotsIP) != 0 {
				updateStrategy := redisCluster.Spec.UpdateStrategy.DeepCopy()
				strategies := updateStrategy.AssignStrategies
				strategyLen := len(strategies)
				willAssignIpLen := len(willAssginSlotsIP)
				if strategyLen < willAssignIpLen {
					err := fmt.Errorf("assign slots to new master strategies is error: strategyCount too less")
					glog.Error(err.Error())
					//更新状态为Running
					rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
					return err
				}

				updateStrategy.AssignStrategies = strategies[(strategyLen - willAssignIpLen):]

				//查询新建集群的节点信息,主要是master的nodeId信息
				nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name)

				if err != nil {
					//更新状态为Failed
					rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
					return err
				}

				var willAssginSlotsNodeIds []string
				for _, willIp := range willAssginSlotsIP {
					for _, info := range nodeInfos {
						if willIp+":6379" == info.IpPort {
							willAssginSlotsNodeIds = append(willAssginSlotsNodeIds, info.NodeId)
						}
					}
				}

				//reshare分配卡槽
				err = rco.reshareRedisClusterSlotsToMasterNode(*updateStrategy, existInstanceIp, reference.Name, reference.Namespace, willAssginSlotsNodeIds)
				if err != nil {
					//更新状态为Running
					rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
					return err
				}
			}

		default:
			err := fmt.Errorf("redisCluster UpdateStrategy Type only [AutoReceive, AssignReceive] error")
			glog.Error(err.Error())
			return err
		}

		glog.Infof("cluster fix upgrade success")
		//更新状态为Running
		_, err = rco.updateRedisClusterStatus(redisCluster, endpoints, redistype.RedisClusterRunning, "")

		return err
	}

	return nil
}

/*
获取已形成集群的address和独立的address
*/
func (rco *RedisClusterOperator) getIsClusteredAndOnlyKnownSelfAddress(addresses []v1.EndpointAddress) ([]v1.EndpointAddress, []v1.EndpointAddress, error) {
	var isClusteredAddress, isOnlyKnownSelfAddress []v1.EndpointAddress
	for _, addr := range addresses {
		isOK, isOnlyKnownSelf, err := rco.execClusterInfo(addr.IP, addr.TargetRef.Name, addr.TargetRef.Namespace)
		if err != nil {
			return nil, nil, err
		}

		//如果isOnlyKnownSelf为false,其状态为fail,表示集群有问题(可能原因pod实例IP和node配置里的IP不匹配)
		if !isOnlyKnownSelf && !isOK {
			glog.Errorf("%v by instance: %v", clusterStateFailed, addr.IP)
			return nil, nil, err
		}

		//该addr对应的podIP已经组成集群
		if !isOnlyKnownSelf && isOK {
			isClusteredAddress = append(isClusteredAddress, addr)
		} else {
			//不会出现isOnlyKnownSelf和isOK同时为true
			//独立的实例,没有加入到集群
			isOnlyKnownSelfAddress = append(isOnlyKnownSelfAddress, addr)
		}
	}
	return isClusteredAddress, isOnlyKnownSelfAddress, nil
}

//查看redis集群master信息
//existInstanceIp：redis集群中的一个实例IP
//podName：pod实例中的一个podName,不一定已经加入redis集群
//namespace：redis集群所在的ns
func (rco *RedisClusterOperator) execRedisTribInfo(existInstanceIp, podName, namespace string) ([]redisTribInfo, error) {
	infoCmd := fmt.Sprintf(" redis-trib.rb info %v:6379 ", existInstanceIp)
	commandInfo := []string{"/bin/sh", "-c", infoCmd}
	stdout, stderr, err := rco.ExecToPodThroughAPI(commandInfo, redisContainerName, podName, namespace, nil)

	if err != nil || stderr != "" || strings.Contains(stdout, "[ERR]") {
		err := fmt.Errorf("redisClusterInstance: %v/%v -- infoCmd: %v\n -- stdout: %v\n -- stderr: %v\n -- error: %v", namespace, podName, commandInfo, stdout, stderr, err)
		glog.Errorf(err.Error())
		return []redisTribInfo{}, err
	}
	glog.V(4).Infof("redisClusterInstance: %v/%v infoCmd: %v \n -- \nstdout: %v", namespace, podName, commandInfo, stdout)

	var masterInfos []redisTribInfo
	infos := strings.Split(stdout, "\n")
	for _, info := range infos {
		submatches := reg.FindStringSubmatch(info)
		if len(submatches) != 6 {
			continue
		}
		keys, _ := strconv.Atoi(submatches[3])
		slots, _ := strconv.Atoi(submatches[4])
		slaves, _ := strconv.Atoi(submatches[5])
		masterInfo := redisTribInfo{
			Ip:           submatches[1],
			Port:         strconv.Itoa(redisServicePort6379),
			NodeIdPrefix: submatches[2],
			Keys:         keys,
			Slots:        slots,
			Slaves:       slaves,
		}
		masterInfos = append(masterInfos, masterInfo)
	}

	return masterInfos, nil
}

/*
待加入集群的master、slave ip以及slave对应的masterIP
willAddClusterMasterIps：需要加入的master ip
willAddClusterSlaveIps：需要加入的slave ip
slaveParentIps: 需要加入的slave对应的master ip,因为异常场景下可能slave对应的master ip不在willAddClusterMasterIps集合里
*/
func (rco *RedisClusterOperator) buildWillAddClusterMasterSlaveIPs(nodeInfos []*redisNodeInfo, newEndpoints *v1.Endpoints, oldEndpoints *v1.Endpoints) ([]string, []string, []string, error) {

	if len(newEndpoints.Subsets) == 0 || len(newEndpoints.Subsets[0].Addresses) == 0 {
		return nil, nil, nil, fmt.Errorf("newEndpoints subsets or addresses is empty")
	}

	newAddresses := newEndpoints.Subsets[0].Addresses

	//已存在的master、slave IP以及其绑定关系
	var existedMasterInstanceIPs, existedSlaveInstanceIPs []string
	masterSlaveConnector := make(map[string]string, len(newAddresses))
	for _, info := range nodeInfos {
		if masterFlagType == info.Flags {
			masterIP := strings.Split(info.IpPort, ":")[0]
			for _, info1 := range nodeInfos {
				if info.NodeId == info1.Master {
					masterSlaveConnector[masterIP] = strings.Split(info1.IpPort, ":")[0]
					break
				}
			}
			existedMasterInstanceIPs = append(existedMasterInstanceIPs, masterIP)
		} else {
			existedSlaveInstanceIPs = append(existedSlaveInstanceIPs, strings.Split(info.IpPort, ":")[0])
		}
	}

	//参考升级前,根据新endpoints里的Addresses生成IP分配
	masterInstanceIPs, slaveInstanceIPs, err := rco.waitExpectMasterSlaveIPAssign(newAddresses, masterSlaveConnector)
	if err == wait.ErrWaitTimeout {
		var newAddresses []v1.EndpointAddress
		newAddresses, err = rco.assignMasterSlaveIPAddress(newEndpoints, oldEndpoints)
		if err != nil {
			return nil, nil, nil, err
		}
		// 如果参考升级前的IP分配超时,则不参考,新节点直接分配
		masterInstanceIPs, slaveInstanceIPs, err = rco.assignMasterSlaveIP(newAddresses)
	}

	if err != nil {
		return nil, nil, nil, err
	}

	//收集待加入集群的master节点
	var willAddClusterMasterIps []string
	for _, masterIPs := range masterInstanceIPs {
		if !util.InSlice(masterIPs, existedMasterInstanceIPs) {
			willAddClusterMasterIps = append(willAddClusterMasterIps, masterIPs)
		}
	}

	//收集待加入集群的slave节点以及slave对应的master ips
	var willAddClusterSlaveIps []string
	var slaveParentIps []string
	for i, slaveIPs := range slaveInstanceIPs {
		if !util.InSlice(slaveIPs, existedSlaveInstanceIPs) {
			willAddClusterSlaveIps = append(willAddClusterSlaveIps, slaveIPs)
			slaveParentIps = append(slaveParentIps, masterInstanceIPs[i])
		}
	}

	return willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps, nil
}
