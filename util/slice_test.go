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

	for _, v := range diffslice  {
		if v != "c" && v != "d"{
			t.Error("should be contain c, d")
			return
		}
	}
}
