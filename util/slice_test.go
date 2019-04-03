// Copyright 2014 beego Author. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package util

import (
	"testing"
)

func TestInSlice(t *testing.T) {
	sl := []string{"A", "b"}
	if !InSlice("A", sl) {
		t.Error("should be true")
	}
	if InSlice("B", sl) {
		t.Error("should be false")
	}
}

func TestSliceStringDiff(t *testing.T) {
	s1 := []string{"A", "b", "c", "d"}
	s2 := []string{"A", "b"}

	diffslice := SliceStringDiff(s1, s2)

	if len(diffslice) != 2 {
		t.Error("should be contain c, d")
		return
	}

	for _, v := range diffslice {
		if v != "c" && v != "d" {
			t.Error("should be contain c, d")
			return
		}
	}
}

func TestAllocSlots(t *testing.T) {
	/*	The first step is to split instances by IP. This is useful as
		# we'll try to allocate master nodes in different physical machines
		# (as much as possible) and to allocate slaves of a given master in
		# different physical machines as well.
		#
		# This code assumes just that if the IP is different, than it is more
		# likely that the instance is running in a different physical host
		# or at least a different virtual machine.
		第一步是按IP拆分实例。这很有用
		＃我们将尝试在不同的物理机器中分配主节点＃（尽可能多）并在
		＃不同的物理机器中分配给定主机的从机。
		##此代码假定如果IP不同，则表示实例正在另一个物理主机
		＃或至少一个不同的虚拟机中运行。
	*/
}
