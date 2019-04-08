package redis

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/api/core/v1"
	)

func TestBuildNodeInfo(t *testing.T) {

	lineInfo := "aaaaa 1.1.1.1:6379 myself,master - 0 0 6 connected 31-12 98-98 102-191 [asa<--asalssakjdhakjhk1h2kjh1j2k]"

	info := strings.Fields(lineInfo)
	//slave没有slot,也可能master没分配slot
	if len(info) >= 9 {
		Slot := strings.Join(info[8:], " ")
		t.Log(Slot)
	}
}

func TestGoFunc1(t *testing.T) {

	for {
		fmt.Println("000")
		create()
		fmt.Println("444")
		time.Sleep(10 * time.Minute)
	}

}

func create() {
	go func() {
		fmt.Println("1111")
		time.Sleep(20 * time.Second)
		fmt.Println("22222")
	}()
}

func TestGoFunc2(t *testing.T) {

	for {
		fmt.Println("000")
		create2()
		fmt.Println("444")
		time.Sleep(2 * time.Minute)
	}

}

func create2() {
	go func() {
		fmt.Println("1111")
		go func() {
			fmt.Println("333")
			time.Sleep(20 * time.Second)
			fmt.Println("555")
		}()
		fmt.Println("22222")
	}()
}

func TestDeferError(t *testing.T) {
	fmt.Println("111111111111")
	deferError()
	fmt.Println("22222222")
}

func deferError() (err error) {
	fmt.Println("3333")
	defer func() {
		fmt.Println(err)
	}()
	fmt.Println("4444")
	err = errors.New("测试defer error")
	return err
}

func TestDefer(t *testing.T) {
	defer fmt.Println("111111111111")
	defer fmt.Println("22222222")
}


func TestIota (t *testing.T) {
	fmt.Println(createCluster)
	fmt.Println(upgradeCluster)
	fmt.Println(dropCluster)
}

func TestIngoreCase (t *testing.T) {
	fmt.Println(strings.EqualFold("foreground", "Foreground"))
	fmt.Println(strings.EqualFold("ForeGround", "Foreground"))
}

func TestAssignMasterSlaveIP (t *testing.T) {
	rco := &RedisClusterOperator{
	}

	redisCluster := &v1alpha1.RedisCluster {
		ObjectMeta: metav1.ObjectMeta  {
			Namespace: "kube-system",
			Name: "example000-redis-cluster",
		},
	}

	a := "10.10.103.154-share"
	b := "10.10.102.77-slave"
	c := "10.10.103.155-build"
	d := "10.10.103.152-slave"
	e := "10.10.104.15-slave"
	f := "10.10.105.14-slave"
	endpoints := &v1.Endpoints {
		Subsets: []v1.EndpointSubset {
			{
				Addresses: []v1.EndpointAddress {
					{
						IP: "10.168.131.67",
						NodeName: &a,
					},
					{
						IP: "10.168.132.35",
						NodeName: &b,
					},
					{
						IP: "10.168.132.36",
						NodeName: &b,
					},
					{
						IP: "10.168.33.119",
						NodeName: &c,
					},
					{
						IP: "10.168.33.120",
						NodeName: &c,
					},
					{
						IP: "10.168.9.186",
						NodeName: &d,
					},
				},
			},
		},
	}

	masterIP, slaveIP, err := rco.assignMasterSlaveIP(redisCluster, endpoints, nil)
	t.Logf("create masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)

	oldEndpoints := endpoints
	newEndpoints := &v1.Endpoints {
		Subsets: []v1.EndpointSubset {
			{
				Addresses: []v1.EndpointAddress {
					{
						IP: "10.168.131.67",
						NodeName: &a,
					},
					{
						IP: "10.168.132.35",
						NodeName: &b,
					},
					{
						IP: "10.168.132.36",
						NodeName: &b,
					},
					{
						IP: "10.168.33.119",
						NodeName: &c,
					},
					{
						IP: "10.168.33.120",
						NodeName: &c,
					},
					{
						IP: "10.168.9.186",
						NodeName: &d,
					},
					{
						IP: "10.168.10.18",
						NodeName: &e,
					},
					{
						IP: "10.168.11.192",
						NodeName: &f,
					},
					{
						IP: "10.168.11.5",
						NodeName: &e,
					},
					{
						IP: "10.168.12.9",
						NodeName: &e,
					},
				},
			},
		},
	}

	masterIP, slaveIP, err = rco.assignMasterSlaveIP(redisCluster, newEndpoints, oldEndpoints)
	t.Logf("upgrade masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
}

func TestDeferSlice(t *testing.T) {

	var slaveInstanceIPs []string

	// print assign info when the method end
	defer func() {
		t.Logf("slaveInstanceIPs: %v", slaveInstanceIPs)
	}()

	slaveInstanceIPs = append(slaveInstanceIPs, "1.1.1.1", "2.2.2.2")
	t.Logf("slaveInstanceIPs: %v", slaveInstanceIPs)
}

func TestDeferErr(t *testing.T) {
	err := errors.New("111")

	defer func() {
		t.Logf("error: %v", err)
	}()
	err = errors.New("222")
}


func TestCreateAssignMasterSlaveIP (t *testing.T) {
	rco := &RedisClusterOperator{
	}

	redisCluster := &v1alpha1.RedisCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
	}

	a := "10.10.103.154-share"
	b := "10.10.102.77-slave"
	c := "10.10.102.77-slave"
	d := "10.10.103.155-build"
	e := "10.10.103.155-build"
	f := "10.10.103.152-slave"
	endpoints := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &d,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &e,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &f,
					},
				},
			},
		},
	}

	for i:= 0; i < 10; i++ {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(redisCluster, endpoints, nil)
		t.Logf("create masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
