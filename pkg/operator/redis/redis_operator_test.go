package redis

import (
	"errors"
	"fmt"
	"harmonycloud.cn/middleware-operator-manager/pkg/apis/redis/v1alpha1"
	"harmonycloud.cn/middleware-operator-manager/util"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
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

func TestIota(t *testing.T) {
	fmt.Println(createCluster)
	fmt.Println(upgradeCluster)
	fmt.Println(dropCluster)
}

func TestIngoreCase(t *testing.T) {
	fmt.Println(strings.EqualFold("foreground", "Foreground"))
	fmt.Println(strings.EqualFold("ForeGround", "Foreground"))
}

func TestAssignMasterSlaveIP(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.102.77-slave"
	c := "10.10.103.155-build"
	d := "10.10.103.152-slave"
	e := "10.10.104.15-slave"
	f := "10.10.105.14-slave"
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.35",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.36",
						NodeName: &b,
					},
					{
						IP:       "10.168.33.119",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.120",
						NodeName: &c,
					},
					{
						IP:       "10.168.9.186",
						NodeName: &d,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
	t.Logf("create masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)

	oldEndpoints := endpoints
	newEndpoints := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.35",
						NodeName: &b,
					},
					{
						IP:       "10.168.132.36",
						NodeName: &b,
					},
					{
						IP:       "10.168.33.119",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.120",
						NodeName: &c,
					},
					{
						IP:       "10.168.9.186",
						NodeName: &d,
					},
					{
						IP:       "10.168.10.18",
						NodeName: &e,
					},
					{
						IP:       "10.168.11.192",
						NodeName: &f,
					},
					{
						IP:       "10.168.11.5",
						NodeName: &e,
					},
					{
						IP:       "10.168.12.9",
						NodeName: &e,
					},
				},
			},
		},
	}
	endpointAddresses, _ := rco.assignMasterSlaveIPAddress(newEndpoints, oldEndpoints)
	masterIP, slaveIP, err = rco.assignMasterSlaveIP(endpointAddresses)
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

func TestCreateAssignMasterSlaveIP(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.102.77-slave"
	c := "10.10.102.77-slave"
	d := "10.10.103.155-build"
	e := "10.10.103.155-build"
	f := "10.10.103.152-slave"
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
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

	for i := 0; i < 10; i++ {
		addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("create masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}

func TestCreateAssignMasterSlaveIPOneNode(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &a,
					},
				},
			},
		},
	}

	//masterIP: [10.168.131.105 10.168.132.44 10.168.132.45]
	//slaveIP: [10.168.33.66 10.168.33.67 10.168.9.134]
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
	t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
}

func TestCreateAssignMasterSlaveIPTwoNode(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	// 5a 1b
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &a,
					},
				},
			},
		},
	}

	//masterIP: [10.168.131.105 10.168.132.44 10.168.132.45]
	//slaveIP: [10.168.33.66 10.168.33.67 10.168.9.134]
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
	t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)

	// 4a 2b
	endpoints = &v1.Endpoints{
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &a,
					},
				},
			},
		},
	}

	//masterIP: [10.168.132.44 10.168.131.105 10.168.132.45]
	//slaveIP: [10.168.33.66 10.168.33.67 10.168.9.134]
	addresses, _ = rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err = rco.assignMasterSlaveIP(addresses)
	t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)

	// 3a 3b
	endpoints = &v1.Endpoints{
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}

	//masterIP: [10.168.131.105 10.168.132.44 10.168.132.45]
	//slaveIP: [10.168.33.67 10.168.33.66 10.168.9.134]
	addresses, _ = rco.assignMasterSlaveIPAddress(endpoints, nil)
	masterIP, slaveIP, err = rco.assignMasterSlaveIP(addresses)
	t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
}

