package redis

import (
	"fmt"
	"github.com/golang/glog"
	redistype "harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	"harmonycloud.cn/middleware-operator-manager/util"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var reg = regexp.MustCompile(`([\d.]+):6379 \((\w+)...\) -> (\d+) keys \| (\d+) slots \| (\d+) slaves`)
var clusterInfoReg = regexp.MustCompile(`cluster_known_nodes:(\d+)`)

//检查集群状态
//redisCluster：redisCluster对象
func (rco *RedisClusterOperator) checkAndUpdateRedisClusterStatus(redisCluster *redistype.RedisCluster) error {

	// 检查集群状态总是获取最新的redisCluster对象,从缓存中获取的不一定是最新的
	latestRedisCluster, err := rco.customCRDClient.CrV1alpha1().RedisClusters(redisCluster.Namespace).Get(redisCluster.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("checkAndUpdateRedisClusterStatus get latest RedisCluster: %v/%v error: %v", redisCluster.Namespace, redisCluster.Name, err)
	}

	glog.V(4).Infof("Started check redisCluster: %v/%v ResourceVersion: %v", redisCluster.Namespace, redisCluster.Name, redisCluster.ResourceVersion)

	if latestRedisCluster.Status.Phase == redistype.RedisClusterRunning ||
		(latestRedisCluster.Status.Phase == redistype.RedisClusterUpgrading &&
			int32(len(latestRedisCluster.Status.Conditions)) == latestRedisCluster.Status.Replicas) ||
		(latestRedisCluster.Status.Phase == redistype.RedisClusterCreating &&
			int32(len(latestRedisCluster.Status.Conditions)) == latestRedisCluster.Status.Replicas) {

		endpoints, err := rco.defaultClient.CoreV1().Endpoints(latestRedisCluster.Namespace).Get(latestRedisCluster.GetName(), metav1.GetOptions{})

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
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
			return err
		}

		if !clusterStatusIsOK {
			abnormalReason := clusterStateFailed
			if latestRedisCluster.Status.Reason != "" {
				abnormalReason = latestRedisCluster.Status.Reason
			}
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, abnormalReason)
			return nil
		}

		_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, "")
		return err
	}

	// 异常场景,尝试修复
	//这里将阻塞
	endpoints, err := rco.checkPodInstanceIsReadyByEndpoint(latestRedisCluster)

	sortEndpointsByPodName(endpoints)

	if err != nil {
		//更新状态为Failing
		rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
		return err
	}

	if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		return fmt.Errorf("endpoints.Subsets is nil, maybe statefulset %v/%v has been deleted", latestRedisCluster.Namespace, latestRedisCluster.Name)
	}

	var phase redistype.RedisClusterPhase
	if latestRedisCluster.Status.Phase == redistype.RedisClusterFailed {
		if len(latestRedisCluster.Status.Conditions) == 0 {
			phase = redistype.RedisClusterCreating
		} else {
			phase = redistype.RedisClusterUpgrading
		}
	} else {
		phase = latestRedisCluster.Status.Phase
	}

	addresses := endpoints.Subsets[0].Addresses

	glog.Infof("latestRedisCluster: %v/%v current phase: %v , fix start.", latestRedisCluster.Namespace, latestRedisCluster.Name, phase)

	//异常场景处理
	switch phase {
	case redistype.RedisClusterCreating, redistype.RedisClusterFailed:
		isClusteredAddress, isOnlyKnownSelfAddress, err := rco.getIsClusteredAndOnlyKnownSelfAddress(addresses)
		if err != nil {
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, clusterStateFailed)
			return err
		}

		//表示新集群,所有实例都没有初始化,开始初始化集群
		if int32(len(isOnlyKnownSelfAddress)) == *latestRedisCluster.Spec.Replicas || len(isClusteredAddress) == 0 {
			return rco.createAndInitRedisCluster(latestRedisCluster)
		}

		existInstanceIp := isClusteredAddress[0].IP
		//查询当前集群的节点信息
		nodeInfos, err := rco.execClusterNodes(existInstanceIp, isClusteredAddress[0].TargetRef.Namespace, isClusteredAddress[0].TargetRef.Name)
		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
			return err
		}

		willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps, err := rco.buildWillAddClusterMasterSlaveIPs(nodeInfos, endpoints, nil)

		reference := addresses[0].TargetRef
		//加master
		if len(willAddClusterMasterIps) != 0 {
			err = rco.addMasterNodeToRedisCluster(willAddClusterMasterIps, existInstanceIp, reference.Name, reference.Namespace)
			if err != nil {
				//更新状态为Failed
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
				return err
			}
		}

		//查询新建集群的节点信息,主要是master的nodeId信息
		nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name)

		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
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
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
				return err
			}
		}

		err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIp, reference.Name, reference.Namespace, "")
		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, err.Error())
			return err
		}

		glog.Infof("cluster create and init fix success")
		//更新状态为Running
		_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, "")

		return err

	case redistype.RedisClusterScaling:
		//复制出一个Endpoints
		oldEndpoints := endpoints.DeepCopy()
		//没有scale前的endpoint里address应该是Conditions长度
		subLen := len(addresses) - len(latestRedisCluster.Status.Conditions)
		oldEndpoints.Subsets[0].Addresses = addresses[:subLen]
		return rco.upgradeRedisCluster(latestRedisCluster, oldEndpoints)
	case redistype.RedisClusterUpgrading:

		isClusteredAddress, isOnlyKnownSelfAddress, err := rco.getIsClusteredAndOnlyKnownSelfAddress(addresses)
		if err != nil {
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterFailed, clusterStateFailed)
			return err
		}

		//复制出一个Endpoints
		oldEndpoints := endpoints.DeepCopy()
		//新增的实例数
		subLen := len(addresses) - len(latestRedisCluster.Status.Conditions)
		oldEndpoints.Subsets[0].Addresses = addresses[:subLen]

		//没有形成集群的实例数如果等于新加实例数,则说明还没有进行升级操作
		if subLen == len(isOnlyKnownSelfAddress) {
			return rco.upgradeRedisCluster(latestRedisCluster, oldEndpoints)
		}

		existInstanceIp := isClusteredAddress[0].IP
		//查询当前集群的节点信息
		nodeInfos, err := rco.execClusterNodes(existInstanceIp, isClusteredAddress[0].TargetRef.Namespace, isClusteredAddress[0].TargetRef.Name)
		if err != nil {
			//更新状态为Running
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
			return err
		}

		willAddClusterMasterIps, willAddClusterSlaveIps, slaveParentIps, err := rco.buildWillAddClusterMasterSlaveIPs(nodeInfos, endpoints, oldEndpoints)

		reference := addresses[0].TargetRef
		//加master
		if len(willAddClusterMasterIps) != 0 {
			err = rco.addMasterNodeToRedisCluster(willAddClusterMasterIps, existInstanceIp, reference.Name, reference.Namespace)
			if err != nil {
				//更新状态为Running
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
				return err
			}
		}

		//查询新建集群的节点信息,主要是master的nodeId信息
		nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name)

		if err != nil {
			//更新状态为Failed
			rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
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
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
				return err
			}
		}

		upgradeType := latestRedisCluster.Spec.UpdateStrategy.Type
		switch upgradeType {
		case redistype.AutoReceiveStrategyType:
			err = rco.rebalanceRedisClusterSlotsToMasterNode(existInstanceIp, reference.Name, reference.Namespace, latestRedisCluster.Spec.UpdateStrategy.Pipeline)
			if err != nil {
				//更新状态为Running
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
				return err
			}
		case redistype.AssignReceiveStrategyType:
			infos, err := rco.execRedisTribInfo(existInstanceIp, reference.Name, reference.Namespace)
			if err != nil {
				//更新状态为Running
				rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
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
				updateStrategy := latestRedisCluster.Spec.UpdateStrategy.DeepCopy()
				strategies := updateStrategy.AssignStrategies
				strategyLen := len(strategies)
				willAssignIpLen := len(willAssginSlotsIP)
				if strategyLen < willAssignIpLen {
					err := fmt.Errorf("assign slots to new master strategies is error: strategyCount too less")
					glog.Error(err.Error())
					//更新状态为Running
					rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
					return err
				}

				updateStrategy.AssignStrategies = strategies[(strategyLen - willAssignIpLen):]

				//查询新建集群的节点信息,主要是master的nodeId信息
				nodeInfos, err = rco.execClusterNodes(existInstanceIp, reference.Namespace, reference.Name)

				if err != nil {
					//更新状态为Failed
					rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
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
					rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, err.Error())
					return err
				}
			}

		default:
			err := fmt.Errorf("latestRedisCluster UpdateStrategy Type only [AutoReceive, AssignReceive] error")
			glog.Error(err.Error())
			return err
		}

		glog.Infof("cluster upgrade fix success")
		//更新状态为Running
		_, err = rco.updateRedisClusterStatus(latestRedisCluster, endpoints, redistype.RedisClusterRunning, "")

		return err
	default:
		glog.Warningf("deleting or none phase shouldn't reach hear")
		return nil
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

	return composeMasterSlaveIP(newAddresses, existedMasterInstanceIPs, existedSlaveInstanceIPs, masterSlaveConnector)
}

