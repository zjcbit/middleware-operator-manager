package redis

const (
	masterFlagType     string = "master"
	slaveFlagType      string = "slave"
	clusterStatusOK           = "cluster_state:ok"
	clusterStateFailed        = "cluster state failed"
)

/*
25e8c9379c3db621da6ff8152684dc95dbe2e163 192.168.64.102:8002 master - 0 1490696025496 15 connected 5461-10922
d777a98ff16901dffca53e509b78b65dd1394ce2 192.168.64.156:8001 slave 0b1f3dd6e53ba76b8664294af2b7f492dbf914ec 0 1490696027498 12 connected
8e082ea9fe9d4c4fcca4fbe75ba3b77512b695ef 192.168.64.108:8000 master - 0 1490696025997 14 connected 0-5460
0b1f3dd6e53ba76b8664294af2b7f492dbf914ec 192.168.64.170:8001 myself,master - 0 0 12 connected 10923-16383
eb8adb8c0c5715525997bdb3c2d5345e688d943f 192.168.64.101:8002 slave 25e8c9379c3db621da6ff8152684dc95dbe2e163 0 1490696027498 15 connected
4000155a787ddab1e7f12584dabeab48a617fc46 192.168.67.54:8000 slave 8e082ea9fe9d4c4fcca4fbe75ba3b77512b695ef 0 1490696026497 14 connected

节点ID：例如25e8c9379c3db621da6ff8152684dc95dbe2e163
ip:port：节点的ip地址和端口号，例如192.168.64.102:8002
flags：节点的角色(master,slave,myself)以及状态(pfail,fail)
如果节点是一个从节点的话，那么跟在flags之后的将是主节点的节点ID，例如192.168.64.156:8001主节点的ID就是0b1f3dd6e53ba76b8664294af2b7f492dbf914ec
集群最近一次向节点发送ping命令之后，过了多长时间还没接到回复
节点最近一次返回pong回复的时间
节点的配置纪元(config epoch)
本节点的网络连接情况
节点目前包含的槽，例如192.168.64.102:8002目前包含的槽为5461-10922
*/
//<id> <ip:port> <flags> <master> <ping-sent> <pong-recv> <config-epoch> <link-state> <slot> <slot> ... <slot>
type redisNodeInfo struct {
	//节点ID：例如25e8c9379c3db621da6ff8152684dc95dbe2e163
	NodeId string `json:"nodeId,omitempty"`
	//ip:port：节点的ip地址和端口号，例如192.168.64.102:8002
	IpPort string `json:"ipPort,omitempty"`
	//flags：节点的角色(master、slave、myself,master、myself,slave、myself,pfail、myself,fail)以及状态(pfail,fail)
	Flags string `json:"flags,omitempty"`
	//slave的master id
	Master string `json:"master,omitempty"`
	//集群最近一次向节点发送ping命令之后，过了多长时间还没接到回复
	PingSent string `json:"pingSent,omitempty"`
	//节点最近一次返回pong回复的时间
	PongRecv string `json:"pongRecv,omitempty"`
	//节点的配置纪元(config epoch)
	ConfigEpoch string `json:"configEpoch,omitempty"`
	//本节点的网络连接情况
	LinkState string `json:"linkState,omitempty"`
	//节点目前包含的槽
	Slot string `json:"slot,omitempty"`
}

type instanceInfo struct {
	InstanceIP string `json:"instanceIP,omitempty"`
	HostName   string `json:"hostName,omitempty"`
	HostIP     string `json:"hostIP,omitempty"`
	NodeName   string `json:"nodeName,omitempty"`
	DomainName string `json:"domainName,omitempty"`
}

/*
127.0.0.1:8001> cluster info
cluster_state:ok

如果当前redis发现有failed的slots，默认为把自己cluster_state从ok个性为fail, 写入命令会失败。如果设置cluster-require-full-coverage为no,则无此限制。
cluster_slots_assigned:16384   #已分配的槽
cluster_slots_ok:16384              #槽的状态是ok的数目
cluster_slots_pfail:0                    #可能失效的槽的数目
cluster_slots_fail:0                      #已经失效的槽的数目
cluster_known_nodes:6             #集群中节点个数
cluster_size:3                              #集群中设置的分片个数
cluster_current_epoch:15          #集群中的currentEpoch总是一致的,currentEpoch越高，代表节点的配置或者操作越新,集群中最大的那个node epoch
cluster_my_epoch:12                 #当前节点的config epoch，每个主节点都不同，一直递增, 其表示某节点最后一次变成主节点或获取新slot所有权的逻辑时间.
cluster_stats_messages_sent:270782059
cluster_stats_messages_received:270732696
*/
type clusterStatusInfo struct {
	ClusterState                 string `json:"clusterState,omitempty"`
	ClusterSlotsAssigned         string `json:"clusterSlotsAssigned,omitempty"`
	ClusterSlotsOk               string `json:"clusterSlotsOk,omitempty"`
	ClusterSlotsPfail            string `json:"clusterSlotsPfail,omitempty"`
	ClusterSlotsFail             string `json:"clusterSlotsFail,omitempty"`
	ClusterKnownNodes            string `json:"clusterKnownNodes,omitempty"`
	ClusterSize                  string `json:"clusterSize,omitempty"`
	ClusterCurrentEpoch          string `json:"clusterCurrentEpoch,omitempty"`
	ClusterMyEpoch               string `json:"clusterMyEpoch,omitempty"`
	ClusterStatsMessagesSent     string `json:"clusterStatsMessagesSent,omitempty"`
	ClusterStatsMessagesReceived string `json:"clusterStatsMessagesReceived,omitempty"`
}

type redisTribInfo struct {
	Ip           string `json:"ip,omitempty"`
	Port         string `json:"port,omitempty"`
	NodeIdPrefix string `json:"nodeIdPrefix,omitempty"`
	Keys         int    `json:"keys,omitempty"`
	Slots        int    `json:"slots,omitempty"`
	Slaves       int    `json:"slaves,omitempty"`
}
