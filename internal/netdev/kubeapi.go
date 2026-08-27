package netdev

// kubeapi.go — kind=k8s 的极简只读 REST 客户端（NETDEV_SPEC_V2 §2.3）。
// 不引 client-go（一棵巨大的依赖树）：解析 kubeconfig（YAML，内容存 secret
// store），固定 context，对白名单资源路径发 GET。写 verb 无代码路径。
// 防 SSRF/context 逃逸（附录 B-7）：server 只来自 kubeconfig，工具层只接受
// target 名——不接受 kubeconfig 内容、context、server 任何参数。

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const kubeBodyCap = 256 * 1024

// kubeNameRe constrains namespace / resource names used in paths.
var kubeNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// kubeConfigFile is the minimal kubeconfig surface we consume.
type kubeConfigFile struct {
	CurrentContext string `yaml:"current-context"`
	Clusters       []struct {
		Name    string `yaml:"name"`
		Cluster struct {
			Server                   string `yaml:"server"`
			CertificateAuthorityData string `yaml:"certificate-authority-data"`
			InsecureSkipTLSVerify    bool   `yaml:"insecure-skip-tls-verify"`
		} `yaml:"cluster"`
	} `yaml:"clusters"`
	Contexts []struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	} `yaml:"contexts"`
	Users []struct {
		Name string `yaml:"name"`
		User struct {
			Token                string `yaml:"token"`
			ClientCertificateData string `yaml:"client-certificate-data"`
			ClientKeyData         string `yaml:"client-key-data"`
		} `yaml:"user"`
	} `yaml:"users"`
}

// kubeTarget is the resolved connection for one device: pinned context.
type kubeTarget struct {
	client    *http.Client
	server    string
	token     string
	namespace string
}

