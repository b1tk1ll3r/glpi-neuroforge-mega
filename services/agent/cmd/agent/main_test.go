package main

import "testing"

func TestHealthcheckURL(t *testing.T) {
	for addr, want := range map[string]string{
		"":               "http://127.0.0.1:8080/healthz",
		":8080":          "http://127.0.0.1:8080/healthz",
		":9090":          "http://127.0.0.1:9090/healthz",
		"0.0.0.0:7000":   "http://127.0.0.1:7000/healthz",
		"[::]:7001":      "http://127.0.0.1:7001/healthz",
		"10.1.2.3:8443":  "http://10.1.2.3:8443/healthz",
		"invalid":        "http://127.0.0.1:8080/healthz",
		" :8081 ":        "http://127.0.0.1:8081/healthz",
		"localhost:8082": "http://localhost:8082/healthz",
	} {
		if got := healthcheckURL(addr); got != want {
			t.Errorf("healthcheckURL(%q)=%q want %q", addr, got, want)
		}
	}
}
