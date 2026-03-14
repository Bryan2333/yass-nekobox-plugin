package main

import (
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// NaiveConfig 定义了 NekoBox 传过来的 NaiveProxy 配置结构
type NaiveConfig struct {
	Listen              string `json:"listen"`
	Proxy               string `json:"proxy"`
	InsecureConcurrency int    `json:"insecure-concurrency,omitempty"`
	HostResolverRules   string `json:"host-resolver-rules,omitempty"` // [新增] 解析本地映射规则
}

// YassConfig 定义了 Yass CLI 需要的配置结构
type YassConfig struct {
	Local                  string `json:"local"`
	LocalPort              int    `json:"local_port"`
	Method                 string `json:"method"`
	Server                 string `json:"server"`
	ServerSni              string `json:"server_sni"`
	ServerPort             int    `json:"server_port"`
	ConnectTimeout         int    `json:"connect_timeout"`
	Username               string `json:"username"`
	Password               string `json:"password"`
	InsecureMode           bool   `json:"insecure_mode"`
	CertificateChainFile   string `json:"certificate_chain_file"`
	EnablePostQuantumKyber bool   `json:"enable_post_quantum_kyber"`
	TcpCongestionAlgorithm string `json:"tcp_congestion_algorithm"`
}

func main() {
	// 1. 获取 NekoBox 传递的 Naive 配置文件路径
	var configPath string
	if len(os.Args) > 1 {
		configPath = os.Args[len(os.Args)-1]
	}

	if configPath == "" || !strings.HasSuffix(configPath, ".json") {
		log.Fatalf("错误: 未能找到 JSON 配置文件路径。参数: %v", os.Args)
	}

	// 2. 读取 Naive 配置文件
	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("读取 naive 配置文件失败: %v", err)
	}

	// 3. 解析 Naive 配置
	var naive NaiveConfig
	if err := json.Unmarshal(configBytes, &naive); err != nil {
		log.Fatalf("解析 naive JSON 失败: %v", err)
	}

	// 4. 解析监听地址 (Listen: socks://127.0.0.1:1080)
	listenURL, err := url.Parse(naive.Listen)
	if err != nil {
		log.Fatalf("解析 Listen URL 失败: %v", err)
	}
	localPort, _ := strconv.Atoi(listenURL.Port())
	localIP := listenURL.Hostname()

	// 5. 解析远程服务器信息 (Proxy: https://user:pass@example.com:443)
	proxyURL, err := url.Parse(naive.Proxy)
	if err != nil {
		log.Fatalf("解析 Proxy URL 失败: %v", err)
	}
	serverPort, _ := strconv.Atoi(proxyURL.Port())
	if serverPort == 0 {
		if proxyURL.Scheme == "https" {
			serverPort = 443
		} else {
			serverPort = 80
		}
	}
	username := proxyURL.User.Username()
	password, _ := proxyURL.User.Password()

	// [新增核心逻辑] 提取 NekoBox 要求映射的本地 IP (绕过路由死循环)
	serverAddr := proxyURL.Hostname()
	if naive.HostResolverRules != "" {
		// NekoBox 生成的规则通常长这样: "MAP example.com 127.0.0.1" 或 "MAP example.com [::1]"
		parts := strings.Split(naive.HostResolverRules, " ")
		if len(parts) >= 3 && parts[0] == "MAP" {
			serverAddr = parts[2] // 将要连接的服务器强制改成映射的 IP
		}
	}

	// 6. 组装 Yass 的配置
	yass := YassConfig{
		Local:                  localIP,
		LocalPort:              localPort,
		Method:                 "http2",
		Server:                 serverAddr,          // 连接到 NekoBox 提供的本地端口 (如 127.0.0.1)
		ServerSni:              proxyURL.Hostname(), // SNI 必须保持为真实的域名，否则 TLS 握手会失败
		ServerPort:             serverPort,
		ConnectTimeout:         2000,
		Username:               username,
		Password:               password,
		InsecureMode:           false,
		CertificateChainFile:   "",
		EnablePostQuantumKyber: true,
		TcpCongestionAlgorithm: "",
	}

	// 7. 将 Yass 配置写入文件
	yassConfigBytes, err := json.MarshalIndent(yass, "", "    ")
	if err != nil {
		log.Fatalf("生成 Yass 配置失败: %v", err)
	}

	yassConfigPath := filepath.Join(filepath.Dir(configPath), "yass_generated.json")
	if err := os.WriteFile(yassConfigPath, yassConfigBytes, 0600); err != nil {
		log.Fatalf("写入 Yass 配置文件失败: %v", err)
	}

	// 8. 寻找真正的 yass_cli 可执行文件
	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("无法获取当前执行路径: %v", err)
	}
	yassBinaryPath := filepath.Join(filepath.Dir(execPath), "libyass_cli.so")

	if _, err := os.Stat(yassBinaryPath); os.IsNotExist(err) {
		log.Fatalf("找不到核心文件: %s", yassBinaryPath)
	}

	// 9. 使用 syscall.Exec 替换进程
	// 根据 yass_cli --help 输出，使用 -K 或 --config 来指定配置文件
	yassArgs := []string{"libyass_cli.so", "-K", yassConfigPath}
	if err := syscall.Exec(yassBinaryPath, yassArgs, os.Environ()); err != nil {
		log.Fatalf("执行 yass 失败: %v", err)
	}
}
