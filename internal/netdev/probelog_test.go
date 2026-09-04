package netdev

import (
	"reflect"
	"testing"
)

func TestParseRunningServices(t *testing.T) {
	out := `  UNIT LOAD ACTIVE SUB DESCRIPTION
  sshd.service loaded active running OpenSSH server daemon
  nginx.service loaded active running nginx web server
  crond.service loaded active running Command Scheduler
  systemd-journald.service loaded active running Journal Service
  user@1000.service loaded active running User Manager for UID 1000

3 loaded units listed. Pass --all to see loaded but inactive units.`
	got := parseRunningServices(out)
	want := []string{"crond", "nginx", "sshd", "systemd-journald", "user@1000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestParseLsFiles(t *testing.T) {
	out := `total 21M
drwxr-xr-x.  3 root root 4.0K Sep  3 08:00 audit
-rw-------.  1 root root 814K Sep  3 10:00 messages
-rw-------.  1 root root  42K Sep  3 09:00 secure
-rw-------.  1 root root  26K Aug 30 10:00 messages-20260830
-rw-------.  1 root root  12K Aug 23 10:00 messages.1.gz
-rw-------.  1 root root  30K Sep  3 10:00 btmp
-rw-r-----.  1 root root 2.1M Sep  3 10:01 nginx-access.log
lrwxrwxrwx.  1 root root   11 Jul  1 08:00 mail -> spool/mail`
	got := parseLsFiles(out, "/var/log", true)
	want := []ProbeFile{
		{Name: "btmp", Path: "/var/log/btmp", Size: "30K", Allowed: true},
		{Name: "messages", Path: "/var/log/messages", Size: "814K", Allowed: true},
		{Name: "nginx-access.log", Path: "/var/log/nginx-access.log", Size: "2.1M", Allowed: true},
		{Name: "secure", Path: "/var/log/secure", Size: "42K", Allowed: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}

	// App dir listing: same parser, allowed=false marks whitelist-pending.
	app := `-rw-r--r--. 1 app app 5.2M Sep  3 11:00 app.log
-rw-r--r--. 1 app app 1.1M Sep  2 11:00 app.log.1.gz
drwxr-xr-x. 2 app app 4.0K Sep  3 11:00 old`
	gotApp := parseLsFiles(app, "/opt/myapp/logs", false)
	wantApp := []ProbeFile{{Name: "app.log", Path: "/opt/myapp/logs/app.log", Size: "5.2M", Allowed: false}}
	if !reflect.DeepEqual(gotApp, wantApp) {
		t.Fatalf("app dir: got %+v want %+v", gotApp, wantApp)
	}
}

func TestParseDockerPs(t *testing.T) {
	out := `CONTAINER ID   IMAGE          COMMAND                  CREATED       STATUS         PORTS                    NAMES
9f3c2a1b0c4d   nginx:latest   "/docker-entrypoint.…"   3 hours ago   Up 3 hours    0.0.0.0:80->80/tcp, :::80->80/tcp   web
7a1e9d2c3f4b   redis:7        "docker-entrypoint.s…"   5 days ago    Up 5 days     6379/tcp                 cache`
	got := parseDockerPs(out)
	want := []string{"cache", "web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
