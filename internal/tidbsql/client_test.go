package tidbsql

import (
	"reflect"
	"testing"

	"titop/internal/monitor"
)

func TestSQLAddresses(t *testing.T) {
	instances := []monitor.Instance{
		{Name: "10.0.0.2:10080", Role: "TIDB", Status: "UP"},
		{Name: "[2001:db8::1]:10080", Role: "TIDB", Status: "UP"},
		{Name: "10.0.0.1:10080", Role: "TIDB", Status: "DOWN"},
		{Name: "10.0.0.3:20180", Role: "TIKV", Status: "UP"},
	}
	want := []string{"10.0.0.2:4000", "[2001:db8::1]:4000"}
	if got := sqlAddresses(instances); !reflect.DeepEqual(got, want) {
		t.Fatalf("sqlAddresses() = %#v, want %#v", got, want)
	}
}

func TestResourceBytes(t *testing.T) {
	tests := []struct {
		value string
		want  int64
	}{
		{"104857600", 104857600},
		{"0", 0},
		{"18446744073709098156", 0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		if got := resourceBytes([]byte(tt.value)); got != tt.want {
			t.Errorf("resourceBytes(%q) = %d, want %d", tt.value, got, tt.want)
		}
	}
}

func TestClusterTLSStatus(t *testing.T) {
	configs := func(value string) []tlsConfig {
		return []tlsConfig{
			{component: "tidb", instance: "tidb-1", key: "ca", value: value},
			{component: "tidb", instance: "tidb-1", key: "cert", value: value},
			{component: "tidb", instance: "tidb-1", key: "key", value: value},
			{component: "tikv", instance: "tikv-1", key: "ca", value: value},
			{component: "tikv", instance: "tikv-1", key: "cert", value: value},
			{component: "tikv", instance: "tikv-1", key: "key", value: value},
			{component: "pd", instance: "pd-1", key: "ca", value: value},
			{component: "pd", instance: "pd-1", key: "cert", value: value},
			{component: "pd", instance: "pd-1", key: "key", value: value},
		}
	}
	if got := clusterTLSStatus(configs("/tls/file.pem")); got != "ON" {
		t.Fatalf("configured TLS status = %q, want ON", got)
	}
	if got := clusterTLSStatus(configs("")); got != "OFF" {
		t.Fatalf("empty TLS status = %q, want OFF", got)
	}
	mixed := configs("/tls/file.pem")
	mixed[4].value = ""
	if got := clusterTLSStatus(mixed); got != "INCONSISTENT" {
		t.Fatalf("mixed TLS status = %q, want INCONSISTENT", got)
	}
	if got := clusterTLSStatus(nil); got != "UNKNOWN" {
		t.Fatalf("missing TLS status = %q, want UNKNOWN", got)
	}
}
