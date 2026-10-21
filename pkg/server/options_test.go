package server

import (
	"reflect"
	"testing"
)

func TestWithAlertmanagerUrlKeepsList(t *testing.T) {
	list := []string{"http://am1:9092", "http://am2:9092"}

	if _, err := NewServiceSyncServer(WithAlertmanagerUrl(list)); err != nil {
		t.Fatalf("NewServiceSyncServer() error = %v", err)
	}

	if want := []string{"http://am1:9092", "http://am2:9092"}; !reflect.DeepEqual(list, want) {
		t.Errorf("list = %v, want unchanged %v", list, want)
	}
}
