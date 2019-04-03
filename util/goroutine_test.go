package util

import (
	"testing"
	"time"
)

func TestGetGID(t *testing.T) {
	go func() {
		t.Log(GetGID())
	}()
	go func() {
		t.Log(GetGID())
	}()
	go func() {
		t.Log(GetGID())
	}()
	go func() {
		t.Log(GetGID())
	}()

	time.Sleep(2 * time.Second)
}