func (m *Manager) kubeTarget(deviceName string) (*kubeTarget, error) {
	d, ok := m.cfg.NetDevDeviceByName(deviceName)
	if !ok {
		return nil, fmt.Errorf("device %q is not in the inventory (add it in the 运维 settings)", deviceName)
	}
	if d.Kind != "k8s" || d.K8s == nil {
		return nil, fmt.Errorf("device %q is not a kind=k8s target", deviceName)
	}
	if strings.TrimSpace(d.K8s.KubeconfigEnv) == "" {
		return nil, fmt.Errorf("device %q: k8s.kubeconfig_env (secret-store key holding the kubeconfig YAML) is required", deviceName)
	}
	yamlText, ok, err := secretGetter(SecretKindKubeconfig, d.K8s.KubeconfigEnv)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("secret kubeconfig/%s not set — paste the kubeconfig into the 运维 settings (content never enters TOML)", d.K8s.KubeconfigEnv)
	}
	var kc kubeConfigFile
	if err := yaml.Unmarshal([]byte(yamlText), &kc); err != nil {
		return nil, fmt.Errorf("kubeconfig parse: %v", err)
	}
	ctxName := d.K8s.Context
	if ctxName == "" {
		ctxName = kc.CurrentContext
	}
	var ctxRef *struct {
		Name    string `yaml:"name"`
		Context struct {
			Cluster   string `yaml:"cluster"`
			User      string `yaml:"user"`
			Namespace string `yaml:"namespace"`
		} `yaml:"context"`
	}
	for i := range kc.Contexts {
		if kc.Contexts[i].Name == ctxName {
			ctxRef = &kc.Contexts[i]
			break
		}
	}
	if ctxRef == nil {
		return nil, fmt.Errorf("context %q not found in kubeconfig (has: current=%q)", ctxName, kc.CurrentContext)
	}
	var server string
	var caData []byte
	var insecure bool
	for _, c := range kc.Clusters {
		if c.Name == ctxRef.Context.Cluster {
			server = strings.TrimRight(c.Cluster.Server, "/")
			if c.Cluster.CertificateAuthorityData != "" {
				caData, _ = base64.StdEncoding.DecodeString(c.Cluster.CertificateAuthorityData)
			}
			insecure = c.Cluster.InsecureSkipTLSVerify
			break
		}
	}
	if server == "" {
		return nil, fmt.Errorf("cluster %q (context %q) has no server in kubeconfig", ctxRef.Context.Cluster, ctxName)
	}
	var token, certPEM, keyPEM string
	for _, u := range kc.Users {
		if u.Name == ctxRef.Context.User {
			token = u.User.Token
			if u.User.ClientCertificateData != "" {
				b, _ := base64.StdEncoding.DecodeString(u.User.ClientCertificateData)
				certPEM = string(b)
			}
			if u.User.ClientKeyData != "" {
				b, _ := base64.StdEncoding.DecodeString(u.User.ClientKeyData)
				keyPEM = string(b)
			}
			break
		}
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: insecure} //nolint:gosec — mirrors kubeconfig's own flag
	if len(caData) > 0 {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caData)
		tlsCfg.RootCAs = pool
	}
	if certPEM != "" && keyPEM != "" {
		cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if err != nil {
			return nil, fmt.Errorf("kubeconfig client cert: %v", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	ns := ctxRef.Context.Namespace
	if ns == "" {
		ns = "default"
	}
	return &kubeTarget{
		client:    &http.Client{Transport: &http.Transport{TLSClientConfig: tlsCfg}, Timeout: 30 * time.Second},
		server:    server,
		token:     token,
		namespace: ns,
	}, nil
}

// KubeGet answers ONE whitelisted GET against the device's pinned context.
// what ∈ version | pods | pod | podlog | events | deployments | nodes.
func (m *Manager) KubeGet(ctx context.Context, deviceName, what, namespace, name string, tailN int) (string, error) {
	t, err := m.kubeTarget(deviceName)
	if err != nil {
		return "", err
	}
	d, _ := m.cfg.NetDevDeviceByName(deviceName)

	if namespace == "" {
		namespace = t.namespace
	}
	if what != "version" && what != "nodes" {
		if !kubeNameRe.MatchString(namespace) {
			return "", fmt.Errorf("invalid namespace %q", namespace)
		}
		// Namespace allowlist from the device's k8s config (empty = all).
		if d.K8s != nil && len(d.K8s.Namespaces) > 0 {
			allowed := false
			for _, ns := range d.K8s.Namespaces {
				if ns == namespace {
					allowed = true
					break
				}
			}
			if !allowed {
				return "", fmt.Errorf("namespace %q is outside this target's allowlist %v", namespace, d.K8s.Namespaces)
			}
		}
	}

	var path string
	switch what {
	case "version":
		path = "/version"
	case "nodes":
		path = "/api/v1/nodes"
	case "pods":
		path = "/api/v1/namespaces/" + namespace + "/pods?limit=200"
	case "events":
		path = "/api/v1/namespaces/" + namespace + "/events?limit=200"
	case "deployments":
		path = "/apis/apps/v1/namespaces/" + namespace + "/deployments?limit=200"
	case "pod", "podlog":
		if !kubeNameRe.MatchString(name) {
			return "", fmt.Errorf("invalid pod name %q", name)
		}
		if what == "pod" {
			path = "/api/v1/namespaces/" + namespace + "/pods/" + name
		} else {
			if tailN <= 0 || tailN > 1000 {
				tailN = 100
			}
			path = fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log?tailLines=%d", namespace, name, tailN)
		}
	default:
		return "", errors.New("what must be version|pods|pod|podlog|events|deployments|nodes")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.server+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	res, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, kubeBodyCap))
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return string(body), fmt.Errorf("kube API %s → HTTP %d", path, res.StatusCode)
	}
	return compactKubeJSON(path, string(body)), nil
}

// compactKubeJSON prunes the noisiest fields (managedFields) so a pod list
// stays context-friendly.
func compactKubeJSON(path, body string) string {
	if !strings.Contains(path, "/pods") && !strings.Contains(path, "/deployments") && !strings.Contains(path, "/nodes") {
		return body
	}
	var doc map[string]any
	if json.Unmarshal([]byte(body), &doc) != nil {
		return body
	}
	items, ok := doc["items"].([]any)
	if !ok {
		return body
	}
	for _, it := range items {
		if obj, ok := it.(map[string]any); ok {
			if md, ok := obj["metadata"].(map[string]any); ok {
				delete(md, "managedFields")
			}
		}
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return string(out)
}