func TestCreateAssignMasterSlaveIPThreeNode1(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"

	// 3a 2b 1c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &c,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
func TestCreateAssignMasterSlaveIPThreeNode2(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"
	// 4a 1b 1c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &c,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &a,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
func TestCreateAssignMasterSlaveIPThreeNode3(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"

	// 3a 2b 1c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &c,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
func TestCreateAssignMasterSlaveIPThreeNode4(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"

	// 2a 2b 2c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &a,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &c,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &b,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &c,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}

}
func TestCreateAssignMasterSlaveIPThreeNode5(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-cc"

	// 2a 3b 1c
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}

func TestCreateAssignMasterSlaveIPFourNode(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-bb"
	d := "10.10.103.154-cc"
	// 3a 1b 1c 1d
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &a,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &d,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}

	// 2a 2b 1c 1d
	endpoints = &v1.Endpoints{
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
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &c,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &d,
					},
				},
			},
		},
	}
	addresses, _ = rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}

	// 2a 2b 1c 1d
	endpoints = &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &c,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &d,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}
	addresses, _ = rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}
func TestCreateAssignMasterSlaveIPFourNode1(t *testing.T) {
	rco := &RedisClusterOperator{}

	a := "10.10.103.154-share"
	b := "10.10.103.154-aa"
	c := "10.10.103.154-bb"
	d := "10.10.103.154-cc"

	// 2a 2b 1c 1d
	endpoints := &v1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "example000-redis-cluster",
		},
		Subsets: []v1.EndpointSubset{
			{
				// c ("10.168.131.105") d("10.168.132.44")
				//a ("10.168.132.45", "10.168.33.66")  b("10.168.33.67", "10.168.9.134" )
				// [10.168.131.105", "10.168.132.44", "10.168.132.45", "10.168.9.134", "10.168.33.67", "10.168.33.66"  ]
				// Rotating the list sometimes helps to get better initial anti-affinity before the optimizer runs.
				Addresses: []v1.EndpointAddress{
					{
						IP:       "10.168.131.105",
						NodeName: &c,
					},
					{
						IP:       "10.168.132.44",
						NodeName: &d,
					},
					{
						IP:       "10.168.132.45",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.66",
						NodeName: &a,
					},
					{
						IP:       "10.168.33.67",
						NodeName: &b,
					},
					{
						IP:       "10.168.9.134",
						NodeName: &b,
					},
				},
			},
		},
	}
	addresses, _ := rco.assignMasterSlaveIPAddress(endpoints, nil)
	for i := 10000; i > 0; i-- {
		masterIP, slaveIP, err := rco.assignMasterSlaveIP(addresses)
		t.Logf("masterIP: %v\nslaveIP: %v\nerror: %v", masterIP, slaveIP, err)
	}
}

func TestSliceRemoveIndex(t *testing.T) {

	interleaved := []string{"0", "1", "2"}

	removeIndex := 0

	//remove assigned addr
	// if interleaved = ["0", "1", "2"]
	// removeIndex = 0 -- >> interleaved[:0], interleaved[1:]...  -- >> ["1", "2"]
	// removeIndex = 1 -- >> interleaved[:1], interleaved[2:]...  -- >> ["0", "2"]
	// removeIndex = 2 -- >> interleaved[:2], interleaved[3:]...  -- >> ["0", "1"]
	interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)
	t.Logf("interleaved: %v", interleaved)

	interleaved = []string{"0", "1", "2"}
	removeIndex = 1
	interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)
	t.Logf("interleaved: %v", interleaved)

	interleaved = []string{"0", "1", "2"}
	removeIndex = 2
	interleaved = append(interleaved[:removeIndex], interleaved[removeIndex+1:]...)
	t.Logf("interleaved: %v", interleaved)

	/*
		len(interleaved): 3 cap(interleaved): 3
		interleaved: []
	*/
	interleaved = []string{"0", "1", "2"}
	t.Logf("len(interleaved): %v cap(interleaved): %v", len(interleaved), cap(interleaved))
	t.Logf("interleaved: %v", interleaved[3:])

	/*
		i: 0 v:
		i: 1 v:
		i: 2 v:
	*/
	interleaved = make([]string, 3, 10)
	for i, v := range interleaved {
		t.Logf("i: %v v: %v", i, v)
	}

	/**
	len(interleaved): 3 cap(interleaved): 10
	interleaved: []
	*/
	t.Logf("len(interleaved): %v cap(interleaved): %v", len(interleaved), cap(interleaved))
	t.Logf("interleaved: %v", interleaved[3:])
	// panic: runtime error: slice bounds out of range
	//t.Logf("interleaved: %v", interleaved[4:])

	interleaved = []string{"0", "1", "2", "3", "4", "5", "6"}

	// interleaved: [3 4 5 6]
	interleaved = interleaved[3:]
	t.Logf("interleaved: %v", interleaved)

	// interleaved: [3 4]
	interleaved = interleaved[0:2]
	t.Logf("interleaved: %v", interleaved)
}