func composeMasterSlaveIP(newAddresses []v1.EndpointAddress, existedMasterInstanceIPs, existedSlaveInstanceIPs []string, masterSlaveConnector map[string]string) ([]string, []string, []string, error) {
	//参考升级前,根据新endpoints里的Addresses生成IP分配
	var willAddClusterAddr, existMasterAddr, existSlaveAddr []v1.EndpointAddress
	for _, addr := range newAddresses {
		if util.InSlice(addr.IP, existedMasterInstanceIPs) {
			existMasterAddr = append(existMasterAddr, addr)
		} else if util.InSlice(addr.IP, existedSlaveInstanceIPs) {
			existSlaveAddr = append(existSlaveAddr, addr)
		} else {
			willAddClusterAddr = append(willAddClusterAddr, addr)
		}
	}

	willAssignMasterCount := len(newAddresses)/2 - len(existedMasterInstanceIPs)

	//分配master
	var willAddClusterMasterAddr []v1.EndpointAddress

	for i := 0; i < len(willAddClusterAddr); {

		if len(willAddClusterMasterAddr) == willAssignMasterCount {
			break
		}

		isSameNode := false
		for _, existAddr := range existMasterAddr {
			if *existAddr.NodeName == *willAddClusterAddr[i].NodeName {
				isSameNode = true
				break
			}
		}

		if isSameNode {
			i++
			continue
		}

		//找到不同node的addr ip
		willAddClusterMasterAddr = append(willAddClusterMasterAddr, willAddClusterAddr[i])
		existMasterAddr = append(existMasterAddr, willAddClusterAddr[i])
		willAddClusterAddr = append(willAddClusterAddr[:i], willAddClusterAddr[i+1:]...)
	}

	//如果willAddClusterMasterAddr长度不够willAssignMasterCount则取前面的addr作为master实例
	for i := 0; len(willAddClusterMasterAddr) < willAssignMasterCount; i++ {
		willAddClusterMasterAddr = append(willAddClusterMasterAddr, willAddClusterAddr[i])
		existMasterAddr = append(existMasterAddr, willAddClusterAddr[i])
		willAddClusterAddr = append(willAddClusterAddr[:i], willAddClusterAddr[i+1:]...)
	}

	//没有slave的master Addr
	var noSlaveMasterAddr []v1.EndpointAddress
	for _, addr := range existMasterAddr {
		if _, ok := masterSlaveConnector[addr.IP]; !ok {
			noSlaveMasterAddr = append(noSlaveMasterAddr, addr)
		}
	}

	//noSlaveMasterAddr = append(noSlaveMasterAddr, willAddClusterMasterAddr...)

	/*	if len(noSlaveMasterAddr) != len(willAddClusterAddr) {
		err := fmt.Errorf("cluster state is abnormal, noSlaveMasterAddr: %v willAddClusterAddr: %v", noSlaveMasterAddr, willAddClusterAddr)
		glog.Errorf(err.Error())
		return nil, nil, nil, err
	}*/

	//--------------------------------------------

	//key: nodeName value: ips
	nodeIPs := make(map[string][]v1.EndpointAddress)
	for _, addr := range willAddClusterAddr {
		nodeIPs[*addr.NodeName] = append(nodeIPs[*addr.NodeName], addr)
	}

	//将nodeIPs map的key排序,保证多次遍历map时输出顺序一致
	sortedKeys := make([]string, 0)
	for k := range nodeIPs {
		sortedKeys = append(sortedKeys, k)
	}

	// sort 'string' key in increasing order
	sort.Strings(sortedKeys)

	// all master and slave count
	nodesCount := len(willAddClusterAddr)

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

	// Rotating the list sometimes helps to get better initial anti-affinity before the optimizer runs.
	interleaved = append(interleaved[1:], interleaved[:1]...)

	//选slave
	var willAddClusterSlaveIPs, slaveParentIps []string
	var willAddClusterSlaveAddr []v1.EndpointAddress

	for _, master := range noSlaveMasterAddr {
		if nodesCount == 0 {
			break
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
			willAddClusterSlaveIPs = append(willAddClusterSlaveIPs, instanceIP)
		} else {
			willAddClusterSlaveIPs = append(willAddClusterSlaveIPs, interleaved[0].IP)
			removeIndex = 0
		}

		//待加入slave的master addr
		slaveParentIps = append(slaveParentIps, master.IP)

		// 用于判断一组master、slave是否在同一节点上
		willAddClusterSlaveAddr = append(willAddClusterSlaveAddr, interleaved[removeIndex])

		//remove assigned addr
		// if interleaved = ["0", "1", "2"]
		// removeIndex = 0 -- >> interleaved[:0], interleaved[1:]...  -- >> ["1", "2"]
		// removeIndex = 1 -- >> interleaved[:1], interleaved[2:]...  -- >> ["0", "2"]
		// removeIndex = 2 -- >> interleaved[:2], interleaved[3:]...  -- >> ["0", "1"]
		interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)

		// nodesCount dec
		nodesCount -= 1
	}

	var willAddClusterMasterIPs []string
	for _, addr := range willAddClusterMasterAddr {
		willAddClusterMasterIPs = append(willAddClusterMasterIPs, addr.IP)
	}

	glog.V(4).Infof("\nwillAddClusterMasterIPs: %v\nwillAddClusterSlaveIPs: %v\nslaveParentIps: %v", willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps)

	return willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, nil
}
