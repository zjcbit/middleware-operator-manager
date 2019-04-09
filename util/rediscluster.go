package util

import (
	"harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
)

func DeepEqualRedisClusterStatus(a, b v1alpha1.RedisClusterStatus) bool {

	if a.Replicas != b.Replicas || a.Reason != b.Reason || a.Phase != b.Phase {
		return false
	}

	if len(a.Conditions) != len(b.Conditions) {
		return false
	}

	if len(a.Conditions) == 0 {
		return true
	}

	for i, v := range a.Conditions {
		if v.Reason != b.Conditions[i].Reason ||
			v.DomainName != b.Conditions[i].DomainName ||
			v.Status != b.Conditions[i].Status ||
			v.Name != b.Conditions[i].Name ||
			v.NodeId != b.Conditions[i].NodeId ||
			v.Slots != b.Conditions[i].Slots ||
			v.Type != b.Conditions[i].Type ||
			v.HostIP != b.Conditions[i].HostIP ||
			v.Message != b.Conditions[i].Message ||
			v.MasterNodeId != b.Conditions[i].MasterNodeId ||
			v.Hostname != b.Conditions[i].Hostname ||
			v.Instance != b.Conditions[i].Instance {
			return false
		}
	}

	return true
}