func TestLoopMap(t *testing.T) {
	m := make(map[string]string)
	m["hello"] = "echo hello"
	m["world"] = "echo world"
	m["go"] = "echo go"
	m["is"] = "echo is"
	m["cool"] = "echo cool"

	sortedKeys := make([]string, 0)
	for k := range m {
		fmt.Println("k--", k)
		sortedKeys = append(sortedKeys, k)
	}

	// sort 'string' key in increasing order
	sort.Strings(sortedKeys)

	for _, k := range sortedKeys {
		fmt.Printf("k=%v, v=%v\n", k, m[k])
	}
}

func TestDeepEqualExcludeFiled(t *testing.T) {

	tempStatus1 := v1alpha1.RedisClusterStatus{
		Conditions: []v1alpha1.RedisClusterCondition{
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.122",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "xxxx",
				Name:               "redis-cluster-0",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.122",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "qqqqqq",
				Name:               "redis-cluster-0",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
		},
	}

	tempStatus2 := v1alpha1.RedisClusterStatus{
		Conditions: []v1alpha1.RedisClusterCondition{
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.123",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "qqqqqq",
				Name:               "redis-cluster-1",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
			{
				DomainName:         "redis-cluster-0.redis-cluster.kube-system.svc.cluster.local",
				HostIP:             "192.168.26.1",
				Hostname:           "docker-vm-3",
				LastTransitionTime: metav1.Time{},
				Message:            "xxxx",
				Name:               "redis-cluster-0",
				NodeId:             "allkk111snknkcs",
				Reason:             "xxxx",
				Slots:              "1024",
				Status:             "False",
				Type:               "master",
			},
		},
	}

	t.Logf("Before sort Conditions: %v", tempStatus2.Conditions)

	sort.SliceStable(tempStatus2.Conditions, func(i, j int) bool {
		name1 := tempStatus2.Conditions[i].Name
		name2 := tempStatus2.Conditions[j].Name
		return name1 < name2
	})

	t.Logf("After sort Conditions: %v", tempStatus2.Conditions)
	t.Logf("tempStatus1 equal tempStatus2: %v", util.DeepEqualRedisClusterStatus(tempStatus1, tempStatus2))

}

func TestRedisTribInfoReg(t *testing.T) {
	reg := `([\d.]+):6379 \((\w+)...\) -> (\d+) keys \| (\d+) slots \| (\d+) slaves`
	infos := `10.168.33.80:6379 (9ffde2b6...) -> 0 keys | 5461 slots | 1 slaves.
10.168.9.165:6379 (c9537d65...) -> 0 keys | 5461 slots | 1 slaves.
10.168.32.72:6379 (27288e18...) -> 0 keys | 5462 slots | 1 slaves.
[OK] 0 keys in 3 masters.
0.00 keys per slot on average.`

	compile := regexp.MustCompile(reg)

	submatch := compile.FindStringSubmatch(infos)

	for i, v := range submatch {
		t.Log(i, " -->> ", v)
	}
}

