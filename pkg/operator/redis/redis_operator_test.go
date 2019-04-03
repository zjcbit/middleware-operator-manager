package redis

import (
	"errors"
	"fmt"
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
