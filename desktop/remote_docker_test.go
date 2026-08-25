package main

import "testing"

func TestParseDockerPS(t *testing.T) {
	out := `{"Command":"\"docker-entrypoint.…\"","CreatedAt":"2026-08-24 14:00:00 +0800 CST","ID":"abc123","Image":"redis:7","Labels":"","LocalVolumes":"0","Mounts":"","Names":"redis","Networks":"bridge","Ports":"6379/tcp","RunningFor":"2 minutes","Size":"0B","State":"running","Status":"Up 2 minutes"}
{"Command":"nginx -g 'daemon off;'","CreatedAt":"2026-08-24 13:59:00 +0800 CST","ID":"def456","Image":"nginx:latest","Names":"docker-nginx-1,docker-api-1","State":"running","Status":"Up 3 minutes"}

not-json-garbage`
	got := parseDockerPS(out)
	if len(got) != 2 {
		t.Fatalf("parsed %d containers, want 2: %+v", len(got), got)
	}
	if got[0].Names != "redis" || got[0].Image != "redis:7" || got[0].Status != "Up 2 minutes" {
		t.Fatalf("container[0] = %+v", got[0])
	}
	if got[1].Names != "docker-nginx-1,docker-api-1" || got[1].ID != "def456" {
		t.Fatalf("container[1] = %+v", got[1])
	}
	if parseDockerPS("") != nil && len(parseDockerPS("")) != 0 {
		t.Fatal("empty input should parse to no containers")
	}
}