func TestChangeEndpoints(t *testing.T) {
	endpoints := &v1.Endpoints{
		Subsets: []v1.EndpointSubset{
			{
				Addresses: []v1.EndpointAddress{
					{
						IP: "10.168.131.105",
						TargetRef: &v1.ObjectReference{
							Name:      "pod3",
							Namespace: "redis",
						},
					},
					{
						IP: "10.168.132.44",
						TargetRef: &v1.ObjectReference{
							Name:      "pod1",
							Namespace: "redis",
						},
					},
					{
						IP: "10.168.132.45",
						TargetRef: &v1.ObjectReference{
							Name:      "pod2",
							Namespace: "redis",
						},
					},
				},
			},
		},
	}

	sortEndpointsByPodName(endpoints)

	t.Log(endpoints)
}

func TestComposeMasterSlaveIP(t *testing.T) {

	nodeName0 := "10.10.103.66-share"
	nodeName1 := "10.10.103.60-master"
	nodeName2 := "10.10.102.43-share"
	nodeName3 := "10.10.103.61-slave"
	nodeName4 := "10.10.103.66-share"
	nodeName5 := "10.10.103.60-master"
	nodeName6 := "10.10.103.66-share"
	nodeName7 := "10.10.102.43-share"
	nodeName8 := "10.10.103.66-share"
	nodeName9 := "10.10.103.60-master"

	newAddresses := []v1.EndpointAddress{
		{
			IP:       "10.168.7.224",
			Hostname: "example000-redis-cluster-0",
			NodeName: &nodeName0,
		},
		{
			IP:       "10.168.131.69",
			Hostname: "example000-redis-cluster-1",
			NodeName: &nodeName1,
		},
		{
			IP:       "10.168.167.89",
			Hostname: "example000-redis-cluster-2",
			NodeName: &nodeName2,
		},
		{
			IP:       "10.168.246.107",
			Hostname: "example000-redis-cluster-3",
			NodeName: &nodeName3,
		},
		{
			IP:       "10.168.7.227",
			Hostname: "example000-redis-cluster-4",
			NodeName: &nodeName4,
		},
		{
			IP:       "10.168.131.70",
			Hostname: "example000-redis-cluster-5",
			NodeName: &nodeName5,
		},
		{
			IP:       "10.168.7.229",
			Hostname: "example000-redis-cluster-6",
			NodeName: &nodeName6,
		},
		{
			IP:       "10.168.167.90",
			Hostname: "example000-redis-cluster-7",
			NodeName: &nodeName7,
		},
		{
			IP:       "10.168.7.193",
			Hostname: "example000-redis-cluster-8",
			NodeName: &nodeName8,
		},
		{
			IP:       "10.168.131.75",
			Hostname: "example000-redis-cluster-9",
			NodeName: &nodeName9,
		},
	}

	existedMasterInstanceIPs := []string{"10.168.167.89", "10.168.131.69", "10.168.246.107"}
	existedSlaveInstanceIPs := []string{"10.168.131.70", "10.168.7.227", "10.168.7.224"}

	masterSlaveConnector := make(map[string]string, 3)
	masterSlaveConnector["10.168.167.89"] = "10.168.131.70"
	masterSlaveConnector["10.168.131.69"] = "10.168.7.227"
	masterSlaveConnector["10.168.246.107"] = "10.168.7.224"

	willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, err := composeMasterSlaveIP(newAddresses, existedMasterInstanceIPs, existedSlaveInstanceIPs, masterSlaveConnector)

	t.Logf("\nwillAddClusterMasterIPs: %v \nwillAddClusterSlaveIPs: %v \nslaveParentIps: %v \nerr: %v\n", willAddClusterMasterIPs, willAddClusterSlaveIPs, slaveParentIps, err)
}
